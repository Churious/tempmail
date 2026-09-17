package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/microcosm-cc/bluemonday"
	_ "modernc.org/sqlite"
)

func testApp(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	a := &app{db: db, cfg: config{domain: "example.com", attachmentDir: filepath.Join(dir, "attachments"), ttl: time.Hour}, clients: make(map[string]map[*websocket.Conn]struct{}), cleaner: bluemonday.UGCPolicy()}
	if err := os.MkdirAll(a.cfg.attachmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.initDB(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return a
}

func TestMailboxRetentionAndDeletion(t *testing.T) {
	a := testApp(t)
	create := func(localPart, retention string) map[string]any {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/mailboxes", strings.NewReader(`{"local_part":"`+localPart+`","retention":"`+retention+`"}`))
		response := httptest.NewRecorder()
		a.handleMailboxes(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("create returned %d: %s", response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	temporary := create("short", "10m")
	if temporary["expires_at"] == nil || temporary["is_preserved"] != false {
		t.Fatalf("unexpected temporary mailbox: %#v", temporary)
	}
	permanent := create("forever", "lifetime")
	if permanent["expires_at"] != nil || permanent["is_preserved"] != true {
		t.Fatalf("unexpected lifetime mailbox: %#v", permanent)
	}

	var mailboxID string
	if err := a.db.QueryRow(`SELECT id FROM mailboxes WHERE address=?`, temporary["address"]).Scan(&mailboxID); err != nil {
		t.Fatal(err)
	}
	attachmentPath := filepath.Join(a.cfg.attachmentDir, "delete-me")
	if err := os.WriteFile(attachmentPath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO messages(id,mailbox_id,sender_address,subject,received_at) VALUES(?,?,?,?,?)`, "message", mailboxID, "sender@example.net", "delete", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO attachments(id,message_id,filename,size,path) VALUES(?,?,?,?,?)`, "attachment", "message", "file.txt", 4, attachmentPath); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/mailboxes/"+temporary["address"].(string), nil)
	request.Header.Set("Authorization", "Bearer "+temporary["token"].(string))
	response := httptest.NewRecorder()
	a.handleMessageList(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("delete returned %d: %s", response.Code, response.Body.String())
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM mailboxes WHERE id=?`, mailboxID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("mailbox was not deleted: count=%d err=%v", count, err)
	}
	if _, err := os.Stat(attachmentPath); !os.IsNotExist(err) {
		t.Fatalf("attachment was not deleted: %v", err)
	}
}

func TestStoreMessageParsesAndSanitizesMIME(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO mailboxes(id,address,expires_at,created_at) VALUES(?,?,?,?)`, "box", "test@example.com", time.Now().Add(time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Join([]string{
		"From: Sender <sender@example.net>",
		"To: test@example.com",
		"Subject: MIME test",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="mixed"`,
		"", "--mixed", `Content-Type: multipart/alternative; boundary="alt"`,
		"", "--alt", "Content-Type: text/plain; charset=utf-8", "", "Plain body",
		"--alt", "Content-Type: text/html; charset=utf-8", "", `<p>Hello</p><script>alert(1)</script>`, "--alt--",
		"--mixed", `Content-Type: text/plain; name="note.txt"`, `Content-Disposition: attachment; filename="note.txt"`, "Content-Transfer-Encoding: base64", "", "YXR0YWNobWVudA==", "--mixed--", "",
	}, "\r\n")
	if err := a.storeMessage("test@example.com", "sender@example.net", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	var subject, textBody, htmlBody string
	if err := a.db.QueryRow(`SELECT subject,body_text,body_html FROM messages`).Scan(&subject, &textBody, &htmlBody); err != nil {
		t.Fatal(err)
	}
	if subject != "MIME test" || !strings.Contains(textBody, "Plain body") {
		t.Fatalf("unexpected message: %q %q", subject, textBody)
	}
	if strings.Contains(strings.ToLower(htmlBody), "script") {
		t.Fatalf("unsafe HTML was stored: %s", htmlBody)
	}
	var path, filename string
	if err := a.db.QueryRow(`SELECT path,filename FROM attachments`).Scan(&path, &filename); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if filename != "note.txt" || string(content) != "attachment" {
		t.Fatalf("unexpected attachment %q: %q", filename, content)
	}
}

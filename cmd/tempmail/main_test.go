package main

import (
	"database/sql"
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

package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/gorilla/websocket"
	"github.com/jhillyerd/enmime"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type config struct {
	domain, dbPath, attachmentDir, httpAddr, smtpAddr string
	ttl                                               time.Duration
}

type session struct{ MailboxID, Address string }

type app struct {
	db       *sql.DB
	cfg      config
	sessions sync.Map
	clients  map[string]map[*websocket.Conn]struct{}
	mu       sync.Mutex
	cleaner  *bluemonday.Policy
}

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func main() {
	cfg := config{
		domain:        env("DOMAIN", "localhost"),
		dbPath:        env("DB_PATH", "./data/tempmail.db"),
		attachmentDir: env("ATTACHMENT_DIR", "./data/attachments"),
		httpAddr:      env("HTTP_ADDR", ":8080"),
		smtpAddr:      env("SMTP_ADDR", ":25"),
		ttl:           time.Duration(envInt("DEFAULT_TTL_HOURS", 1)) * time.Hour,
	}
	if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.attachmentDir, 0o755); err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("sqlite", cfg.dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatal(err)
	}
	a := &app{db: db, cfg: cfg, clients: make(map[string]map[*websocket.Conn]struct{}), cleaner: bluemonday.UGCPolicy()}
	if err := a.initDB(); err != nil {
		log.Fatal(err)
	}
	go a.cleanupLoop()
	go a.serveSMTP()
	log.Printf("HTTP %s, SMTP %s, domain %s", cfg.httpAddr, cfg.smtpAddr, cfg.domain)
	log.Fatal(http.ListenAndServe(cfg.httpAddr, a.routes()))
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(env(key, ""))
	if err != nil {
		return fallback
	}
	return v
}
func newID() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }

func (a *app) initDB() error {
	_, err := a.db.Exec(`
CREATE TABLE IF NOT EXISTS mailboxes (
 id TEXT PRIMARY KEY, address TEXT UNIQUE NOT NULL, is_preserved INTEGER NOT NULL DEFAULT 0,
 password_hash TEXT, expires_at DATETIME, created_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
 id TEXT PRIMARY KEY, mailbox_id TEXT NOT NULL, sender_name TEXT, sender_address TEXT NOT NULL,
 subject TEXT NOT NULL DEFAULT '', body_text TEXT, body_html TEXT, received_at DATETIME NOT NULL,
 FOREIGN KEY(mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS attachments (
 id TEXT PRIMARY KEY, message_id TEXT NOT NULL, filename TEXT NOT NULL, mime_type TEXT,
 size INTEGER NOT NULL, path TEXT NOT NULL,
 FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_messages_mailbox_received ON messages(mailbox_id, received_at DESC);
CREATE INDEX IF NOT EXISTS idx_attachments_message ON attachments(message_id);
`)
	return err
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mailboxes", a.handleMailboxes)
	mux.HandleFunc("/api/v1/mailboxes/preserve", a.handlePreserve)
	mux.HandleFunc("/api/v1/mailboxes/restore", a.handleRestore)
	mux.HandleFunc("/api/v1/mailboxes/", a.handleMessageList)
	mux.HandleFunc("/api/v1/messages/", a.handleMessage)
	mux.HandleFunc("/api/v1/attachments/", a.handleAttachment)
	mux.HandleFunc("/ws/", a.handleWS)
	mux.Handle("/", http.FileServer(http.Dir("./web")))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func (a *app) authenticate(r *http.Request) (session, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	v, ok := a.sessions.Load(token)
	if !ok {
		return session{}, false
	}
	return v.(session), true
}

func validLocalPart(v string) bool {
	if len(v) < 3 || len(v) > 32 {
		return false
	}
	for _, ch := range v {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func (a *app) handleMailboxes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input struct {
		LocalPart string `json:"local_part"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	local := strings.ToLower(strings.TrimSpace(input.LocalPart))
	if local == "" {
		local = newID()[:10]
	}
	if !validLocalPart(local) {
		writeError(w, http.StatusBadRequest, "local_part must be 3-32 lowercase letters, numbers, - or _")
		return
	}
	now, mailboxID := time.Now().UTC(), newID()
	address := local + "@" + strings.ToLower(a.cfg.domain)
	expiresAt := now.Add(a.cfg.ttl)
	if _, err := a.db.Exec(`INSERT INTO mailboxes(id,address,expires_at,created_at) VALUES(?,?,?,?)`, mailboxID, address, expiresAt, now); err != nil {
		writeError(w, http.StatusConflict, "address unavailable")
		return
	}
	token := newID()
	a.sessions.Store(token, session{mailboxID, address})
	writeJSON(w, http.StatusCreated, map[string]any{"id": mailboxID, "address": address, "token": token, "expires_at": expiresAt, "is_preserved": false})
}

func (a *app) handlePreserve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s, ok := a.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Password) < 8 || len(input.Password) > 128 {
		writeError(w, http.StatusBadRequest, "password must be 8-128 characters")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, "failed to hash password")
		return
	}
	if _, err = a.db.Exec(`UPDATE mailboxes SET is_preserved=1,password_hash=?,expires_at=NULL WHERE id=?`, hash, s.MailboxID); err != nil {
		writeError(w, 500, "failed to preserve mailbox")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "address": s.Address})
}

func (a *app) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input struct{ Address, Password string }
	if !decodeJSON(w, r, &input) {
		return
	}
	var s session
	var hash string
	err := a.db.QueryRow(`SELECT id,address,password_hash FROM mailboxes WHERE lower(address)=lower(?) AND is_preserved=1`, strings.TrimSpace(input.Address)).Scan(&s.MailboxID, &s.Address, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid address or password")
		return
	}
	token := newID()
	a.sessions.Store(token, s)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "address": s.Address, "expires_at": nil, "is_preserved": true})
}

func (a *app) handleMessageList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s, ok := a.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/mailboxes/")
	if !strings.HasSuffix(path, "/messages") {
		http.NotFound(w, r)
		return
	}
	address := strings.TrimSuffix(path, "/messages")
	if address != s.Address {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	rows, err := a.db.Query(`SELECT id,COALESCE(sender_name,''),sender_address,subject,received_at FROM messages WHERE mailbox_id=? ORDER BY received_at DESC`, s.MailboxID)
	if err != nil {
		writeError(w, 500, "failed to list messages")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, from, subject string
		var received time.Time
		if rows.Scan(&id, &name, &from, &subject, &received) == nil {
			items = append(items, map[string]any{"id": id, "sender_name": name, "sender_address": from, "subject": subject, "received_at": received})
		}
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *app) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s, ok := a.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	messageID := strings.TrimPrefix(r.URL.Path, "/api/v1/messages/")
	var mailboxID, name, from, subject, textBody, htmlBody string
	var received time.Time
	err := a.db.QueryRow(`SELECT mailbox_id,COALESCE(sender_name,''),sender_address,subject,COALESCE(body_text,''),COALESCE(body_html,''),received_at FROM messages WHERE id=?`, messageID).Scan(&mailboxID, &name, &from, &subject, &textBody, &htmlBody, &received)
	if err != nil || mailboxID != s.MailboxID {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	attachments := make([]map[string]any, 0)
	rows, _ := a.db.Query(`SELECT id,filename,COALESCE(mime_type,''),size FROM attachments WHERE message_id=?`, messageID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var id, filename, mimeType string
			var size int64
			if rows.Scan(&id, &filename, &mimeType, &size) == nil {
				attachments = append(attachments, map[string]any{"id": id, "filename": filename, "mime_type": mimeType, "size": size})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": messageID, "sender_name": name, "sender_address": from, "subject": subject, "body_text": textBody, "body_html": htmlBody, "received_at": received, "attachments": attachments})
}

func (a *app) handleAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s, ok := a.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/attachments/"), "/download")
	var path, filename, mimeType string
	err := a.db.QueryRow(`SELECT a.path,a.filename,COALESCE(a.mime_type,'') FROM attachments a JOIN messages m ON m.id=a.message_id WHERE a.id=? AND m.mailbox_id=?`, id, s.MailboxID).Scan(&path, &filename, &mimeType)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", strings.ReplaceAll(filename, `"`, "")))
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	http.ServeFile(w, r, path)
}

func (a *app) handleWS(w http.ResponseWriter, r *http.Request) {
	s, ok := a.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	address := strings.TrimPrefix(r.URL.Path, "/ws/")
	if address != s.Address {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	a.mu.Lock()
	if a.clients[s.MailboxID] == nil {
		a.clients[s.MailboxID] = make(map[*websocket.Conn]struct{})
	}
	a.clients[s.MailboxID][conn] = struct{}{}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.clients[s.MailboxID], conn); a.mu.Unlock(); _ = conn.Close() }()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (a *app) notify(mailboxID string, event any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for conn := range a.clients[mailboxID] {
		if err := conn.WriteJSON(event); err != nil {
			_ = conn.Close()
			delete(a.clients[mailboxID], conn)
		}
	}
}

func (a *app) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		a.cleanupExpired()
	}
}

func (a *app) cleanupExpired() {
	rows, err := a.db.Query(`SELECT a.path FROM attachments a JOIN messages m ON m.id=a.message_id JOIN mailboxes b ON b.id=m.mailbox_id WHERE b.is_preserved=0 AND b.expires_at<?`, time.Now().UTC())
	if err == nil {
		var paths []string
		for rows.Next() {
			var path string
			if rows.Scan(&path) == nil {
				paths = append(paths, path)
			}
		}
		rows.Close()
		for _, path := range paths {
			_ = os.Remove(path)
		}
	}
	_, _ = a.db.Exec(`DELETE FROM mailboxes WHERE is_preserved=0 AND expires_at<?`, time.Now().UTC())
}

type smtpBackend struct{ app *app }
type smtpSession struct {
	app        *app
	from       string
	recipients []string
}

func (b *smtpBackend) NewSession(*smtp.Conn) (smtp.Session, error) {
	return &smtpSession{app: b.app}, nil
}
func (s *smtpSession) Mail(from string, _ *smtp.MailOptions) error { s.from = from; return nil }
func (s *smtpSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	to = strings.ToLower(strings.TrimSpace(to))
	var count int
	if err := s.app.db.QueryRow(`SELECT COUNT(*) FROM mailboxes WHERE lower(address)=? AND (is_preserved=1 OR expires_at>?)`, to, time.Now().UTC()).Scan(&count); err != nil || count == 0 {
		return &smtp.SMTPError{Code: 550, Message: "mailbox unavailable"}
	}
	s.recipients = append(s.recipients, to)
	return nil
}
func (s *smtpSession) Reset()        { s.from = ""; s.recipients = nil }
func (s *smtpSession) Logout() error { return nil }
func (s *smtpSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, 25<<20))
	if err != nil {
		return err
	}
	for _, recipient := range s.recipients {
		if err := s.app.storeMessage(recipient, s.from, raw); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) storeMessage(recipient, envelopeFrom string, raw []byte) error {
	var mailboxID string
	if err := a.db.QueryRow(`SELECT id FROM mailboxes WHERE lower(address)=?`, recipient).Scan(&mailboxID); err != nil {
		return err
	}
	envelope, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("parse MIME: %w", err)
	}
	messageID := newID()
	now := time.Now().UTC()
	fromName, fromAddress := "", envelopeFrom
	if parsed, err := mail.ParseAddress(envelope.GetHeader("From")); err == nil {
		fromName, fromAddress = parsed.Name, parsed.Address
	}
	subject := envelope.GetHeader("Subject")
	htmlBody := a.cleaner.Sanitize(envelope.HTML)
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO messages(id,mailbox_id,sender_name,sender_address,subject,body_text,body_html,received_at) VALUES(?,?,?,?,?,?,?,?)`, messageID, mailboxID, fromName, fromAddress, subject, envelope.Text, htmlBody, now); err != nil {
		return err
	}
	var created []string
	for _, part := range envelope.Attachments {
		attachmentID := newID()
		filename := filepath.Base(part.FileName)
		if filename == "." || filename == "" {
			filename = "attachment"
		}
		path := filepath.Join(a.cfg.attachmentDir, attachmentID)
		if err = os.WriteFile(path, part.Content, 0o600); err != nil {
			for _, p := range created {
				_ = os.Remove(p)
			}
			return err
		}
		created = append(created, path)
		if _, err = tx.Exec(`INSERT INTO attachments(id,message_id,filename,mime_type,size,path) VALUES(?,?,?,?,?,?)`, attachmentID, messageID, filename, part.ContentType, len(part.Content), path); err != nil {
			for _, p := range created {
				_ = os.Remove(p)
			}
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		for _, p := range created {
			_ = os.Remove(p)
		}
		return err
	}
	a.notify(mailboxID, map[string]any{"type": "new_message", "id": messageID, "subject": subject, "received_at": now})
	return nil
}

func (a *app) serveSMTP() {
	server := smtp.NewServer(&smtpBackend{app: a})
	server.Addr, server.Domain = a.cfg.smtpAddr, a.cfg.domain
	server.ReadTimeout, server.WriteTimeout = 30*time.Second, 30*time.Second
	server.MaxMessageBytes, server.MaxRecipients = 25<<20, 20
	server.AllowInsecureAuth = false
	if err := server.ListenAndServe(); err != nil {
		log.Printf("SMTP server stopped: %v", err)
	}
}

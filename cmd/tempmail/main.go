package main

import (
 "crypto/rand"
 "database/sql"
 "encoding/json"
 "fmt"
 "html/template"
 "io"
 "log"
 "mime"
 "net/http"
 "os"
 "path/filepath"
 "strconv"
 "strings"
 "sync"
 "time"

 "github.com/emersion/go-smtp"
 "github.com/gorilla/websocket"
 _ "github.com/mattn/go-sqlite3"
 "golang.org/x/crypto/bcrypt"
)

type App struct { db *sql.DB; domain, attach string; ttl time.Duration; clients map[string]map[*websocket.Conn]bool; mu sync.Mutex }
type session struct { MailboxID, Address string }
var sessions sync.Map
var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func main() {
 domain := getenv("DOMAIN", "localhost"); dbPath := getenv("DB_PATH", "./data/tempmail.db"); attach := getenv("ATTACHMENT_DIR", "./data/attachments")
 os.MkdirAll(filepath.Dir(dbPath), 0755); os.MkdirAll(attach, 0755)
 db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on"); if err != nil { log.Fatal(err) }
 app := &App{db: db, domain: domain, attach: attach, ttl: time.Duration(getenvInt("DEFAULT_TTL_HOURS", 1))*time.Hour, clients: map[string]map[*websocket.Conn]bool{}}
 if err := app.initDB(); err != nil { log.Fatal(err) }
 go app.cleanup()
 go app.runSMTP(getenv("SMTP_ADDR", ":25"))
 mux := http.NewServeMux(); mux.HandleFunc("/api/v1/mailboxes", app.mailboxes); mux.HandleFunc("/api/v1/mailboxes/preserve", app.preserve); mux.HandleFunc("/api/v1/mailboxes/restore", app.restore); mux.HandleFunc("/api/v1/mailboxes/", app.messages); mux.HandleFunc("/api/v1/messages/", app.message); mux.HandleFunc("/api/v1/attachments/", app.download); mux.HandleFunc("/ws/", app.ws)
 mux.Handle("/", http.FileServer(http.Dir("./web")))
 log.Printf("HTTP on %s, SMTP on %s for %s", getenv("HTTP_ADDR", ":8080"), getenv("SMTP_ADDR", ":25"), domain); log.Fatal(http.ListenAndServe(getenv("HTTP_ADDR", ":8080"), withCORS(mux)))
}
func getenv(k,d string) string { if v:=os.Getenv(k); v!="" { return v }; return d }; func getenvInt(k string,d int) int { v,_:=strconv.Atoi(getenv(k,strconv.Itoa(d))); return v }
func (a *App) initDB() error { _,e:=a.db.Exec(`CREATE TABLE IF NOT EXISTS mailboxes(id TEXT PRIMARY KEY,address TEXT UNIQUE NOT NULL,is_preserved INTEGER DEFAULT 0,password_hash TEXT,expires_at DATETIME,created_at DATETIME NOT NULL); CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,mailbox_id TEXT NOT NULL,sender_name TEXT,sender_address TEXT NOT NULL,subject TEXT,body_text TEXT,body_html TEXT,received_at DATETIME NOT NULL,FOREIGN KEY(mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE); CREATE TABLE IF NOT EXISTS attachments(id TEXT PRIMARY KEY,message_id TEXT NOT NULL,filename TEXT,mime_type TEXT,size INTEGER,path TEXT,FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE);`); return e }
func id() string { b:=make([]byte,16); rand.Read(b); return fmt.Sprintf("%x",b) }
func (a *App) newMailbox(custom string) (map[string]any,error) { if custom=="" { custom=id()[:10] }; address:=strings.ToLower(custom)+"@"+a.domain; mid:=id(); now:=time.Now(); _,e:=a.db.Exec(`INSERT INTO mailboxes(id,address,expires_at,created_at) VALUES(?,?,?,?)`,mid,address,now.Add(a.ttl),now); if e!=nil{return nil,e}; token:=id(); sessions.Store(token,session{mid,address}); return map[string]any{"id":mid,"address":address,"token":token,"expires_at":now.Add(a.ttl)},nil }
func jsonOut(w http.ResponseWriter,v any,code int){w.Header().Set("Content-Type","application/json");w.WriteHeader(code);json.NewEncoder(w).Encode(v)}
func body(r *http.Request) map[string]string { var v map[string]string; json.NewDecoder(r.Body).Decode(&v); return v }
func (a *App) auth(r *http.Request) (session,bool) { v,ok:=sessions.Load(strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer ")); if !ok{return session{},false}; return v.(session),true }
func (a *App) mailboxes(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return}; v,e:=a.newMailbox(body(r)["local_part"]);if e!=nil{jsonOut(w,map[string]string{"error":"address unavailable"},409);return};jsonOut(w,v,201)}
func (a *App) preserve(w http.ResponseWriter,r *http.Request){s,ok:=a.auth(r);if !ok{http.Error(w,"unauthorized",401);return}; p:=body(r)["password"];if len(p)<8{jsonOut(w,map[string]string{"error":"password must be at least 8 characters"},400);return};h,_:=bcrypt.GenerateFromPassword([]byte(p),bcrypt.DefaultCost);_,e:=a.db.Exec(`UPDATE mailboxes SET is_preserved=1,password_hash=?,expires_at=NULL WHERE id=?`,h,s.MailboxID);if e!=nil{http.Error(w,"failed",500);return};jsonOut(w,map[string]any{"ok":true,"address":s.Address},200)}
func (a *App) restore(w http.ResponseWriter,r *http.Request){v:=body(r);var s session;var hash string;e:=a.db.QueryRow(`SELECT id,address,password_hash FROM mailboxes WHERE address=? AND is_preserved=1`,v["address"]).Scan(&s.MailboxID,&s.Address,&hash);if e!=nil||bcrypt.CompareHashAndPassword([]byte(hash),[]byte(v["password"]))!=nil{http.Error(w,"invalid credentials",401);return};t:=id();sessions.Store(t,s);jsonOut(w,map[string]string{"token":t,"address":s.Address},200)}
func (a *App) messages(w http.ResponseWriter,r *http.Request){s,ok:=a.auth(r);if !ok{http.Error(w,"unauthorized",401);return};addr:=strings.TrimSuffix(strings.TrimPrefix(r.URL.Path,"/api/v1/mailboxes/"),"/messages");if addr!=s.Address{http.Error(w,"forbidden",403);return};rows,e:=a.db.Query(`SELECT id,sender_name,sender_address,subject,received_at FROM messages WHERE mailbox_id=? ORDER BY received_at DESC`,s.MailboxID);if e!=nil{http.Error(w,"failed",500);return};defer rows.Close();out:=[]any{};for rows.Next(){var id,n,from,sub,at string;rows.Scan(&id,&n,&from,&sub,&at);out=append(out,map[string]string{"id":id,"sender_name":n,"sender_address":from,"subject":sub,"received_at":at})};jsonOut(w,out,200)}
func (a *App) message(w http.ResponseWriter,r *http.Request){s,ok:=a.auth(r);if !ok{http.Error(w,"unauthorized",401);return};mid:=strings.TrimPrefix(r.URL.Path,"/api/v1/messages/");var mb,n,from,sub,text,html,at string;e:=a.db.QueryRow(`SELECT mailbox_id,sender_name,sender_address,subject,body_text,body_html,received_at FROM messages WHERE id=?`,mid).Scan(&mb,&n,&from,&sub,&text,&html,&at);if e!=nil||mb!=s.MailboxID{http.Error(w,"not found",404);return};jsonOut(w,map[string]any{"sender_name":n,"sender_address":from,"subject":sub,"body_text":text,"body_html":html,"received_at":at},200)}
func (a *App) download(w http.ResponseWriter,r *http.Request){s,ok:=a.auth(r);if !ok{http.Error(w,"unauthorized",401);return};var path,name string;e:=a.db.QueryRow(`SELECT a.path,a.filename FROM attachments a JOIN messages m ON m.id=a.message_id WHERE a.id=? AND m.mailbox_id=?`,strings.TrimPrefix(r.URL.Path,"/api/v1/attachments/"),s.MailboxID).Scan(&path,&name);if e!=nil{http.NotFound(w,r);return};w.Header().Set("Content-Disposition",`attachment; filename="`+name+`"`);http.ServeFile(w,r,path)}
func (a *App) ws(w http.ResponseWriter,r *http.Request){s,ok:=a.auth(r);if !ok {if v,found:=sessions.Load(r.URL.Query().Get("token"));found{s=v.(session);ok=true}};if !ok{http.Error(w,"unauthorized",401);return};c,e:=upgrader.Upgrade(w,r,nil);if e!=nil{return};a.mu.Lock();if a.clients[s.MailboxID]==nil{a.clients[s.MailboxID]=map[*websocket.Conn]bool{}};a.clients[s.MailboxID][c]=true;a.mu.Unlock();defer func(){a.mu.Lock();delete(a.clients[s.MailboxID],c);a.mu.Unlock();c.Close()}();for{if _,_,e=c.ReadMessage();e!=nil{return}}}
func (a *App) notify(mailbox string,v any){a.mu.Lock();defer a.mu.Unlock();for c:=range a.clients[mailbox]{if e:=c.WriteJSON(v);e!=nil{c.Close();delete(a.clients[mailbox],c)}}}
func (a *App) cleanup(){for{time.Sleep(5*time.Minute);rows,_:=a.db.Query(`SELECT id FROM mailboxes WHERE is_preserved=0 AND expires_at<?`,time.Now());var ids []string;for rows.Next(){var id string;rows.Scan(&id);ids=append(ids,id)};rows.Close();for _,id:=range ids{a.db.Exec(`DELETE FROM mailboxes WHERE id=?`,id)}}}
type backend struct{a *App};func (b *backend) NewSession(_ *smtp.Conn) (smtp.Session,error){return &mailSession{a:b.a},nil};type mailSession struct{a *App; from string; to string; data strings.Builder};func(m *mailSession) Mail(from string,_ *smtp.MailOptions)error{m.from=from;return nil};func(m *mailSession) Rcpt(to string,_ *smtp.RcptOptions)error{m.to=strings.ToLower(to);return nil};func(m *mailSession) Data(r io.Reader)error{d,_:=io.ReadAll(r);m.data.Write(d);return m.save()};func(m *mailSession) Reset(){};func(m *mailSession) Logout()error{return nil}
func(m *mailSession) save()error{var mb string;err:=m.a.db.QueryRow(`SELECT id FROM mailboxes WHERE lower(address)=?`,m.to).Scan(&mb);if err!=nil{return fmt.Errorf("unknown recipient")}; raw:=m.data.String(); subject:=raw; if i:=strings.Index(raw,"Subject:");i>=0{line:=raw[i+8:];if j:=strings.Index(line,"\n");j>=0{subject=strings.TrimSpace(line[:j])}};mid:=id();_,err=m.a.db.Exec(`INSERT INTO messages(id,mailbox_id,sender_address,subject,body_text,received_at) VALUES(?,?,?,?,?,?)`,mid,mb,m.from,subject,raw,time.Now());if err==nil{m.a.notify(mb,map[string]any{"type":"new_message","id":mid,"subject":subject})};return err}
func(a *App)runSMTP(addr string){s:=smtp.NewServer(&backend{a});s.Addr=addr;s.Domain=a.domain;s.AllowInsecureAuth=true;if e:=s.ListenAndServe();e!=nil{log.Printf("SMTP stopped: %v",e)}}
func withCORS(h http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("Access-Control-Allow-Origin","*");w.Header().Set("Access-Control-Allow-Headers","Authorization, Content-Type");if r.Method=="OPTIONS"{w.WriteHeader(204);return};h.ServeHTTP(w,r)})}
var _=mime.TypeByExtension;var _=template.HTMLEscapeString

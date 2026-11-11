package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type user struct {
	ID          int       `json:"id"`
	Username    string    `json:"username"`
	Hash        string    `json:"password_hash"`
	Role        string    `json:"role"`
	Email       string    `json:"email"`
	Nickname    string    `json:"nickname"`
	Avatar      string    `json:"avatar"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// public: so wie CasaOS den Benutzer an die Oberfläche gibt (ohne Passwort)
func (u user) public() map[string]any {
	return map[string]any{"id": u.ID, "username": u.Username, "role": u.Role, "email": u.Email,
		"nickname": u.Nickname, "avatar": u.Avatar, "description": u.Description, "created_at": u.CreatedAt}
}

type state struct {
	Secret  string `json:"secret"`
	Users   []user `json:"users"`
	NextID  int    `json:"next_id"`
	RegKeys map[string]time.Time `json:"-"`
}

type storeT struct {
	mu   sync.Mutex
	path string
	st   state
}

func loadStore(path string) *storeT {
	s := &storeT{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.st)
	}
	if s.st.Secret == "" {
		buf := make([]byte, 32)
		_, _ = rand.Read(buf)
		s.st.Secret = hex.EncodeToString(buf)
		s.save()
	}
	if s.st.NextID == 0 {
		s.st.NextID = 1
	}
	s.st.RegKeys = map[string]time.Time{}
	return s
}

// save: atomar schreiben (Datei nur für root lesbar, enthält Passwort-Hashes und das Token-Geheimnis)
func (s *storeT) save() {
	b, _ := json.MarshalIndent(s.st, "", "  ")
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

func (s *storeT) userByName(name string) *user {
	for i := range s.st.Users {
		if strings.EqualFold(s.st.Users[i].Username, name) {
			return &s.st.Users[i]
		}
	}
	return nil
}

func (s *storeT) userByID(id int) *user {
	for i := range s.st.Users {
		if s.st.Users[i].ID == id {
			return &s.st.Users[i]
		}
	}
	return nil
}

// ── Tokens: HMAC-SHA256, Format base64(json).base64(sig) ─────────────────────
type claims struct {
	UID  int    `json:"uid"`
	Kind string `json:"kind"` // access | refresh
	Exp  int64  `json:"exp"`
}

func (s *storeT) sign(c claims) string {
	b, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, []byte(s.st.Secret))
	m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *storeT) verify(tok, kind string) (*user, error) {
	tok = strings.TrimPrefix(strings.TrimSpace(tok), "Bearer ")
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return nil, errors.New("ungültig")
	}
	m := hmac.New(sha256.New, []byte(s.st.Secret))
	m.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return nil, errors.New("Signatur falsch")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var c claims
	if json.Unmarshal(b, &c) != nil || c.Kind != kind || time.Now().Unix() > c.Exp {
		return nil, errors.New("abgelaufen")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.userByID(c.UID)
	if u == nil {
		return nil, errors.New("Benutzer weg")
	}
	cp := *u
	return &cp, nil
}

func (s *storeT) tokens(u *user) map[string]any {
	exp := time.Now().Add(3 * time.Hour)
	return map[string]any{
		"access_token":  s.sign(claims{u.ID, "access", exp.Unix()}),
		"refresh_token": s.sign(claims{u.ID, "refresh", time.Now().Add(14 * 24 * time.Hour).Unix()}),
		"expires_at":    exp.Unix(),
	}
}

// auth: Handler nur mit gültigem Access-Token (Header "Authorization" ohne/mit Bearer, oder ?token=)
func (s *storeT) auth(h func(http.ResponseWriter, *http.Request, *user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("Authorization")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		u, err := s.verify(tok, "access")
		if err != nil {
			fail(w, http.StatusUnauthorized, 401, "nicht angemeldet")
			return
		}
		h(w, r, u)
	}
}

// ── Anmelde-Bremse gegen Durchprobieren ──────────────────────────────────────
var loginFails = struct {
	sync.Mutex
	m map[string][]time.Time
}{m: map[string][]time.Time{}}

func tooManyFails(ip string) bool {
	loginFails.Lock()
	defer loginFails.Unlock()
	var keep []time.Time
	for _, t := range loginFails.m[ip] {
		if time.Since(t) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	loginFails.m[ip] = keep
	return len(keep) >= 8
}

func noteFail(ip string) {
	loginFails.Lock()
	loginFails.m[ip] = append(loginFails.m[ip], time.Now())
	loginFails.Unlock()
}

func clientIP(r *http.Request) string {
	h := r.RemoteAddr
	if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	return h
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func registerUsers(mux *http.ServeMux, s *storeT) {
	customDir := filepath.Join(*dataDir, "custom")
	_ = os.MkdirAll(customDir, 0o700)

	mux.HandleFunc("GET /v1/users/status", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.st.Users) > 0 {
			ok(w, map[string]any{"initialized": true, "key": "", "gpus": 0})
			return
		}
		buf := make([]byte, 16)
		_, _ = rand.Read(buf)
		k := hex.EncodeToString(buf)
		s.st.RegKeys[k] = time.Now()
		ok(w, map[string]any{"initialized": false, "key": k, "gpus": 0})
	})

	// Erster Benutzer (Willkommens-Seite). Nur solange es noch keinen gibt und mit gültigem Schlüssel.
	mux.HandleFunc("POST /v1/users/register", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if readJSON(r, &in) != nil {
			fail(w, 400, 400, "ungültige Anfrage")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.st.Users) > 0 {
			fail(w, 400, 10006, "Benutzer existiert bereits")
			return
		}
		if _, okk := s.st.RegKeys[in["key"]]; !okk {
			fail(w, 400, 10003, "Schlüssel ungültig")
			return
		}
		if len(in["username"]) < 1 || len(in["password"]) < 5 {
			fail(w, 400, 400, "Benutzername/Passwort zu kurz")
			return
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(in["password"]), bcrypt.DefaultCost)
		u := user{ID: s.st.NextID, Username: in["username"], Hash: string(h), Role: "admin", CreatedAt: time.Now()}
		s.st.NextID++
		s.st.Users = append(s.st.Users, u)
		delete(s.st.RegKeys, in["key"])
		s.save()
		ok(w, u.public())
	})

	mux.HandleFunc("POST /v1/users/login", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if tooManyFails(ip) {
			fail(w, http.StatusTooManyRequests, 10014, "Zu viele Fehlversuche, bitte 10 Minuten warten")
			return
		}
		var in map[string]string
		if readJSON(r, &in) != nil {
			fail(w, 400, 400, "ungültige Anfrage")
			return
		}
		s.mu.Lock()
		u := s.userByName(in["username"])
		var cp user
		if u != nil {
			cp = *u
		}
		s.mu.Unlock()
		if u == nil || bcrypt.CompareHashAndPassword([]byte(cp.Hash), []byte(in["password"])) != nil {
			noteFail(ip)
			fail(w, 400, 10005, "Benutzer oder Passwort falsch")
			return
		}
		ok(w, map[string]any{"token": s.tokens(&cp), "user": cp.public()})
	})

	mux.HandleFunc("POST /v1/users/refresh", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = readJSON(r, &in)
		u, err := s.verify(in["refresh_token"], "refresh")
		if err != nil {
			fail(w, http.StatusUnauthorized, 10012, "Sitzung abgelaufen")
			return
		}
		ok(w, s.tokens(u))
	})

	mux.HandleFunc("GET /v1/users/name", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		names := []string{}
		for _, u := range s.st.Users {
			names = append(names, u.Username)
		}
		ok(w, names)
	})

	mux.HandleFunc("GET /v1/users/current", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		ok(w, u.public())
	}))

	mux.HandleFunc("PUT /v1/users/current", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var in map[string]string
		_ = readJSON(r, &in)
		s.mu.Lock()
		defer s.mu.Unlock()
		p := s.userByID(u.ID)
		if v, okk := in["username"]; okk && v != "" {
			if o := s.userByName(v); o != nil && o.ID != u.ID {
				fail(w, 400, 10006, "Name vergeben")
				return
			}
			p.Username = v
		}
		for k, dst := range map[string]*string{"nickname": &p.Nickname, "email": &p.Email, "avatar": &p.Avatar, "description": &p.Description} {
			if v, okk := in[k]; okk {
				*dst = v
			}
		}
		s.save()
		ok(w, p.public())
	}))

	mux.HandleFunc("PUT /v1/users/current/password", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var in map[string]string
		_ = readJSON(r, &in)
		s.mu.Lock()
		defer s.mu.Unlock()
		p := s.userByID(u.ID)
		if bcrypt.CompareHashAndPassword([]byte(p.Hash), []byte(in["old_password"])) != nil {
			fail(w, 400, 10005, "Altes Passwort falsch")
			return
		}
		if len(in["password"]) < 5 {
			fail(w, 400, 400, "Passwort zu kurz")
			return
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(in["password"]), bcrypt.DefaultCost)
		p.Hash = string(h)
		s.save()
		ok(w, p.public())
	}))

	// Freier Schlüssel/Wert-Speicher der Oberfläche (Hintergrund, Widgets, Sprache, Link-Apps …)
	mux.HandleFunc("GET /v1/users/current/custom/{key}", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		k := r.PathValue("key")
		if !keyRe.MatchString(k) {
			fail(w, 400, 400, "ungültiger Schlüssel")
			return
		}
		b, err := os.ReadFile(filepath.Join(customDir, k+".json"))
		if err != nil || len(b) == 0 {
			ok(w, "") // wie CasaOS: leerer Text → Oberfläche legt Standardwerte an
			return
		}
		if json.Valid(b) {
			ok(w, json.RawMessage(b))
		} else {
			ok(w, string(b))
		}
	}))

	mux.HandleFunc("POST /v1/users/current/custom/{key}", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		k := r.PathValue("key")
		if !keyRe.MatchString(k) {
			fail(w, 400, 400, "ungültiger Schlüssel")
			return
		}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err := os.WriteFile(filepath.Join(customDir, k+".json"), b, 0o600); err != nil {
			fail(w, 500, 500, err.Error())
			return
		}
		if json.Valid(b) {
			ok(w, json.RawMessage(b))
		} else {
			ok(w, string(b))
		}
	}))
}

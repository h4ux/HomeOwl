// Package auth manages HomeOwl user accounts, password hashing, login sessions and API tokens.
package auth

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
	pbkdfIter  = 600_000
	SessionTTL = 30 * 24 * time.Hour
)

type User struct {
	Username  string `json:"username"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Hash      string `json:"hash"`
	Tailscale string `json:"tailscale,omitempty"` // tailnet login that signs this user in automatically
	Created   int64  `json:"created"`
	LastLogin int64  `json:"last_login,omitempty"`
}

// Public is a user without the password hash.
type Public struct {
	Username  string `json:"username"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Tailscale string `json:"tailscale,omitempty"`
	Created   int64  `json:"created"`
	LastLogin int64  `json:"last_login,omitempty"`
}

func (u User) Public() Public {
	return Public{u.Username, u.Name, u.Role, u.Tailscale, u.Created, u.LastLogin}
}

type session struct {
	User    string `json:"user"`
	CSRF    string `json:"csrf"`
	Expires int64  `json:"expires"`
	Via     string `json:"via"` // password | tailscale
}

type Store struct {
	mu        sync.Mutex
	dir       string
	users     map[string]*User
	sessions  map[string]*session // key = sha256(token)
	attempts  map[string][]time.Time
	SetupCode string // one-time code needed to create the first admin from another device
}

var userRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)

func Open(dataDir string) (*Store, error) {
	s := &Store{dir: dataDir, users: map[string]*User{}, sessions: map[string]*session{}, attempts: map[string][]time.Time{}}
	if b, err := os.ReadFile(s.usersPath()); err == nil {
		var list []*User
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, fmt.Errorf("users.json: %w", err)
		}
		for _, u := range list {
			s.users[u.Username] = u
		}
	}
	if b, err := os.ReadFile(s.sessionsPath()); err == nil {
		_ = json.Unmarshal(b, &s.sessions)
	}
	if len(s.users) == 0 {
		// the code survives restarts so `homeowl setup-code` can show it
		if b, err := os.ReadFile(s.codePath()); err == nil && len(strings.TrimSpace(string(b))) == 9 {
			s.SetupCode = strings.TrimSpace(string(b))
		} else {
			s.SetupCode = randomCode()
			_ = config.WriteFileAtomic(s.codePath(), []byte(s.SetupCode+"\n"), 0o600)
		}
	}
	return s, nil
}

func (s *Store) usersPath() string    { return filepath.Join(s.dir, "users.json") }
func (s *Store) codePath() string     { return filepath.Join(s.dir, "setup-code") }
func (s *Store) sessionsPath() string { return filepath.Join(s.dir, "sessions.json") }

func (s *Store) saveUsers() error {
	list := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Username < list[j].Username })
	b, _ := json.MarshalIndent(list, "", "  ")
	return config.WriteFileAtomic(s.usersPath(), b, 0o600)
}

func (s *Store) saveSessions() {
	now := time.Now().Unix()
	for k, v := range s.sessions {
		if v.Expires < now {
			delete(s.sessions, k)
		}
	}
	b, _ := json.Marshal(s.sessions)
	_ = config.WriteFileAtomic(s.sessionsPath(), b, 0o600)
}

func (s *Store) NeedsSetup() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users) == 0
}

func (s *Store) Users() []Public {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Public, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u.Public())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func (s *Store) Get(username string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[strings.ToLower(username)]
	if !ok {
		return User{}, false
	}
	return *u, true
}

// ByTailscale finds the user linked to a tailnet login.
func (s *Store) ByTailscale(login string) (User, bool) {
	login = strings.ToLower(strings.TrimSpace(login))
	if login == "" {
		return User{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Tailscale, login) {
			return *u, true
		}
	}
	return User{}, false
}

type UserInput struct {
	Username  string `json:"username"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Password  string `json:"password"`
	Tailscale string `json:"tailscale"`
}

func CheckPassword(p string) error {
	if len(p) < 10 {
		return errors.New("use a password of at least 10 characters")
	}
	if len(p) > 200 {
		return errors.New("password too long")
	}
	return nil
}

// Save creates or updates a user. An empty password keeps the current one (for updates).
func (s *Store) Save(in UserInput, create bool) error {
	un := strings.ToLower(strings.TrimSpace(in.Username))
	if !userRE.MatchString(un) {
		return errors.New("username: 2–32 characters, letters, numbers, dot, dash or underscore")
	}
	if in.Role != RoleAdmin && in.Role != RoleViewer {
		return errors.New("role must be admin or viewer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, exists := s.users[un]
	if create && exists {
		return errors.New("that username is taken")
	}
	if !create && !exists {
		return errors.New("no such user")
	}
	if !exists {
		if err := CheckPassword(in.Password); err != nil {
			return err
		}
		u = &User{Username: un, Created: time.Now().Unix()}
	}
	if exists && u.Role == RoleAdmin && in.Role != RoleAdmin && s.adminCount() == 1 {
		return errors.New("keep at least one admin")
	}
	if in.Password != "" {
		if err := CheckPassword(in.Password); err != nil {
			return err
		}
		u.Hash = HashPassword(in.Password)
		s.dropSessions(un)
	}
	u.Name = strings.TrimSpace(in.Name)
	if len(u.Name) > 60 {
		u.Name = u.Name[:60]
	}
	u.Role = in.Role
	ts := strings.ToLower(strings.TrimSpace(in.Tailscale))
	if ts != "" {
		for _, o := range s.users {
			if o.Username != un && strings.EqualFold(o.Tailscale, ts) {
				return fmt.Errorf("%s is already linked to %s", ts, o.Username)
			}
		}
	}
	u.Tailscale = ts
	s.users[un] = u
	if len(s.users) > 0 && s.SetupCode != "" {
		s.SetupCode = ""
		_ = os.Remove(s.codePath())
	}
	return s.saveUsers()
}

func (s *Store) adminCount() int {
	n := 0
	for _, u := range s.users {
		if u.Role == RoleAdmin {
			n++
		}
	}
	return n
}

func (s *Store) Delete(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[username]
	if !ok {
		return errors.New("no such user")
	}
	if u.Role == RoleAdmin && s.adminCount() == 1 {
		return errors.New("keep at least one admin")
	}
	delete(s.users, username)
	s.dropSessions(username)
	return s.saveUsers()
}

// SetPassword is used by the `homeowl reset-password` command.
func (s *Store) SetPassword(username, password string) error {
	if err := CheckPassword(password); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[strings.ToLower(username)]
	if !ok {
		return errors.New("no such user")
	}
	u.Hash = HashPassword(password)
	s.dropSessions(u.Username)
	return s.saveUsers()
}

func (s *Store) dropSessions(username string) {
	for k, v := range s.sessions {
		if v.User == username {
			delete(s.sessions, k)
		}
	}
	s.saveSessions()
}

// ---- passwords

func HashPassword(p string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	k, _ := pbkdf2.Key(sha256.New, p, salt, pbkdfIter, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdfIter, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(k))
}

func VerifyPassword(hash, p string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, _ := pbkdf2.Key(sha256.New, p, salt, iter, len(want))
	return hmac.Equal(got, want)
}

var dummyHash = HashPassword("not-a-real-password-just-timing")

// ---- login & sessions

// Allow applies a simple per-client limit: 8 failed logins per 15 minutes.
func (s *Store) allow(client string) bool {
	cut := time.Now().Add(-15 * time.Minute)
	var keep []time.Time
	for _, t := range s.attempts[client] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	s.attempts[client] = keep
	return len(keep) < 8
}

var ErrRateLimited = errors.New("too many failed attempts — wait 15 minutes and try again")
var ErrBadLogin = errors.New("wrong username or password")

// Login checks a password and returns a new session token and its CSRF token.
func (s *Store) Login(username, password, client string) (token, csrf string, u User, err error) {
	un := strings.ToLower(strings.TrimSpace(username))
	s.mu.Lock()
	if !s.allow(client) {
		s.mu.Unlock()
		return "", "", User{}, ErrRateLimited
	}
	usr, ok := s.users[un]
	hash := dummyHash
	if ok {
		hash = usr.Hash
	}
	s.mu.Unlock()
	good := VerifyPassword(hash, password) && ok // always spend the hashing time
	s.mu.Lock()
	defer s.mu.Unlock()
	if !good {
		s.attempts[client] = append(s.attempts[client], time.Now())
		return "", "", User{}, ErrBadLogin
	}
	delete(s.attempts, client)
	usr.LastLogin = time.Now().Unix()
	_ = s.saveUsers()
	token, csrf = s.newSession(usr.Username, "password")
	return token, csrf, *usr, nil
}

// SessionFor creates a session without a password (tailnet identity already proven by Tailscale).
func (s *Store) SessionFor(username, via string) (token, csrf string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[username]; ok {
		u.LastLogin = time.Now().Unix()
		_ = s.saveUsers()
	}
	return s.newSession(username, via)
}

func (s *Store) newSession(username, via string) (string, string) {
	token, csrf := randomToken(), randomToken()
	s.sessions[tokenKey(token)] = &session{User: username, CSRF: csrf, Expires: time.Now().Add(SessionTTL).Unix(), Via: via}
	s.saveSessions()
	return token, csrf
}

// Check validates a session token; it slides the expiry forward once a day.
func (s *Store) Check(token string) (User, string, bool) {
	if token == "" {
		return User{}, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := tokenKey(token)
	ss, ok := s.sessions[k]
	if !ok || ss.Expires < time.Now().Unix() {
		return User{}, "", false
	}
	u, ok := s.users[ss.User]
	if !ok {
		delete(s.sessions, k)
		return User{}, "", false
	}
	if ss.Expires-time.Now().Unix() < int64((SessionTTL - 24*time.Hour).Seconds()) {
		ss.Expires = time.Now().Add(SessionTTL).Unix()
		s.saveSessions()
	}
	return *u, ss.CSRF, true
}

func (s *Store) Logout(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, tokenKey(token))
	s.saveSessions()
}

// CheckSetupCode compares the one-time setup code in constant time.
func (s *Store) CheckSetupCode(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	want := strings.ReplaceAll(s.SetupCode, "-", "")
	return want != "" && subtle.ConstantTimeCompare([]byte(c), []byte(want)) == 1
}

func tokenKey(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	out := make([]byte, 0, 9)
	for i, x := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(x)%len(alphabet)])
	}
	return string(out)
}

// ---- API tokens (MCP)

// NewToken returns a token to show once, and the hash to store.
func NewToken() (token, hash string) {
	token = "hwl_" + randomToken()
	return token, TokenHash(token)
}

func TokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

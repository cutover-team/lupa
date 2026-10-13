// Package store — пользователи, сайты, цели и сессии входа в SQLite.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
	mu sync.RWMutex
	// кэш сайтов для быстрого ответа счётчику
	sites map[uint32]Site
}

type User struct {
	ID    int64
	Email string
}

type Site struct {
	ID        uint32
	Name      string
	Domain    string
	CreatedAt time.Time
	Replay    bool // вебвизор включён
	ReplayPct int  // доля записываемых визитов, %
}

type Goal struct {
	ID     int64
	SiteID uint32
	Name   string
	Event  string // имя события или путь, начинающийся с /
}

var ErrNotFound = errors.New("not found")

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, sites: map[uint32]Site{}}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	if err := s.reloadSites(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, email TEXT UNIQUE NOT NULL, pass_hash TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS login_sessions (token TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sites (id INTEGER PRIMARY KEY, name TEXT NOT NULL, domain TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS goals (id INTEGER PRIMARY KEY, site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE, name TEXT NOT NULL, event TEXT NOT NULL);
`)
	if err != nil {
		return err
	}
	// Поля, добавленные позже: ошибку «колонка уже есть» игнорируем.
	for _, q := range []string{
		"ALTER TABLE sites ADD COLUMN replay INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE sites ADD COLUMN replay_pct INTEGER NOT NULL DEFAULT 10",
	} {
		if _, err := s.db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---- пользователи

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(password) < 8 {
		return User{}, errors.New("email и пароль от 8 символов обязательны")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO users(email, pass_hash, created_at) VALUES(?,?,?)", email, string(hash), time.Now().Unix())
	if err != nil {
		return User{}, fmt.Errorf("пользователь с такой почтой уже есть")
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Email: email}, nil
}

func (s *Store) Authenticate(ctx context.Context, email, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx, "SELECT id, email, pass_hash FROM users WHERE email=?", email).Scan(&u.ID, &u.Email, &hash)
	if err != nil {
		return User{}, ErrNotFound
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

// UserByEmail — поиск пользователя без проверки пароля (для автоматического входа в демо).
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, "SELECT id, email FROM users WHERE email=?", strings.ToLower(strings.TrimSpace(email))).Scan(&u.ID, &u.Email)
	if err != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (s *Store) CreateLoginSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	_, err := s.db.ExecContext(ctx, "INSERT INTO login_sessions(token, user_id, expires_at) VALUES(?,?,?)", token, userID, time.Now().Add(ttl).Unix())
	return token, err
}

func (s *Store) UserByToken(ctx context.Context, token string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.email FROM login_sessions ls JOIN users u ON u.id=ls.user_id WHERE ls.token=? AND ls.expires_at>?`, token, time.Now().Unix()).Scan(&u.ID, &u.Email)
	if err != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (s *Store) DeleteLoginSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM login_sessions WHERE token=?", token)
	return err
}

// ---- сайты

func (s *Store) reloadSites() error {
	rows, err := s.db.Query("SELECT id, name, domain, created_at, replay, replay_pct FROM sites ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	m := map[uint32]Site{}
	for rows.Next() {
		var st Site
		var created int64
		var replay int
		if err := rows.Scan(&st.ID, &st.Name, &st.Domain, &created, &replay, &st.ReplayPct); err != nil {
			return err
		}
		st.Replay = replay == 1
		st.CreatedAt = time.Unix(created, 0)
		m[st.ID] = st
	}
	s.mu.Lock()
	s.sites = m
	s.mu.Unlock()
	return rows.Err()
}

func (s *Store) Sites() []Site {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Site, 0, len(s.sites))
	for _, st := range s.sites {
		out = append(out, st)
	}
	// стабильный порядок по id
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (s *Store) Site(id uint32) (Site, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.sites[id]
	return st, ok
}

// SetReplay включает или выключает вебвизор и долю записываемых визитов.
func (s *Store) SetReplay(ctx context.Context, id uint32, on bool, pct int) error {
	if pct < 1 {
		pct = 1
	}
	if pct > 100 {
		pct = 100
	}
	v := 0
	if on {
		v = 1
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE sites SET replay=?, replay_pct=? WHERE id=?", v, pct, id); err != nil {
		return err
	}
	return s.reloadSites()
}

// Lookup — для счётчика: домен сайта по id.
func (s *Store) Lookup(id uint32) (string, bool) {
	st, ok := s.Site(id)
	return st.Domain, ok
}

func (s *Store) CreateSite(ctx context.Context, name, domain string) (Site, error) {
	domain = normalizeDomain(domain)
	name = strings.TrimSpace(name)
	if domain == "" {
		return Site{}, errors.New("укажите домен сайта")
	}
	if name == "" {
		name = domain
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO sites(name, domain, created_at) VALUES(?,?,?)", name, domain, time.Now().Unix())
	if err != nil {
		return Site{}, err
	}
	id, _ := res.LastInsertId()
	if err := s.reloadSites(); err != nil {
		return Site{}, err
	}
	st, _ := s.Site(uint32(id))
	return st, nil
}

func (s *Store) DeleteSite(ctx context.Context, id uint32) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sites WHERE id=?", id); err != nil {
		return err
	}
	return s.reloadSites()
}

func normalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimPrefix(d, "www.")
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	return d
}

// ---- цели

func (s *Store) Goals(ctx context.Context, siteID uint32) ([]Goal, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, site_id, name, event FROM goals WHERE site_id=? ORDER BY id", siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Goal
	for rows.Next() {
		var g Goal
		if err := rows.Scan(&g.ID, &g.SiteID, &g.Name, &g.Event); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) CreateGoal(ctx context.Context, siteID uint32, name, event string) error {
	name, event = strings.TrimSpace(name), strings.TrimSpace(event)
	if event == "" {
		return errors.New("укажите событие или путь")
	}
	if name == "" {
		name = event
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO goals(site_id, name, event) VALUES(?,?,?)", siteID, name, event)
	return err
}

func (s *Store) DeleteGoal(ctx context.Context, siteID uint32, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM goals WHERE id=? AND site_id=?", id, siteID)
	return err
}

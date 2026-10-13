// Package ingest принимает события от счётчика и складывает их в ClickHouse.
package ingest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lupa/internal/ch"
)

// SiteLookup проверяет, что сайт с таким id существует, и возвращает его домен.
type SiteLookup func(id uint32) (domain string, ok bool)

type Handler struct {
	batch         *ch.Batcher
	clicks        *ch.Batcher
	replays       *ch.Batcher
	sessions      *Sessions
	secret        string
	trustProxy    bool
	lookup        SiteLookup
	replayAllowed ReplayAllowed
}

func NewHandler(events, clicks, replays *ch.Batcher, s *Sessions, secret string, trustProxy bool, lookup SiteLookup, replayAllowed ReplayAllowed) *Handler {
	return &Handler{batch: events, clicks: clicks, replays: replays, sessions: s, secret: secret, trustProxy: trustProxy, lookup: lookup, replayAllowed: replayAllowed}
}

// Secret — соль сервера, нужна для подписи токенов карты кликов.
func (h *Handler) Secret() string { return h.secret }

// payload — то, что шлёт lupa.js. Короткие ключи, чтобы пакет был маленьким.
type payload struct {
	Site   uint32         `json:"s"`
	Name   string         `json:"n"`
	URL    string         `json:"u"`
	Ref    string         `json:"r"`
	Title  string         `json:"t"`
	W      int            `json:"w"`
	H      int            `json:"h"`
	Lang   string         `json:"l"`
	Dur    int            `json:"d"`
	Scroll int            `json:"sc"`
	Props  map[string]any `json:"p"`
}

// row — строка таблицы events.
type row struct {
	SiteID      uint32 `json:"site_id"`
	TS          string `json:"ts"`
	Name        string `json:"name"`
	VisitorID   uint64 `json:"visitor_id"`
	SessionID   uint64 `json:"session_id"`
	URL         string `json:"url"`
	Path        string `json:"path"`
	Title       string `json:"title"`
	Referrer    string `json:"referrer"`
	RefDomain   string `json:"ref_domain"`
	RefSource   string `json:"ref_source"`
	UTMSource   string `json:"utm_source"`
	UTMMedium   string `json:"utm_medium"`
	UTMCampaign string `json:"utm_campaign"`
	UTMContent  string `json:"utm_content"`
	UTMTerm     string `json:"utm_term"`
	Device      string `json:"device"`
	Browser     string `json:"browser"`
	OS          string `json:"os"`
	ScreenW     int    `json:"screen_w"`
	ScreenH     int    `json:"screen_h"`
	Lang        string `json:"lang"`
	Country     string `json:"country"`
	DurationMS  int    `json:"duration_ms"`
	ScrollPct   int    `json:"scroll_pct"`
	Props       string `json:"props"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	var p payload
	if err := json.Unmarshal(body, &p); err != nil || p.Site == 0 || p.Name == "" || p.URL == "" {
		http.Error(w, "payload", http.StatusBadRequest)
		return
	}
	ua := r.UserAgent()
	if IsBot(ua) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	domain, ok := h.lookup(p.Site)
	if !ok {
		http.Error(w, "unknown site", http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	ip := clientIP(r, h.trustProxy)
	visitor := h.visitorID(p.Site, ip, ua, now)
	session, _ := h.sessions.Touch(visitor, now)

	u, _ := url.Parse(p.URL)
	path, q := "/", url.Values{}
	if u != nil {
		path = u.Path
		if path == "" {
			path = "/"
		}
		q = u.Query()
	}
	browser, osName, device := ParseUA(ua)
	refDomain := RefDomain(p.Ref, domain)
	props := ""
	if len(p.Props) > 0 {
		if b, err := json.Marshal(p.Props); err == nil && len(b) <= 4096 {
			props = string(b)
		}
	}
	name := strings.TrimSpace(p.Name)
	if len(name) > 64 {
		name = name[:64]
	}
	rw := row{
		SiteID: p.Site, TS: now.Format("2006-01-02 15:04:05"), Name: name,
		VisitorID: visitor, SessionID: session,
		URL: cut(p.URL, 2048), Path: cut(path, 512), Title: cut(p.Title, 256),
		Referrer: cut(p.Ref, 1024), RefDomain: refDomain, RefSource: Source(refDomain),
		UTMSource: cut(q.Get("utm_source"), 128), UTMMedium: cut(q.Get("utm_medium"), 128),
		UTMCampaign: cut(q.Get("utm_campaign"), 128), UTMContent: cut(q.Get("utm_content"), 128), UTMTerm: cut(q.Get("utm_term"), 128),
		Device: device, Browser: browser, OS: osName,
		ScreenW: clamp(p.W, 0, 65535), ScreenH: clamp(p.H, 0, 65535), Lang: cut(p.Lang, 16),
		DurationMS: clamp(p.Dur, 0, 86400000), ScrollPct: clamp(p.Scroll, 0, 100), Props: props,
	}
	b, _ := json.Marshal(rw)
	h.batch.Add(b)
	w.WriteHeader(http.StatusAccepted)
}

// visitorID — хеш от секрета, даты, сайта, IP и User-Agent. Меняется каждый день,
// поэтому посетителя нельзя отследить дольше суток, а cookie не нужны.
func (h *Handler) visitorID(site uint32, ip, ua string, now time.Time) uint64 {
	sum := sha256.Sum256([]byte(h.secret + "|" + now.Format("2006-01-02") + "|" + itoa(site) + "|" + ip + "|" + ua))
	return binary.LittleEndian.Uint64(sum[:8])
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i > 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
		if rip := r.Header.Get("X-Real-IP"); rip != "" {
			return rip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func itoa(v uint32) string {
	var b [10]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
		if v == 0 {
			break
		}
	}
	return string(b[i:])
}

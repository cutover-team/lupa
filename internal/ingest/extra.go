package ingest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// identity — общие вычисления для всех входящих запросов счётчика.
type identity struct {
	site    uint32
	domain  string
	ip      string
	ua      string
	now     time.Time
	visitor uint64
	session uint64
}

func (h *Handler) identify(w http.ResponseWriter, r *http.Request, site uint32) (identity, bool) {
	ua := r.UserAgent()
	if IsBot(ua) {
		w.WriteHeader(http.StatusAccepted)
		return identity{}, false
	}
	domain, ok := h.lookup(site)
	if !ok {
		http.Error(w, "unknown site", http.StatusNotFound)
		return identity{}, false
	}
	now := time.Now().UTC()
	ip := clientIP(r, h.trustProxy)
	visitor := h.visitorID(site, ip, ua, now)
	session, _ := h.sessions.Touch(visitor, now)
	return identity{site: site, domain: domain, ip: ip, ua: ua, now: now, visitor: visitor, session: session}, true
}

func cors(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	return true
}

// ---- клики для карты кликов

type clickPayload struct {
	Site     uint32  `json:"s"`
	URL      string  `json:"u"`
	X        float64 `json:"x"`  // доля ширины документа, 0..1
	Y        int     `json:"y"`  // пиксели от верха документа
	VW       int     `json:"vw"` // ширина окна
	VH       int     `json:"vh"`
	Selector string  `json:"sel"`
	Text     string  `json:"txt"`
}

type clickRow struct {
	SiteID    uint32  `json:"site_id"`
	TS        string  `json:"ts"`
	VisitorID uint64  `json:"visitor_id"`
	SessionID uint64  `json:"session_id"`
	Path      string  `json:"path"`
	XPct      float64 `json:"x_pct"`
	Y         int     `json:"y"`
	VW        int     `json:"vw"`
	VH        int     `json:"vh"`
	Selector  string  `json:"selector"`
	Text      string  `json:"text"`
}

// ServeClick принимает один клик или массив кликов.
func (h *Handler) ServeClick(w http.ResponseWriter, r *http.Request) {
	if !cors(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	var list []clickPayload
	if len(body) > 0 && body[0] == '[' {
		if err := json.Unmarshal(body, &list); err != nil {
			http.Error(w, "payload", http.StatusBadRequest)
			return
		}
	} else {
		var one clickPayload
		if err := json.Unmarshal(body, &one); err != nil {
			http.Error(w, "payload", http.StatusBadRequest)
			return
		}
		list = []clickPayload{one}
	}
	if len(list) == 0 || list[0].Site == 0 {
		http.Error(w, "payload", http.StatusBadRequest)
		return
	}
	id, ok := h.identify(w, r, list[0].Site)
	if !ok {
		return
	}
	for i, c := range list {
		if i >= 50 || c.Site != id.site {
			break
		}
		u, _ := url.Parse(c.URL)
		path := "/"
		if u != nil && u.Path != "" {
			path = u.Path
		}
		if c.X < 0 {
			c.X = 0
		}
		if c.X > 1 {
			c.X = 1
		}
		row := clickRow{SiteID: id.site, TS: id.now.Format("2006-01-02 15:04:05"), VisitorID: id.visitor, SessionID: id.session,
			Path: cut(path, 512), XPct: c.X, Y: clamp(c.Y, 0, 200000), VW: clamp(c.VW, 0, 65535), VH: clamp(c.VH, 0, 65535),
			Selector: cut(c.Selector, 256), Text: cut(c.Text, 80)}
		b, _ := json.Marshal(row)
		h.clicks.Add(b)
	}
	w.WriteHeader(http.StatusAccepted)
}

// ---- вебвизор: чанки событий rrweb

type replayPayload struct {
	Site    uint32          `json:"s"`
	RID     string          `json:"rid"`
	Seq     uint32          `json:"seq"`
	URL     string          `json:"u"`
	Ref     string          `json:"r"`
	FirstMS uint64          `json:"f"`
	LastMS  uint64          `json:"l"`
	Events  json.RawMessage `json:"e"`
}

type replayRow struct {
	SiteID    uint32 `json:"site_id"`
	RID       string `json:"rid"`
	VisitorID uint64 `json:"visitor_id"`
	TS        string `json:"ts"`
	Seq       uint32 `json:"seq"`
	Path      string `json:"path"`
	FirstMS   uint64 `json:"first_ms"`
	LastMS    uint64 `json:"last_ms"`
	Device    string `json:"device"`
	Browser   string `json:"browser"`
	OS        string `json:"os"`
	RefSource string `json:"ref_source"`
	Bytes     uint32 `json:"bytes"`
	Data      string `json:"data"`
}

// ReplayAllowed — включён ли вебвизор для сайта (настройка в панели).
type ReplayAllowed func(site uint32) bool

// ServeReplay принимает чанк записи. Лимит 2 МБ на чанк.
func (h *Handler) ServeReplay(w http.ResponseWriter, r *http.Request) {
	if !cors(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	var p replayPayload
	if err := json.Unmarshal(body, &p); err != nil || p.Site == 0 || len(p.RID) < 8 || len(p.RID) > 64 || len(p.Events) < 2 {
		http.Error(w, "payload", http.StatusBadRequest)
		return
	}
	if h.replayAllowed != nil && !h.replayAllowed(p.Site) {
		w.WriteHeader(http.StatusAccepted) // молча игнорируем: вебвизор выключен
		return
	}
	id, ok := h.identify(w, r, p.Site)
	if !ok {
		return
	}
	u, _ := url.Parse(p.URL)
	path := "/"
	if u != nil && u.Path != "" {
		path = u.Path
	}
	browser, osName, device := ParseUA(id.ua)
	rid := strings.Map(func(c rune) rune {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			return c
		}
		return -1
	}, p.RID)
	row := replayRow{SiteID: id.site, RID: rid, VisitorID: id.visitor, TS: id.now.Format("2006-01-02 15:04:05"), Seq: p.Seq,
		Path: cut(path, 512), FirstMS: p.FirstMS, LastMS: p.LastMS, Device: device, Browser: browser, OS: osName,
		RefSource: Source(RefDomain(p.Ref, id.domain)), Bytes: uint32(len(p.Events)), Data: string(p.Events)}
	b, _ := json.Marshal(row)
	h.replays.Add(b)
	w.WriteHeader(http.StatusAccepted)
}

// ---- подписанные токены для карты кликов, которую рисует страница клиента

// SignHeatmapToken выдаёт токен вида exp.hex, действующий до exp (unix).
func SignHeatmapToken(secret string, site uint32, exp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("heatmap|" + strconv.FormatUint(uint64(site), 10) + "|" + strconv.FormatInt(exp, 10)))
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(mac.Sum(nil))
}

// VerifyHeatmapToken проверяет подпись и срок.
func VerifyHeatmapToken(secret string, site uint32, token string, now time.Time) bool {
	i := strings.IndexByte(token, '.')
	if i <= 0 {
		return false
	}
	exp, err := strconv.ParseInt(token[:i], 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(SignHeatmapToken(secret, site, exp)), []byte(token))
}

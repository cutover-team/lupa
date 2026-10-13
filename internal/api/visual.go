package api

import (
	"context"
	"fmt"
	"strings"

	"lupa/internal/ch"
)

// ---- карта кликов и скролла

type ClickElement struct {
	Selector string  `json:"selector"`
	Text     string  `json:"text"`
	Clicks   uint64  `json:"clicks"`
	Visitors uint64  `json:"visitors"`
	Share    float64 `json:"share"` // доля от всех кликов на странице
}

type ClickPoint struct {
	X  float64 `json:"x"`
	Y  uint32  `json:"y"`
	VW uint16  `json:"vw"`
}

type ScrollBucket struct {
	Depth   int     `json:"depth"`   // 25, 50, 75, 100
	Reached float64 `json:"reached"` // доля визитов, дошедших до глубины
}

type ClickReport struct {
	Path      string         `json:"path"`
	Pageviews uint64         `json:"pageviews"`
	Clicks    uint64         `json:"clicks"`
	Elements  []ClickElement `json:"elements"`
	Scroll    []ScrollBucket `json:"scroll"`
	Pages     []Row          `json:"pages"` // страницы с кликами, для выбора
}

func (s *Stats) ClickPages(ctx context.Context, site uint32, p Period) ([]Row, error) {
	q := fmt.Sprintf(`SELECT path AS key, uniq(visitor_id) AS visitors, uniq(session_id) AS visits, count() AS pageviews
FROM clicks WHERE %s GROUP BY key ORDER BY pageviews DESC LIMIT 50`, s.where(site, p))
	return ch.Query[Row](ctx, s.ch, q)
}

func (s *Stats) ClickReport(ctx context.Context, site uint32, p Period, path string) (ClickReport, error) {
	out := ClickReport{Path: path, Elements: []ClickElement{}, Scroll: []ScrollBucket{}}
	var err error
	if out.Pages, err = s.ClickPages(ctx, site, p); err != nil {
		return out, err
	}
	if out.Pages == nil {
		out.Pages = []Row{}
	}
	if path == "" {
		if len(out.Pages) > 0 {
			path = out.Pages[0].Key
		} else {
			path = "/"
		}
		out.Path = path
	}
	w := s.where(site, p) + " AND path = " + esc(path)
	totals, err := ch.Query[struct {
		Pageviews uint64 `json:"pageviews"`
	}](ctx, s.ch, "SELECT countIf(name='pageview') AS pageviews FROM events WHERE "+w)
	if err != nil {
		return out, err
	}
	if len(totals) > 0 {
		out.Pageviews = totals[0].Pageviews
	}
	els, err := ch.Query[ClickElement](ctx, s.ch, fmt.Sprintf(`WITH (SELECT count() FROM clicks WHERE %[1]s) AS total
SELECT selector, any(text) AS text, count() AS clicks, uniq(visitor_id) AS visitors, if(total = 0, 0, count() / total) AS share
FROM clicks WHERE %[1]s GROUP BY selector ORDER BY clicks DESC LIMIT 40`, w))
	if err != nil {
		return out, err
	}
	if els != nil {
		out.Elements = els
	}
	for _, e := range out.Elements {
		out.Clicks += e.Clicks
	}
	// Глубина скролла из событий engage: доля визитов, дошедших до 25/50/75/100 %.
	sc, err := ch.Query[struct {
		D25, D50, D75, D100 float64
	}](ctx, s.ch, fmt.Sprintf(`SELECT
  if(count() = 0, 0, countIf(m >= 25) / count()) AS D25,
  if(count() = 0, 0, countIf(m >= 50) / count()) AS D50,
  if(count() = 0, 0, countIf(m >= 75) / count()) AS D75,
  if(count() = 0, 0, countIf(m >= 100) / count()) AS D100
FROM (SELECT session_id, max(scroll_pct) AS m FROM events WHERE %s AND name = 'engage' GROUP BY session_id)`, w))
	if err != nil {
		return out, err
	}
	if len(sc) > 0 {
		out.Scroll = []ScrollBucket{{25, sc[0].D25}, {50, sc[0].D50}, {75, sc[0].D75}, {100, sc[0].D100}}
	}
	return out, nil
}

// ClickPoints — точки для наложения на страницу. Ограничение 5000, чтобы не перегружать браузер.
func (s *Stats) ClickPoints(ctx context.Context, site uint32, p Period, path string) ([]ClickPoint, error) {
	q := fmt.Sprintf(`SELECT x_pct AS x, y, vw FROM clicks WHERE %s AND path = %s ORDER BY ts DESC LIMIT 5000`, s.where(site, p), esc(path))
	rows, err := ch.Query[ClickPoint](ctx, s.ch, q)
	if rows == nil {
		rows = []ClickPoint{}
	}
	return rows, err
}

// ---- вебвизор

type ReplaySummary struct {
	RID       string `json:"rid"`
	Start     string `json:"start"`
	Duration  uint64 `json:"duration"` // секунды
	Pages     uint64 `json:"pages"`
	FirstPath string `json:"first_path"`
	Device    string `json:"device"`
	Browser   string `json:"browser"`
	OS        string `json:"os"`
	Source    string `json:"source"`
	Bytes     uint64 `json:"bytes"`
}

func (s *Stats) Replays(ctx context.Context, site uint32, p Period, tz string, limit int) ([]ReplaySummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := fmt.Sprintf(`SELECT rid, formatDateTime(min(ts), '%%Y-%%m-%%d %%H:%%i', %s) AS start,
  toUInt64(greatest(0, (max(last_ms) - min(first_ms)) / 1000)) AS duration,
  uniq(path) AS pages, argMin(path, seq) AS first_path, any(device) AS device, any(browser) AS browser, any(os) AS os,
  argMin(ref_source, seq) AS source, sum(bytes) AS bytes
FROM replays WHERE %s GROUP BY rid ORDER BY min(ts) DESC LIMIT %d`, esc(tz), s.where(site, p), limit)
	rows, err := ch.Query[ReplaySummary](ctx, s.ch, q)
	if rows == nil {
		rows = []ReplaySummary{}
	}
	return rows, err
}

// ReplayEvents склеивает чанки в один JSON-массив событий rrweb.
func (s *Stats) ReplayEvents(ctx context.Context, site uint32, rid string) ([]byte, error) {
	q := fmt.Sprintf(`SELECT data FROM replays WHERE site_id = %d AND rid = %s ORDER BY seq`, site, esc(rid))
	rows, err := ch.Query[struct {
		Data string `json:"data"`
	}](ctx, s.ch, q)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteByte('[')
	first := true
	for _, r := range rows {
		d := strings.TrimSpace(r.Data)
		d = strings.TrimPrefix(d, "[")
		d = strings.TrimSuffix(d, "]")
		if d == "" {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(d)
	}
	b.WriteByte(']')
	return []byte(b.String()), nil
}

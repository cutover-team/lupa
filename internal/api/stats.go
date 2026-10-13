// Package api — запросы статистики к ClickHouse и JSON-эндпоинты.
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lupa/internal/ch"
)

type Stats struct {
	ch *ch.Client
}

// ErrBadInput — ошибка входных параметров, а не хранилища.
var ErrBadInput = errors.New("неверные параметры")

func NewStats(c *ch.Client) *Stats { return &Stats{ch: c} }

// Period — интервал [From, To) в UTC.
type Period struct {
	From, To time.Time
}

func (p Period) dur() time.Duration { return p.To.Sub(p.From) }

// Prev — предыдущий интервал той же длины, для сравнения.
func (p Period) Prev() Period { return Period{From: p.From.Add(-p.dur()), To: p.From} }

// ParsePeriod разбирает пресеты и произвольные даты. Даты считаются в заданном часовом поясе.
func ParsePeriod(preset, from, to string, loc *time.Location) (Period, error) {
	now := time.Now().In(loc)
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc) }
	switch preset {
	case "", "today":
		return Period{From: day(now).UTC(), To: day(now).Add(24 * time.Hour).UTC()}, nil
	case "yesterday":
		return Period{From: day(now).Add(-24 * time.Hour).UTC(), To: day(now).UTC()}, nil
	case "7d":
		return Period{From: day(now).Add(-6 * 24 * time.Hour).UTC(), To: day(now).Add(24 * time.Hour).UTC()}, nil
	case "30d":
		return Period{From: day(now).Add(-29 * 24 * time.Hour).UTC(), To: day(now).Add(24 * time.Hour).UTC()}, nil
	case "month":
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		return Period{From: start.UTC(), To: start.AddDate(0, 1, 0).UTC()}, nil
	case "custom":
		f, err1 := time.ParseInLocation("2006-01-02", from, loc)
		t, err2 := time.ParseInLocation("2006-01-02", to, loc)
		if err1 != nil || err2 != nil || !t.After(f.Add(-time.Second)) {
			return Period{}, errors.New("неверные даты")
		}
		return Period{From: f.UTC(), To: t.Add(24 * time.Hour).UTC()}, nil
	}
	return Period{}, errors.New("неизвестный период")
}

func ts(t time.Time) string {
	return "toDateTime('" + t.UTC().Format("2006-01-02 15:04:05") + "', 'UTC')"
}

// esc экранирует строку для SQL-литерала ClickHouse.
func esc(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

func (s *Stats) where(site uint32, p Period) string {
	return fmt.Sprintf("site_id = %d AND ts >= %s AND ts < %s", site, ts(p.From), ts(p.To))
}

type Overview struct {
	Visitors    uint64  `json:"visitors"`
	Visits      uint64  `json:"visits"`
	Pageviews   uint64  `json:"pageviews"`
	BounceRate  float64 `json:"bounce_rate"`
	AvgDuration float64 `json:"avg_duration"` // секунды
	Events      uint64  `json:"events"`
}

func (s *Stats) Overview(ctx context.Context, site uint32, p Period) (Overview, error) {
	q := fmt.Sprintf(`SELECT
  uniq(visitor_id) AS visitors,
  count() AS visits,
  sum(pv) AS pageviews,
  if(count() = 0, 0, countIf(pv <= 1) / count()) AS bounce_rate,
  if(count() = 0, 0, avg(dur)) AS avg_duration,
  sum(ev) AS events
FROM (
  SELECT session_id, any(visitor_id) AS visitor_id,
         countIf(name = 'pageview') AS pv,
         countIf(name NOT IN ('pageview', 'engage')) AS ev,
         greatest(dateDiff('second', min(ts), max(ts)), toInt64(maxIf(duration_ms, name = 'engage') / 1000)) AS dur
  FROM events WHERE %s GROUP BY session_id
)`, s.where(site, p))
	rows, err := ch.Query[Overview](ctx, s.ch, q)
	if err != nil || len(rows) == 0 {
		return Overview{}, err
	}
	return rows[0], nil
}

type Point struct {
	T         string `json:"t"`
	Visitors  uint64 `json:"visitors"`
	Pageviews uint64 `json:"pageviews"`
}

// Timeseries — по часам для интервалов до 2 суток, иначе по дням, в часовом поясе loc.
// Пустые интервалы заполняются нулями, чтобы график был непрерывным.
func (s *Stats) Timeseries(ctx context.Context, site uint32, p Period, loc *time.Location) ([]Point, string, error) {
	tz := loc.String()
	bucket := "toStartOfDay(ts, " + esc(tz) + ")"
	unit := "day"
	step := func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
	if p.dur() <= 48*time.Hour {
		bucket = "toStartOfHour(ts, " + esc(tz) + ")"
		unit = "hour"
		step = func(t time.Time) time.Time { return t.Add(time.Hour) }
	}
	q := fmt.Sprintf(`SELECT formatDateTime(%s, '%%Y-%%m-%%dT%%H:%%i:%%S') AS t, uniq(visitor_id) AS visitors, countIf(name='pageview') AS pageviews
FROM events WHERE %s GROUP BY t ORDER BY t`, bucket, s.where(site, p))
	rows, err := ch.Query[Point](ctx, s.ch, q)
	if err != nil {
		return nil, unit, err
	}
	have := make(map[string]Point, len(rows))
	for _, r := range rows {
		have[r.T] = r
	}
	const layout = "2006-01-02T15:04:05"
	var out []Point
	start := p.From.In(loc)
	if unit == "day" {
		start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	} else {
		start = start.Truncate(time.Hour)
	}
	for t, n := start, 0; t.Before(p.To) && n < 1000; t, n = step(t), n+1 {
		key := t.Format(layout)
		if r, ok := have[key]; ok {
			out = append(out, r)
		} else {
			out = append(out, Point{T: key})
		}
	}
	return out, unit, nil
}

type Row struct {
	Key       string `json:"key"`
	Visitors  uint64 `json:"visitors"`
	Visits    uint64 `json:"visits"`
	Pageviews uint64 `json:"pageviews"`
}

var breakdownCols = map[string]string{
	"path":         "path",
	"title":        "title",
	"source":       "ref_source",
	"ref_domain":   "ref_domain",
	"utm_source":   "utm_source",
	"utm_medium":   "utm_medium",
	"utm_campaign": "utm_campaign",
	"utm_content":  "utm_content",
	"utm_term":     "utm_term",
	"device":       "device",
	"browser":      "browser",
	"os":           "os",
	"country":      "country",
	"lang":         "lang",
	"screen":       "concat(toString(screen_w), 'x', toString(screen_h))",
}

// Breakdown — топ значений по колонке среди просмотров страниц.
func (s *Stats) Breakdown(ctx context.Context, site uint32, p Period, by string, limit int) ([]Row, error) {
	col, ok := breakdownCols[by]
	if !ok {
		return nil, fmt.Errorf("%w: неизвестный разрез %q", ErrBadInput, by)
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	filter := ""
	if strings.HasPrefix(by, "utm_") || by == "ref_domain" || by == "country" {
		filter = " AND " + col + " != ''"
	}
	q := fmt.Sprintf(`SELECT %s AS key, uniq(visitor_id) AS visitors, uniq(session_id) AS visits, count() AS pageviews
FROM events WHERE %s AND name = 'pageview'%s GROUP BY key ORDER BY visitors DESC, pageviews DESC LIMIT %d`, col, s.where(site, p), filter, limit)
	return ch.Query[Row](ctx, s.ch, q)
}

// EntryPages — страницы входа: первая страница сессии.
func (s *Stats) EntryPages(ctx context.Context, site uint32, p Period, limit int) ([]Row, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	q := fmt.Sprintf(`SELECT key, uniq(visitor_id) AS visitors, count() AS visits, 0 AS pageviews FROM (
  SELECT session_id, any(visitor_id) AS visitor_id, argMin(path, ts) AS key
  FROM events WHERE %s AND name = 'pageview' GROUP BY session_id
) GROUP BY key ORDER BY visits DESC LIMIT %d`, s.where(site, p), limit)
	return ch.Query[Row](ctx, s.ch, q)
}

type EventRow struct {
	Name       string  `json:"name"`
	Count      uint64  `json:"count"`
	Sessions   uint64  `json:"sessions"`
	Visitors   uint64  `json:"visitors"`
	Conversion float64 `json:"conversion"` // доля сессий с событием
}

// Events — все пользовательские события с конверсией от числа сессий периода.
func (s *Stats) Events(ctx context.Context, site uint32, p Period) ([]EventRow, error) {
	q := fmt.Sprintf(`WITH (SELECT uniq(session_id) FROM events WHERE %[1]s) AS total
SELECT name, count() AS count, uniq(session_id) AS sessions, uniq(visitor_id) AS visitors,
       if(total = 0, 0, uniq(session_id) / total) AS conversion
FROM events WHERE %[1]s AND name NOT IN ('pageview', 'engage') GROUP BY name ORDER BY count DESC LIMIT 100`, s.where(site, p))
	return ch.Query[EventRow](ctx, s.ch, q)
}

// GoalConversion — конверсия в цель: событие по имени или просмотр пути с заданным префиксом.
func (s *Stats) GoalConversion(ctx context.Context, site uint32, p Period, event string) (EventRow, error) {
	cond := "name = " + esc(event)
	if strings.HasPrefix(event, "/") {
		cond = "(name = 'pageview' AND startsWith(path, " + esc(event) + "))"
	}
	// Алиас результата не должен совпадать с колонкой name: в ClickHouse алиасы видны в выражениях.
	q := fmt.Sprintf(`WITH (SELECT uniq(session_id) FROM events WHERE %[1]s) AS total
SELECT countIf(%[2]s) AS count, uniqIf(session_id, %[2]s) AS sessions, uniqIf(visitor_id, %[2]s) AS visitors,
       if(total = 0, 0, uniqIf(session_id, %[2]s) / total) AS conversion
FROM events WHERE %[1]s`, s.where(site, p), cond)
	rows, err := ch.Query[EventRow](ctx, s.ch, q)
	if err != nil || len(rows) == 0 {
		return EventRow{Name: event}, err
	}
	rows[0].Name = event
	return rows[0], nil
}

type Realtime struct {
	Visitors uint64  `json:"visitors"` // за последние 5 минут
	Pages    []Row   `json:"pages"`    // что смотрят сейчас
	Minutes  []Point `json:"minutes"`  // по минутам за полчаса
	Sources  []Row   `json:"sources"`
}

func (s *Stats) Realtime(ctx context.Context, site uint32, tz string) (Realtime, error) {
	var out Realtime
	base := fmt.Sprintf("site_id = %d AND ts >= now() - INTERVAL 5 MINUTE", site)
	v, err := ch.Query[struct {
		Visitors uint64 `json:"visitors"`
	}](ctx, s.ch, "SELECT uniq(visitor_id) AS visitors FROM events WHERE "+base)
	if err != nil {
		return out, err
	}
	if len(v) > 0 {
		out.Visitors = v[0].Visitors
	}
	if out.Pages, err = ch.Query[Row](ctx, s.ch, "SELECT path AS key, uniq(visitor_id) AS visitors, uniq(session_id) AS visits, count() AS pageviews FROM events WHERE "+base+" AND name='pageview' GROUP BY key ORDER BY visitors DESC LIMIT 10"); err != nil {
		return out, err
	}
	if out.Sources, err = ch.Query[Row](ctx, s.ch, "SELECT ref_source AS key, uniq(visitor_id) AS visitors, uniq(session_id) AS visits, count() AS pageviews FROM events WHERE "+base+" AND name='pageview' GROUP BY key ORDER BY visitors DESC LIMIT 10"); err != nil {
		return out, err
	}
	out.Minutes, err = ch.Query[Point](ctx, s.ch, fmt.Sprintf(`SELECT formatDateTime(m, '%%H:%%i', %s) AS t, visitors, pageviews FROM (
  SELECT toStartOfMinute(ts) AS m, uniq(visitor_id) AS visitors, countIf(name='pageview') AS pageviews
  FROM events WHERE site_id = %d AND ts >= now() - INTERVAL 30 MINUTE GROUP BY m
) ORDER BY m`, esc(tz), site))
	// Заполняем все 30 минут, чтобы график был непрерывным.
	have := make(map[string]Point, len(out.Minutes))
	for _, m := range out.Minutes {
		have[m.T] = m
	}
	loc, lerr := time.LoadLocation(tz)
	if lerr != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc).Truncate(time.Minute)
	out.Minutes = make([]Point, 0, 31)
	for i := 30; i >= 0; i-- {
		key := now.Add(-time.Duration(i) * time.Minute).Format("15:04")
		if m, ok := have[key]; ok {
			out.Minutes = append(out.Minutes, m)
		} else {
			out.Minutes = append(out.Minutes, Point{T: key})
		}
	}
	if out.Pages == nil {
		out.Pages = []Row{}
	}
	if out.Sources == nil {
		out.Sources = []Row{}
	}
	return out, err
}

type FunnelStep struct {
	Step     string  `json:"step"`
	Sessions uint64  `json:"sessions"`
	Rate     float64 `json:"rate"` // доля от первого шага
}

// Funnel — последовательность шагов внутри сессии. Шаг: имя события или путь, начинающийся с /.
func (s *Stats) Funnel(ctx context.Context, site uint32, p Period, steps []string) ([]FunnelStep, error) {
	if len(steps) < 2 || len(steps) > 8 {
		return nil, fmt.Errorf("%w: воронка от 2 до 8 шагов", ErrBadInput)
	}
	conds := make([]string, len(steps))
	for i, st := range steps {
		st = strings.TrimSpace(st)
		if st == "" {
			return nil, fmt.Errorf("%w: пустой шаг", ErrBadInput)
		}
		if strings.HasPrefix(st, "/") {
			conds[i] = "(name = 'pageview' AND startsWith(path, " + esc(st) + "))"
		} else {
			conds[i] = "name = " + esc(st)
		}
	}
	// sequenceMatch — агрегатная функция, поэтому считаем её по сессиям во вложенном запросе.
	var inner []string
	for i := range steps {
		pat := ""
		for j := 0; j <= i; j++ {
			if j > 0 {
				pat += ".*"
			}
			pat += fmt.Sprintf("(?%d)", j+1)
		}
		inner = append(inner, fmt.Sprintf("sequenceMatch('%s')(ts, %s) AS m%d", pat, strings.Join(conds, ", "), i))
	}
	var outer []string
	for i := range steps {
		outer = append(outer, fmt.Sprintf("countIf(m%d) AS s%d", i, i))
	}
	q := fmt.Sprintf("SELECT %s FROM (SELECT session_id, %s FROM events WHERE %s GROUP BY session_id)",
		strings.Join(outer, ", "), strings.Join(inner, ", "), s.where(site, p))
	rows, err := ch.Query[map[string]uint64](ctx, s.ch, q)
	if err != nil {
		return nil, err
	}
	out := make([]FunnelStep, len(steps))
	var first uint64
	for i, st := range steps {
		var n uint64
		if len(rows) > 0 {
			n = rows[0][fmt.Sprintf("s%d", i)]
		}
		if i == 0 {
			first = n
		}
		rate := 0.0
		if first > 0 {
			rate = float64(n) / float64(first)
		}
		out[i] = FunnelStep{Step: st, Sessions: n, Rate: rate}
	}
	return out, nil
}

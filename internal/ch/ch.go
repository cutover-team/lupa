// Package ch — минимальный клиент ClickHouse поверх HTTP-интерфейса, без внешних зависимостей.
package ch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	base string
	db   string
	user string
	pass string
	http *http.Client
}

func New(baseURL, db, user, pass string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), db: db, user: user, pass: pass,
		http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) do(ctx context.Context, query string, body io.Reader, useDB bool) ([]byte, error) {
	q := url.Values{}
	q.Set("query", query)
	if useDB {
		q.Set("database", c.db)
	}
	q.Set("output_format_json_quote_64bit_integers", "0")
	q.Set("date_time_output_format", "iso")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/?"+q.Encode(), body)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.Header.Set("X-ClickHouse-User", c.user)
		req.Header.Set("X-ClickHouse-Key", c.pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clickhouse %d: %s", resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Exec выполняет запрос без результата.
func (c *Client) Exec(ctx context.Context, query string) error {
	_, err := c.do(ctx, query, nil, true)
	return err
}

// Ping проверяет доступность сервера.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.do(ctx, "SELECT 1", nil, false)
	return err
}

// Query выполняет SELECT и раскладывает строки в []T по именам полей (FORMAT JSON).
func Query[T any](ctx context.Context, c *Client, query string) ([]T, error) {
	out, err := c.do(ctx, query+" FORMAT JSON", nil, true)
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Data []T `json:"data"`
	}
	if err := json.Unmarshal(out, &wrap); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return wrap.Data, nil
}

// InsertRows вставляет строки в формате JSONEachRow.
func (c *Client) InsertRows(ctx context.Context, table string, rows [][]byte) error {
	if len(rows) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, r := range rows {
		buf.Write(r)
		buf.WriteByte('\n')
	}
	_, err := c.do(ctx, "INSERT INTO "+table+" FORMAT JSONEachRow", &buf, true)
	return err
}

// EnsureSchema создаёт базу и таблицу событий.
func (c *Client) EnsureSchema(ctx context.Context) error {
	if _, err := c.do(ctx, "CREATE DATABASE IF NOT EXISTS "+c.db, nil, false); err != nil {
		return err
	}
	if err := c.Exec(ctx, `CREATE TABLE IF NOT EXISTS events (
  site_id      UInt32,
  ts           DateTime,
  name         LowCardinality(String),
  visitor_id   UInt64,
  session_id   UInt64,
  url          String,
  path         String,
  title        String,
  referrer     String,
  ref_domain   String,
  ref_source   LowCardinality(String),
  utm_source   String,
  utm_medium   String,
  utm_campaign String,
  utm_content  String,
  utm_term     String,
  device       LowCardinality(String),
  browser      LowCardinality(String),
  os           LowCardinality(String),
  screen_w     UInt16,
  screen_h     UInt16,
  lang         LowCardinality(String),
  country      LowCardinality(String),
  duration_ms  UInt32,
  scroll_pct   UInt8,
  props        String
) ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (site_id, ts, visitor_id)
TTL ts + INTERVAL 24 MONTH`); err != nil {
		return err
	}
	if err := c.Exec(ctx, `CREATE TABLE IF NOT EXISTS clicks (
  site_id    UInt32,
  ts         DateTime,
  visitor_id UInt64,
  session_id UInt64,
  path       String,
  x_pct      Float32,
  y          UInt32,
  vw         UInt16,
  vh         UInt16,
  selector   String,
  text       String
) ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (site_id, path, ts)
TTL ts + INTERVAL 6 MONTH`); err != nil {
		return err
	}
	return c.Exec(ctx, `CREATE TABLE IF NOT EXISTS replays (
  site_id    UInt32,
  rid        String,
  visitor_id UInt64,
  ts         DateTime,
  seq        UInt32,
  path       String,
  first_ms   UInt64,
  last_ms    UInt64,
  device     LowCardinality(String),
  browser    LowCardinality(String),
  os         LowCardinality(String),
  ref_source LowCardinality(String),
  bytes      UInt32,
  data       String CODEC(ZSTD(3))
) ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (site_id, rid, seq)
TTL ts + INTERVAL 30 DAY`)
}

// Batcher копит строки и сбрасывает их пачками: по размеру или по таймеру.
type Batcher struct {
	c        *Client
	table    string
	mu       sync.Mutex
	rows     [][]byte
	maxRows  int
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
}

func NewBatcher(c *Client, table string, maxRows int, interval time.Duration) *Batcher {
	b := &Batcher{c: c, table: table, maxRows: maxRows, interval: interval, stop: make(chan struct{})}
	b.wg.Add(1)
	go b.loop()
	return b
}

func (b *Batcher) Add(row []byte) {
	b.mu.Lock()
	b.rows = append(b.rows, row)
	n := len(b.rows)
	b.mu.Unlock()
	if n >= b.maxRows {
		b.Flush()
	}
}

func (b *Batcher) Flush() {
	b.mu.Lock()
	rows := b.rows
	b.rows = nil
	b.mu.Unlock()
	if len(rows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := b.c.InsertRows(ctx, b.table, rows); err != nil {
		log.Printf("clickhouse insert failed (%d rows): %v", len(rows), err)
		// Возвращаем строки в очередь, чтобы не терять события при кратком сбое.
		b.mu.Lock()
		if len(b.rows) < b.maxRows*10 {
			b.rows = append(rows, b.rows...)
		}
		b.mu.Unlock()
	}
}

func (b *Batcher) loop() {
	defer b.wg.Done()
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			b.Flush()
		case <-b.stop:
			b.Flush()
			return
		}
	}
}

func (b *Batcher) Close() {
	close(b.stop)
	b.wg.Wait()
}

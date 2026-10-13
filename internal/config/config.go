// Package config читает настройки из переменных окружения.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Addr           string // адрес HTTP-сервера, например :8080
	DataDir        string // каталог для SQLite и секрета
	ClickHouseURL  string
	ClickHouseDB   string
	ClickHouseUser string
	ClickHousePass string
	Secret         string // соль для хеширования посетителей
	TrustProxy     bool   // брать IP из X-Forwarded-For
	BaseURL        string // публичный адрес, для кода счётчика
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() (Config, error) {
	c := Config{
		Addr:           env("LUPA_ADDR", ":8080"),
		DataDir:        env("LUPA_DATA_DIR", "./data"),
		ClickHouseURL:  env("LUPA_CLICKHOUSE_URL", "http://localhost:8123"),
		ClickHouseDB:   env("LUPA_CLICKHOUSE_DB", "lupa"),
		ClickHouseUser: env("LUPA_CLICKHOUSE_USER", "default"),
		ClickHousePass: env("LUPA_CLICKHOUSE_PASSWORD", ""),
		Secret:         env("LUPA_SECRET", ""),
		TrustProxy:     strings.EqualFold(env("LUPA_TRUST_PROXY", "true"), "true"),
		BaseURL:        strings.TrimRight(env("LUPA_BASE_URL", "http://localhost:8080"), "/"),
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return c, err
	}
	// Секрет нужен стабильный: от него зависят идентификаторы посетителей в течение дня.
	if c.Secret == "" {
		p := filepath.Join(c.DataDir, "secret")
		b, err := os.ReadFile(p)
		if err == nil && len(b) > 0 {
			c.Secret = strings.TrimSpace(string(b))
		} else {
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return c, err
			}
			c.Secret = hex.EncodeToString(raw)
			if err := os.WriteFile(p, []byte(c.Secret), 0o600); err != nil {
				return c, err
			}
		}
	}
	return c, nil
}

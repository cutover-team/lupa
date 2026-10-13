// Lupa — веб-аналитика без cookie на ClickHouse. Один бинарник: счётчик, приём событий, отчёты.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"lupa/internal/api"
	"lupa/internal/ch"
	"lupa/internal/config"
	"lupa/internal/ingest"
	"lupa/internal/store"
	"lupa/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	loc, err := time.LoadLocation(envOr("LUPA_TZ", "Europe/Moscow"))
	if err != nil {
		log.Fatalf("timezone: %v", err)
	}

	chc := ch.New(cfg.ClickHouseURL, cfg.ClickHouseDB, cfg.ClickHouseUser, cfg.ClickHousePass)
	// ClickHouse в docker-compose стартует дольше нас: ждём до минуты.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	for {
		if err = chc.Ping(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			log.Fatalf("clickhouse недоступен: %v", err)
		case <-time.After(2 * time.Second):
		}
	}
	cancel()
	if err := chc.EnsureSchema(context.Background()); err != nil {
		log.Fatalf("schema: %v", err)
	}

	st, err := store.Open(filepath.Join(cfg.DataDir, "lupa.db"))
	if err != nil {
		log.Fatalf("sqlite: %v", err)
	}
	defer st.Close()

	batch := ch.NewBatcher(chc, "events", 500, 2*time.Second)
	clicks := ch.NewBatcher(chc, "clicks", 500, 2*time.Second)
	replays := ch.NewBatcher(chc, "replays", 50, 3*time.Second)
	sessions := ingest.NewSessions(30 * time.Minute)
	replayAllowed := func(site uint32) bool { s, ok := st.Site(site); return ok && s.Replay }
	ing := ingest.NewHandler(batch, clicks, replays, sessions, cfg.Secret, cfg.TrustProxy, st.Lookup, replayAllowed)
	srv := web.New(st, api.NewStats(chc), ing, cfg.BaseURL, loc)
	if demo := os.Getenv("LUPA_DEMO_USER"); demo != "" {
		srv.SetDemo(demo, envOr("LUPA_READONLY", "true") == "true")
		log.Printf("режим демо: автоматический вход под %s, только чтение", demo)
	}

	hs := &http.Server{Addr: cfg.Addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second}
	go func() {
		log.Printf("lupa слушает %s, адрес %s", cfg.Addr, cfg.BaseURL)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("останавливаюсь, сбрасываю буфер событий")
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer c2()
	_ = hs.Shutdown(shutdownCtx)
	sessions.Close()
	batch.Close()
	clicks.Close()
	replays.Close()
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

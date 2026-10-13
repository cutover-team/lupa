// Package web — HTTP-маршруты: страницы, авторизация, JSON API статистики.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lupa/internal/api"
	"lupa/internal/ingest"
	"lupa/internal/store"
)

//go:embed templates/*.html
var tplFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	store   *store.Store
	stats   *api.Stats
	ingest  *ingest.Handler
	baseURL string
	loc     *time.Location
	tpl     *template.Template
	mux     *http.ServeMux
	// Публичное демо: посетители входят автоматически под этим пользователем и ничего не могут менять.
	demoEmail string
	readOnly  bool
}

// SetDemo включает автоматический вход под demoEmail и запрет изменений.
func (s *Server) SetDemo(email string, readOnly bool) {
	s.demoEmail = strings.ToLower(strings.TrimSpace(email))
	s.readOnly = readOnly
}

func New(st *store.Store, stats *api.Stats, ing *ingest.Handler, baseURL string, loc *time.Location) *Server {
	s := &Server{store: st, stats: stats, ingest: ing, baseURL: baseURL, loc: loc}
	s.tpl = template.Must(template.ParseFS(tplFS, "templates/*.html"))
	m := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	m.HandleFunc("GET /lupa.js", s.tracker)
	m.HandleFunc("GET /lupa-visual.js", s.trackerVisual)
	m.Handle("/api/event", ing)
	m.HandleFunc("/api/click", ing.ServeClick)
	m.HandleFunc("/api/replay", ing.ServeReplay)
	m.HandleFunc("GET /api/heatmap", s.apiHeatmapPublic)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	m.HandleFunc("GET /login", s.loginPage)
	m.HandleFunc("POST /login", s.login)
	m.HandleFunc("GET /setup", s.setupPage)
	m.HandleFunc("POST /setup", s.setup)
	m.HandleFunc("POST /logout", s.logout)

	m.HandleFunc("GET /{$}", s.auth(s.sitesPage))
	m.HandleFunc("POST /sites", s.auth(s.createSite))
	m.HandleFunc("GET /sites/{id}", s.auth(s.sitePage))
	m.HandleFunc("GET /sites/{id}/settings", s.auth(s.settingsPage))
	m.HandleFunc("POST /sites/{id}/goals", s.auth(s.createGoal))
	m.HandleFunc("POST /sites/{id}/goals/{gid}/delete", s.auth(s.deleteGoal))
	m.HandleFunc("POST /sites/{id}/delete", s.auth(s.deleteSite))
	m.HandleFunc("POST /sites/{id}/replay", s.auth(s.setReplay))
	m.HandleFunc("GET /sites/{id}/clicks", s.auth(s.clicksPage))
	m.HandleFunc("GET /sites/{id}/replays", s.auth(s.replaysPage))
	m.HandleFunc("GET /sites/{id}/replays/{rid}", s.auth(s.replayPage))
	m.HandleFunc("GET /api/sites/{id}/clicks", s.auth(s.apiClicks))
	m.HandleFunc("GET /api/sites/{id}/heatmap-url", s.auth(s.apiHeatmapURL))
	m.HandleFunc("GET /api/sites/{id}/replays", s.auth(s.apiReplays))
	m.HandleFunc("GET /api/sites/{id}/replays/{rid}", s.auth(s.apiReplayEvents))

	m.HandleFunc("GET /api/sites/{id}/overview", s.auth(s.apiOverview))
	m.HandleFunc("GET /api/sites/{id}/timeseries", s.auth(s.apiTimeseries))
	m.HandleFunc("GET /api/sites/{id}/breakdown", s.auth(s.apiBreakdown))
	m.HandleFunc("GET /api/sites/{id}/entry", s.auth(s.apiEntry))
	m.HandleFunc("GET /api/sites/{id}/events", s.auth(s.apiEvents))
	m.HandleFunc("GET /api/sites/{id}/goals", s.auth(s.apiGoals))
	m.HandleFunc("GET /api/sites/{id}/realtime", s.auth(s.apiRealtime))
	m.HandleFunc("GET /api/sites/{id}/funnel", s.auth(s.apiFunnel))
	s.mux = m
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) tracker(w http.ResponseWriter, r *http.Request) { s.serveJS(w, "static/lupa.js") }

func (s *Server) trackerVisual(w http.ResponseWriter, r *http.Request) {
	s.serveJS(w, "static/lupa-visual.js")
}

func (s *Server) serveJS(w http.ResponseWriter, name string) {
	b, _ := staticFS.ReadFile(name)
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(b)
}

// VendorReady — лежат ли библиотеки rrweb (их скачивают командой make vendor).
func (s *Server) vendorReady() bool {
	_, err := fs.Stat(staticFS, "static/vendor/rrweb.min.js")
	return err == nil
}

// ---- авторизация

type ctxKey int

const userKey ctxKey = 1

func (s *Server) currentUser(r *http.Request) (store.User, bool) {
	c, err := r.Cookie("lupa_session")
	if err != nil || c.Value == "" {
		return store.User{}, false
	}
	u, err := s.store.UserByToken(r.Context(), c.Value)
	return u, err == nil
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.readOnly && r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "демо работает в режиме только для чтения", http.StatusForbidden)
			return
		}
		u, ok := s.currentUser(r)
		if !ok && s.demoEmail != "" {
			if du, err := s.store.UserByEmail(r.Context(), s.demoEmail); err == nil {
				if token, err := s.store.CreateLoginSession(r.Context(), du.ID, 24*time.Hour); err == nil {
					http.SetCookie(w, &http.Cookie{Name: "lupa_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
						Secure: strings.HasPrefix(s.baseURL, "https://"), MaxAge: 24 * 3600})
					u, ok = du, true
				}
			}
		}
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				jsonErr(w, http.StatusUnauthorized, "нужен вход")
				return
			}
			n, _ := s.store.UserCount(r.Context())
			if n == 0 {
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

func userFrom(r *http.Request) *store.User {
	if u, ok := r.Context().Value(userKey).(store.User); ok {
		return &u
	}
	return nil
}

type page struct {
	Title       string
	User        *store.User
	Setup       bool
	Error       string
	Sites       []store.Site
	Site        store.Site
	Goals       []store.Goal
	BaseURL     string
	RID         string
	VendorReady bool
	Section     string
}

func (s *Server) render(w http.ResponseWriter, name string, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, p); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.demoEmail != "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if n, _ := s.store.UserCount(r.Context()); n == 0 {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	s.render(w, "login.html", page{Title: "Вход"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.Authenticate(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		time.Sleep(300 * time.Millisecond)
		s.render(w, "login.html", page{Title: "Вход", Error: "Неверная почта или пароль"})
		return
	}
	s.startSession(w, r, u)
}

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.store.UserCount(r.Context()); n > 0 {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	s.render(w, "login.html", page{Title: "Настройка", Setup: true})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.store.UserCount(r.Context()); n > 0 {
		http.Error(w, "уже настроено", http.StatusForbidden)
		return
	}
	u, err := s.store.CreateUser(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		s.render(w, "login.html", page{Title: "Настройка", Setup: true, Error: err.Error()})
		return
	}
	s.startSession(w, r, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) {
	token, err := s.store.CreateLoginSession(r.Context(), u.ID, 30*24*time.Hour)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "lupa_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(s.baseURL, "https://"), MaxAge: 30 * 24 * 3600})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("lupa_session"); err == nil {
		_ = s.store.DeleteLoginSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "lupa_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ---- страницы

func (s *Server) sitesPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "sites.html", page{Title: "Сайты", User: userFrom(r), Sites: s.store.Sites(), Error: r.URL.Query().Get("error")})
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.CreateSite(r.Context(), r.FormValue("name"), r.FormValue("domain"))
	if err != nil {
		s.render(w, "sites.html", page{Title: "Сайты", User: userFrom(r), Sites: s.store.Sites(), Error: err.Error()})
		return
	}
	http.Redirect(w, r, "/sites/"+strconv.FormatUint(uint64(st.ID), 10)+"/settings", http.StatusFound)
}

func (s *Server) siteFrom(r *http.Request) (store.Site, bool) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		return store.Site{}, false
	}
	return s.store.Site(uint32(id))
}

func (s *Server) sitePage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, "site.html", page{Title: st.Name, User: userFrom(r), Site: st, Section: "stats"})
}

func (s *Server) clicksPage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, "clicks.html", page{Title: st.Name + " · карта кликов", User: userFrom(r), Site: st, Section: "clicks"})
}

func (s *Server) replaysPage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, "replays.html", page{Title: st.Name + " · вебвизор", User: userFrom(r), Site: st, Section: "replays", VendorReady: s.vendorReady()})
}

func (s *Server) replayPage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, "replay.html", page{Title: st.Name + " · запись", User: userFrom(r), Site: st, RID: r.PathValue("rid"), Section: "replays", VendorReady: s.vendorReady()})
}

func (s *Server) setReplay(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	pct, _ := strconv.Atoi(r.FormValue("pct"))
	_ = s.store.SetReplay(r.Context(), st.ID, r.FormValue("on") == "1", pct)
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/settings", http.StatusFound)
}

// ---- карта кликов и вебвизор, JSON

func (s *Server) apiClicks(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	rep, err := s.stats.ClickReport(r.Context(), st.ID, p, r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, err)
		return
	}
	jsonOK(w, rep)
}

// apiHeatmapURL — ссылка на страницу клиента с токеном: страница сама нарисует карту.
func (s *Server) apiHeatmapURL(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		jsonErr(w, http.StatusNotFound, "сайт не найден")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" || !strings.HasPrefix(path, "/") {
		path = "/"
	}
	preset := r.URL.Query().Get("preset")
	if preset == "" {
		preset = "7d"
	}
	tok := ingest.SignHeatmapToken(s.ingest.Secret(), st.ID, time.Now().Add(2*time.Hour).Unix())
	jsonOK(w, map[string]string{"url": "https://" + st.Domain + path + "#lupa-heatmap=" + tok + "&p=" + preset})
}

// apiHeatmapPublic — данные для наложения; доступ по подписанному токену, CORS открыт.
func (s *Server) apiHeatmapPublic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	q := r.URL.Query()
	id, err := strconv.ParseUint(q.Get("s"), 10, 32)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "site")
		return
	}
	st, ok := s.store.Site(uint32(id))
	if !ok || !ingest.VerifyHeatmapToken(s.ingest.Secret(), st.ID, q.Get("token"), time.Now()) {
		jsonErr(w, http.StatusForbidden, "токен недействителен или истёк, откройте карту заново из панели")
		return
	}
	p, err := api.ParsePeriod(q.Get("preset"), "", "", s.loc)
	if err != nil {
		p, _ = api.ParsePeriod("7d", "", "", s.loc)
	}
	path := q.Get("path")
	if path == "" {
		path = "/"
	}
	rep, err := s.stats.ClickReport(r.Context(), st.ID, p, path)
	if err != nil {
		s.fail(w, err)
		return
	}
	pts, err := s.stats.ClickPoints(r.Context(), st.ID, p, path)
	if err != nil {
		s.fail(w, err)
		return
	}
	jsonOK(w, map[string]any{"points": pts, "elements": rep.Elements, "scroll": rep.Scroll, "pageviews": rep.Pageviews})
}

func (s *Server) apiReplays(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.stats.Replays(r.Context(), st.ID, p, s.loc.String(), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	jsonOK(w, map[string]any{"replays": rows, "enabled": st.Replay, "pct": st.ReplayPct})
}

func (s *Server) apiReplayEvents(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		jsonErr(w, http.StatusNotFound, "сайт не найден")
		return
	}
	b, err := s.stats.ReplayEvents(r.Context(), st.ID, r.PathValue("rid"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(b)
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	goals, _ := s.store.Goals(r.Context(), st.ID)
	s.render(w, "settings.html", page{Title: st.Name + " · настройки", User: userFrom(r), Site: st, Goals: goals, BaseURL: s.baseURL, Error: r.URL.Query().Get("error")})
}

func (s *Server) createGoal(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := s.store.CreateGoal(r.Context(), st.ID, r.FormValue("name"), r.FormValue("event")); err != nil {
		http.Redirect(w, r, r.URL.Path[:len(r.URL.Path)-len("/goals")]+"/settings?error="+err.Error(), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/settings", http.StatusFound)
}

func (s *Server) deleteGoal(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	gid, _ := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	_ = s.store.DeleteGoal(r.Context(), st.ID, gid)
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/settings", http.StatusFound)
}

func (s *Server) deleteSite(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = s.store.DeleteSite(r.Context(), st.ID)
	http.Redirect(w, r, "/", http.StatusFound)
}

// ---- JSON API

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) sitePeriod(w http.ResponseWriter, r *http.Request) (store.Site, api.Period, bool) {
	st, ok := s.siteFrom(r)
	if !ok {
		jsonErr(w, http.StatusNotFound, "сайт не найден")
		return st, api.Period{}, false
	}
	q := r.URL.Query()
	p, err := api.ParsePeriod(q.Get("preset"), q.Get("from"), q.Get("to"), s.loc)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return st, p, false
	}
	return st, p, true
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("api: %v", err)
	jsonErr(w, http.StatusInternalServerError, "ошибка запроса к хранилищу")
}

func (s *Server) apiOverview(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	cur, err := s.stats.Overview(r.Context(), st.ID, p)
	if err != nil {
		s.fail(w, err)
		return
	}
	prev, err := s.stats.Overview(r.Context(), st.ID, p.Prev())
	if err != nil {
		s.fail(w, err)
		return
	}
	jsonOK(w, map[string]any{"current": cur, "previous": prev, "from": p.From, "to": p.To})
}

func (s *Server) apiTimeseries(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	pts, unit, err := s.stats.Timeseries(r.Context(), st.ID, p, s.loc)
	if err != nil {
		s.fail(w, err)
		return
	}
	if pts == nil {
		pts = []api.Point{}
	}
	jsonOK(w, map[string]any{"points": pts, "unit": unit})
}

func (s *Server) apiBreakdown(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.stats.Breakdown(r.Context(), st.ID, p, r.URL.Query().Get("by"), limit)
	if err != nil {
		if errors.Is(err, api.ErrBadInput) {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	if rows == nil {
		rows = []api.Row{}
	}
	jsonOK(w, map[string]any{"rows": rows})
}

func (s *Server) apiEntry(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.stats.EntryPages(r.Context(), st.ID, p, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	if rows == nil {
		rows = []api.Row{}
	}
	jsonOK(w, map[string]any{"rows": rows})
}

func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	rows, err := s.stats.Events(r.Context(), st.ID, p)
	if err != nil {
		s.fail(w, err)
		return
	}
	if rows == nil {
		rows = []api.EventRow{}
	}
	jsonOK(w, map[string]any{"events": rows})
}

func (s *Server) apiGoals(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	goals, err := s.store.Goals(r.Context(), st.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]api.EventRow, 0, len(goals))
	for _, g := range goals {
		row, err := s.stats.GoalConversion(r.Context(), st.ID, p, g.Event)
		if err != nil {
			s.fail(w, err)
			return
		}
		row.Name = g.Name
		out = append(out, row)
	}
	jsonOK(w, map[string]any{"goals": out})
}

func (s *Server) apiRealtime(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteFrom(r)
	if !ok {
		jsonErr(w, http.StatusNotFound, "сайт не найден")
		return
	}
	rt, err := s.stats.Realtime(r.Context(), st.ID, s.loc.String())
	if err != nil {
		s.fail(w, err)
		return
	}
	jsonOK(w, rt)
}

func (s *Server) apiFunnel(w http.ResponseWriter, r *http.Request) {
	st, p, ok := s.sitePeriod(w, r)
	if !ok {
		return
	}
	steps := strings.Split(r.URL.Query().Get("steps"), "|")
	out, err := s.stats.Funnel(r.Context(), st.ID, p, steps)
	if err != nil {
		if errors.Is(err, api.ErrBadInput) {
			jsonOK(w, map[string]any{"error": err.Error()})
			return
		}
		s.fail(w, err)
		return
	}
	jsonOK(w, map[string]any{"steps": out})
}

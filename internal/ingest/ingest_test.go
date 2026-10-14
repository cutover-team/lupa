package ingest

import (
	"testing"
	"time"
)

func TestParseUA(t *testing.T) {
	cases := []struct{ ua, browser, os, device string }{
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 YaBrowser/24.10.0.0 Safari/537.36", "Yandex Browser", "macOS", "desktop"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari", "iOS", "mobile"},
		{"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Mobile Safari/537.36", "Chrome", "Android", "mobile"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0", "Firefox", "Windows", "desktop"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36 Edg/127.0.0.0", "Edge", "Windows", "desktop"},
		{"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", "Safari", "iOS", "tablet"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 Barcelona 350.0.0.0", "Threads (встроенный)", "iOS", "mobile"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148", "Safari (WebView)", "iOS", "mobile"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8 Build/AD1A.240418.003; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/124.0.0.0 Mobile Safari/537.36 Telegram-Android/11.0", "Telegram (встроенный)", "Android", "mobile"},
	}
	for _, c := range cases {
		b, o, d := ParseUA(c.ua)
		if b != c.browser || o != c.os || d != c.device {
			t.Errorf("%s: got %s/%s/%s want %s/%s/%s", c.ua, b, o, d, c.browser, c.os, c.device)
		}
	}
}

func TestIsBot(t *testing.T) {
	if !IsBot("Mozilla/5.0 (compatible; YandexBot/3.0; +http://yandex.com/bots)") {
		t.Error("YandexBot not detected")
	}
	if IsBot("Mozilla/5.0 (Windows NT 10.0) Chrome/127.0 Safari/537.36") {
		t.Error("real browser flagged as bot")
	}
}

func TestSource(t *testing.T) {
	for in, want := range map[string]string{"": "(прямые)", "www.yandex.ru": "Яндекс", "t.me": "Telegram", "habr.com": "Хабр", "shop.example.com": "shop.example.com", "yabs.yandex.ru": "Яндекс"} {
		if got := Source(in); got != want {
			t.Errorf("Source(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRefDomainOwnHost(t *testing.T) {
	if RefDomain("https://cutover.ru/yandex-cloud.html", "cutover.ru") != "" {
		t.Error("own host should be treated as direct")
	}
	if RefDomain("https://t.me/somechannel", "cutover.ru") != "t.me" {
		t.Error("external referrer lost")
	}
}

func TestSessions(t *testing.T) {
	s := NewSessions(30 * time.Minute)
	defer s.Close()
	now := time.Now()
	id1, new1 := s.Touch(42, now)
	id2, new2 := s.Touch(42, now.Add(10*time.Minute))
	id3, new3 := s.Touch(42, now.Add(50*time.Minute))
	if !new1 || new2 || !new3 {
		t.Errorf("new flags: %v %v %v", new1, new2, new3)
	}
	if id1 != id2 || id2 == id3 {
		t.Errorf("session ids: %d %d %d", id1, id2, id3)
	}
}

func TestVisitorIDRotatesDaily(t *testing.T) {
	h := &Handler{secret: "x"}
	d1 := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	a := h.visitorID(1, "1.2.3.4", "ua", d1)
	b := h.visitorID(1, "1.2.3.4", "ua", d1.Add(time.Hour))
	c := h.visitorID(1, "1.2.3.4", "ua", d1.Add(24*time.Hour))
	if a != b || a == c {
		t.Errorf("visitor id rotation wrong: %d %d %d", a, b, c)
	}
}

func TestHeatmapToken(t *testing.T) {
	now := time.Now()
	tok := SignHeatmapToken("s", 1, now.Add(time.Hour).Unix())
	if !VerifyHeatmapToken("s", 1, tok, now) {
		t.Error("valid token rejected")
	}
	if VerifyHeatmapToken("s", 2, tok, now) {
		t.Error("token for another site accepted")
	}
	if VerifyHeatmapToken("other", 1, tok, now) {
		t.Error("token with other secret accepted")
	}
	if VerifyHeatmapToken("s", 1, tok, now.Add(2*time.Hour)) {
		t.Error("expired token accepted")
	}
	if VerifyHeatmapToken("s", 1, "garbage", now) {
		t.Error("garbage accepted")
	}
}

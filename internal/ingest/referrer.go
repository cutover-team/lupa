package ingest

import (
	"net/url"
	"strings"
)

// Source превращает домен реферера в понятное имя источника.
func Source(refDomain string) string {
	d := strings.TrimPrefix(strings.ToLower(refDomain), "www.")
	if d == "" {
		return "(прямые)"
	}
	table := map[string]string{
		"yandex.ru": "Яндекс", "ya.ru": "Яндекс", "dzen.ru": "Дзен", "google.com": "Google", "google.ru": "Google",
		"bing.com": "Bing", "duckduckgo.com": "DuckDuckGo", "mail.ru": "Mail.ru", "go.mail.ru": "Mail.ru",
		"vk.com": "VK", "vk.ru": "VK", "t.me": "Telegram", "telegram.me": "Telegram", "web.telegram.org": "Telegram",
		"habr.com": "Хабр", "career.habr.com": "Хабр Карьера", "vc.ru": "VC.ru", "tenchat.ru": "TenChat",
		"youtube.com": "YouTube", "rutube.ru": "RuTube", "ok.ru": "Одноклассники", "threads.net": "Threads", "threads.com": "Threads",
		"linkedin.com": "LinkedIn", "x.com": "X", "twitter.com": "X", "github.com": "GitHub",
		"instagram.com": "Instagram", "facebook.com": "Facebook", "l.facebook.com": "Facebook", "pikabu.ru": "Пикабу",
	}
	if s, ok := table[d]; ok {
		return s
	}
	for suffix, name := range map[string]string{".yandex.ru": "Яндекс", ".google.com": "Google", ".google.ru": "Google", ".mail.ru": "Mail.ru", ".vk.com": "VK", ".habr.com": "Хабр"} {
		if strings.HasSuffix(d, suffix) {
			return name
		}
	}
	return d
}

// RefDomain извлекает домен из реферера; свой домен считается прямым переходом.
func RefDomain(referrer, ownHost string) string {
	if referrer == "" {
		return ""
	}
	u, err := url.Parse(referrer)
	if err != nil || u.Host == "" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if ownHost != "" && (h == ownHost || strings.HasSuffix(h, "."+ownHost)) {
		return ""
	}
	return h
}

package ingest

import "strings"

// ParseUA — упрощённый разбор User-Agent без внешних баз: браузер, ОС, тип устройства.
func ParseUA(ua string) (browser, os, device string) {
	u := strings.ToLower(ua)
	switch {
	// встроенные браузеры приложений: у них нет Version/, и их полезно видеть отдельно
	case strings.Contains(u, "barcelona") || strings.Contains(u, "threads"):
		browser = "Threads (встроенный)"
	case strings.Contains(u, "instagram"):
		browser = "Instagram (встроенный)"
	case strings.Contains(u, "fban") || strings.Contains(u, "fbav") || strings.Contains(u, "fb_iab"):
		browser = "Facebook (встроенный)"
	case strings.Contains(u, "telegram"):
		browser = "Telegram (встроенный)"
	case strings.Contains(u, "vkclient") || strings.Contains(u, "vkandroidapp") || strings.Contains(u, "vkiphone"):
		browser = "VK (встроенный)"
	case strings.Contains(u, "yabrowser"):
		browser = "Yandex Browser"
	case strings.Contains(u, "edg/") || strings.Contains(u, "edge/"):
		browser = "Edge"
	case strings.Contains(u, "opr/") || strings.Contains(u, "opera"):
		browser = "Opera"
	case strings.Contains(u, "samsungbrowser"):
		browser = "Samsung Internet"
	case strings.Contains(u, "firefox/") || strings.Contains(u, "fxios"):
		browser = "Firefox"
	case strings.Contains(u, "chrome/") || strings.Contains(u, "crios"):
		browser = "Chrome"
	case strings.Contains(u, "safari/") && strings.Contains(u, "version/"):
		browser = "Safari"
	case strings.Contains(u, "applewebkit") && (strings.Contains(u, "iphone") || strings.Contains(u, "ipad")):
		browser = "Safari (WebView)"
	case strings.Contains(u, "; wv)"):
		browser = "Chrome (WebView)"
	default:
		browser = "Другой"
	}
	switch {
	case strings.Contains(u, "iphone") || strings.Contains(u, "ipad") || strings.Contains(u, "ipod"):
		os = "iOS"
	case strings.Contains(u, "android"):
		os = "Android"
	case strings.Contains(u, "windows"):
		os = "Windows"
	case strings.Contains(u, "mac os x") || strings.Contains(u, "macintosh"):
		os = "macOS"
	case strings.Contains(u, "cros"):
		os = "ChromeOS"
	case strings.Contains(u, "linux"):
		os = "Linux"
	default:
		os = "Другая"
	}
	switch {
	case strings.Contains(u, "ipad") || (strings.Contains(u, "android") && !strings.Contains(u, "mobile")) || strings.Contains(u, "tablet"):
		device = "tablet"
	case strings.Contains(u, "mobile") || strings.Contains(u, "iphone") || strings.Contains(u, "android"):
		device = "mobile"
	default:
		device = "desktop"
	}
	return
}

// IsBot отсекает очевидных роботов. Остальной трафик считаем живым.
func IsBot(ua string) bool {
	if ua == "" {
		return true
	}
	u := strings.ToLower(ua)
	for _, s := range []string{"bot", "crawl", "spider", "slurp", "headless", "lighthouse", "pingdom", "uptime", "monitor", "curl/", "wget/", "python-requests", "go-http-client", "facebookexternalhit", "preview"} {
		if strings.Contains(u, s) {
			return true
		}
	}
	return false
}

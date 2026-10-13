package api

import (
	"testing"
	"time"
)

func TestParsePeriod(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Moscow")
	p, err := ParsePeriod("7d", "", "", loc)
	if err != nil {
		t.Fatal(err)
	}
	if d := p.To.Sub(p.From); d != 7*24*time.Hour {
		t.Errorf("7d length = %v", d)
	}
	p, err = ParsePeriod("custom", "2026-10-01", "2026-10-31", loc)
	if err != nil {
		t.Fatal(err)
	}
	if d := p.To.Sub(p.From); d != 31*24*time.Hour {
		t.Errorf("custom length = %v", d)
	}
	if _, err := ParsePeriod("custom", "2026-10-31", "2026-10-01", loc); err == nil {
		t.Error("reversed dates must fail")
	}
	prev := p.Prev()
	if prev.To != p.From || prev.From != p.From.Add(-31*24*time.Hour) {
		t.Error("prev period wrong")
	}
}

func TestEsc(t *testing.T) {
	if got := esc(`it's \ ok`); got != `'it\'s \\ ok'` {
		t.Errorf("esc = %s", got)
	}
}

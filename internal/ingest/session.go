package ingest

import (
	"sync"
	"time"
)

// Sessions хранит активные сессии в памяти: посетитель -> (id сессии, последняя активность).
// Новая сессия начинается после 30 минут тишины. При перезапуске сервера сессии начнутся заново.
type Sessions struct {
	mu   sync.Mutex
	m    map[uint64]sessionState
	gap  time.Duration
	stop chan struct{}
}

type sessionState struct {
	id   uint64
	last time.Time
}

func NewSessions(gap time.Duration) *Sessions {
	s := &Sessions{m: make(map[uint64]sessionState), gap: gap, stop: make(chan struct{})}
	go s.cleanup()
	return s
}

// Touch возвращает id сессии для посетителя на момент now и признак новой сессии.
func (s *Sessions) Touch(visitor uint64, now time.Time) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[visitor]
	if ok && now.Sub(st.last) < s.gap {
		st.last = now
		s.m[visitor] = st
		return st.id, false
	}
	id := uint64(now.UnixNano()) ^ (visitor << 1)
	s.m[visitor] = sessionState{id: id, last: now}
	return id, true
}

func (s *Sessions) cleanup() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			cut := time.Now().Add(-s.gap)
			s.mu.Lock()
			for k, v := range s.m {
				if v.last.Before(cut) {
					delete(s.m, k)
				}
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

func (s *Sessions) Close() { close(s.stop) }

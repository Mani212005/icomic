package accounts

import (
	"sync"
	"time"
)

// Rule allows Max events per Window. Windows are fixed, matching the API admission limiter.
type Rule struct {
	Max    int
	Window time.Duration
}

type limitEntry struct {
	start time.Time
	count int
}

// Limiter is a process-local, bounded fixed-window counter. When it is full of live keys it
// fails closed rather than growing. Multiple server instances do not share counters.
type Limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	maxKeys int
	entries map[string]limitEntry
}

func NewLimiter(now func() time.Time, maxKeys int) *Limiter {
	if now == nil {
		now = time.Now
	}
	if maxKeys <= 0 {
		maxKeys = 10000
	}
	return &Limiter{now: now, maxKeys: maxKeys, entries: make(map[string]limitEntry)}
}

// Allowed reports whether another event is permitted without counting one.
func (l *Limiter) Allowed(key string, rule Rule) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if !ok || now.Sub(e.start) >= rule.Window {
		if !ok && len(l.entries) >= l.maxKeys && !l.purgeLocked(now, rule.Window) {
			return false, rule.Window
		}
		return true, 0
	}
	if e.count >= rule.Max {
		return false, e.start.Add(rule.Window).Sub(now)
	}
	return true, 0
}

// Record counts one event, for example a failed sign-in.
func (l *Limiter) Record(key string, rule Rule) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if ok && now.Sub(e.start) < rule.Window {
		e.count++
		l.entries[key] = e
		return
	}
	if !ok && len(l.entries) >= l.maxKeys && !l.purgeLocked(now, rule.Window) {
		return
	}
	l.entries[key] = limitEntry{start: now, count: 1}
}

// Take is Allowed followed by Record in one step.
func (l *Limiter) Take(key string, rule Rule) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if ok && now.Sub(e.start) < rule.Window {
		if e.count >= rule.Max {
			return false, e.start.Add(rule.Window).Sub(now)
		}
		e.count++
		l.entries[key] = e
		return true, 0
	}
	if !ok && len(l.entries) >= l.maxKeys && !l.purgeLocked(now, rule.Window) {
		return false, rule.Window
	}
	l.entries[key] = limitEntry{start: now, count: 1}
	return true, 0
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// purgeLocked drops expired entries and reports whether there is room afterwards.
func (l *Limiter) purgeLocked(now time.Time, window time.Duration) bool {
	for k, v := range l.entries {
		if now.Sub(v.start) >= window {
			delete(l.entries, k)
		}
	}
	return len(l.entries) < l.maxKeys
}

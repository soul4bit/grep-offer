package app

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateLimitRule struct {
	name     string
	method   string
	path     string
	limit    int
	window   time.Duration
	message  string
	jsonMode bool
}

type rateLimitEntry struct {
	hits     []time.Time
	lastSeen time.Time
}

type rateLimiter struct {
	mu           sync.Mutex
	rules        []rateLimitRule
	entries      map[string]*rateLimitEntry
	requestCount int
	cleanupEvery int
	staleAfter   time.Duration
}

func newRateLimiter(rules []rateLimitRule) *rateLimiter {
	if len(rules) == 0 {
		return nil
	}

	return &rateLimiter{
		rules:        append([]rateLimitRule(nil), rules...),
		entries:      make(map[string]*rateLimitEntry),
		cleanupEvery: 64,
		staleAfter:   30 * time.Minute,
	}
}

func defaultRateLimitRules() []rateLimitRule {
	return []rateLimitRule{
		{
			name:    "login",
			method:  http.MethodPost,
			path:    "/login",
			limit:   8,
			window:  time.Minute,
			message: "Слишком много попыток входа. Подожди немного и попробуй снова.",
		},
		{
			name:    "register",
			method:  http.MethodPost,
			path:    "/register",
			limit:   5,
			window:  10 * time.Minute,
			message: "Слишком много заявок на регистрацию. Подожди немного и попробуй снова.",
		},
		{
			name:    "password-forgot",
			method:  http.MethodPost,
			path:    "/password/forgot",
			limit:   5,
			window:  15 * time.Minute,
			message: "Слишком много запросов на сброс пароля. Подожди немного и попробуй снова.",
		},
		{
			name:    "telegram-webhook",
			method:  http.MethodPost,
			path:    "/telegram/webhook",
			limit:   60,
			window:  time.Minute,
			message: "rate limited",
		},
		{
			name:     "admin-image-upload",
			method:   http.MethodPost,
			path:     "/admin/uploads/images",
			limit:    12,
			window:   5 * time.Minute,
			message:  "Слишком много загрузок за короткое время. Подожди немного и попробуй снова.",
			jsonMode: true,
		},
	}
}

func (a *App) withRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a == nil || a.rateLimiter == nil {
			next.ServeHTTP(w, r)
			return
		}

		rule, ok := a.rateLimiter.match(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		ipAddress := requestClientIP(r)
		allowed, retryAfter := a.rateLimiter.allow(rule, ipAddress, time.Now().UTC())
		if allowed {
			next.ServeHTTP(w, r)
			return
		}

		retryAfterSeconds := int(math.Ceil(retryAfter.Seconds()))
		if retryAfterSeconds < 1 {
			retryAfterSeconds = 1
		}

		w.Header().Set("Retry-After", strconvItoa(retryAfterSeconds))
		if rule.jsonMode {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(struct {
				Error string `json:"error"`
			}{
				Error: rule.message,
			})
			return
		}

		http.Error(w, rule.message, http.StatusTooManyRequests)
	})
}

func (l *rateLimiter) match(r *http.Request) (rateLimitRule, bool) {
	if l == nil || r == nil {
		return rateLimitRule{}, false
	}

	path := strings.TrimSpace(r.URL.Path)
	method := strings.TrimSpace(r.Method)
	for _, rule := range l.rules {
		if rule.method == method && rule.path == path {
			return rule, true
		}
	}

	return rateLimitRule{}, false
}

func (l *rateLimiter) allow(rule rateLimitRule, clientIP string, now time.Time) (bool, time.Duration) {
	if l == nil || rule.limit <= 0 || rule.window <= 0 {
		return true, 0
	}

	if strings.TrimSpace(clientIP) == "" {
		clientIP = "unknown"
	}

	key := rule.name + ":" + clientIP

	l.mu.Lock()
	defer l.mu.Unlock()

	l.requestCount++
	if l.cleanupEvery > 0 && l.requestCount%l.cleanupEvery == 0 {
		l.cleanupStale(now)
	}

	entry := l.entries[key]
	if entry == nil {
		entry = &rateLimitEntry{}
		l.entries[key] = entry
	}

	cutoff := now.Add(-rule.window)
	filtered := entry.hits[:0]
	for _, hit := range entry.hits {
		if hit.After(cutoff) {
			filtered = append(filtered, hit)
		}
	}
	entry.hits = filtered
	entry.lastSeen = now

	if len(entry.hits) >= rule.limit {
		retryAfter := rule.window - now.Sub(entry.hits[0])
		if retryAfter < 0 {
			retryAfter = 0
		}
		return false, retryAfter
	}

	entry.hits = append(entry.hits, now)
	return true, 0
}

func (l *rateLimiter) cleanupStale(now time.Time) {
	if l == nil || l.staleAfter <= 0 {
		return
	}

	for key, entry := range l.entries {
		if entry == nil {
			delete(l.entries, key)
			continue
		}
		if len(entry.hits) == 0 && now.Sub(entry.lastSeen) > l.staleAfter {
			delete(l.entries, key)
			continue
		}
		if now.Sub(entry.lastSeen) > l.staleAfter {
			delete(l.entries, key)
		}
	}
}

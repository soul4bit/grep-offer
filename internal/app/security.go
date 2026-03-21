package app

import (
	"crypto/subtle"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

const (
	csrfCookieName    = "grep_offer_csrf"
	csrfFormFieldName = "csrf_token"
	csrfHeaderName    = "X-CSRF-Token"
	maxFormBodyBytes  = 2 << 20
)

func (a *App) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

		if requestIsSecure(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

func (a *App) withCSRFProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if r.URL.Path == "/telegram/webhook" {
			next.ServeHTTP(w, r)
			return
		}

		if !requestOriginAllowed(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			status := http.StatusBadRequest
			if strings.Contains(strings.ToLower(err.Error()), "request body too large") {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, "invalid form", status)
			return
		}

		cookie, err := r.Cookie(csrfCookieName)
		if err != nil || !validCSRFToken(cookie.Value) {
			http.Error(w, "csrf token missing", http.StatusForbidden)
			return
		}

		token := strings.TrimSpace(r.Header.Get(csrfHeaderName))
		if token == "" {
			token = strings.TrimSpace(r.PostFormValue(csrfFormFieldName))
		}
		if !validCSRFToken(token) || subtle.ConstantTimeCompare([]byte(token), []byte(cookie.Value)) != 1 {
			http.Error(w, "csrf token invalid", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *App) withCacheControl(value string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

func (a *App) ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && validCSRFToken(cookie.Value) {
		return cookie.Value
	}

	token, err := generateSessionToken()
	if err != nil {
		return ""
	}

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   requestIsSecure(r),
		MaxAge:   int((12 * time.Hour).Seconds()),
	})

	return token
}

func validCSRFToken(token string) bool {
	token = strings.TrimSpace(token)
	return len(token) >= 20 && len(token) <= 128 && !strings.ContainsAny(token, " \t\r\n")
}

func requestOriginAllowed(r *http.Request) bool {
	baseURL := requestBaseURL(r)
	if baseURL == "" {
		return false
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		return sameOrigin(origin, baseURL)
	}

	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer != "" {
		return sameOrigin(referer, baseURL)
	}

	return true
}

func sameOrigin(candidate, baseURL string) bool {
	candidate = strings.TrimSpace(candidate)
	baseURL = strings.TrimSpace(baseURL)
	if candidate == "" || baseURL == "" {
		return false
	}

	return candidate == baseURL || strings.HasPrefix(candidate, baseURL+"/")
}

func contentSecurityPolicy(nonce string) string {
	return fmt.Sprintf(
		"default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; frame-src 'none'; form-action 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self' https://fonts.gstatic.com; style-src 'self' https://fonts.googleapis.com; script-src 'self' 'nonce-%s'",
		template.HTMLEscapeString(nonce),
	)
}

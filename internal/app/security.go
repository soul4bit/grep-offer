package app

import (
	"bufio"
	"compress/gzip"
	"crypto/subtle"
	"fmt"
	"html/template"
	"io"
	"net"
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

func (a *App) withVersionedAssetCacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cacheControl := "public, max-age=3600"
		if strings.TrimSpace(r.URL.Query().Get("v")) != "" {
			cacheControl = "public, max-age=31536000, immutable"
		}

		w.Header().Set("Cache-Control", cacheControl)
		next.ServeHTTP(w, r)
	})
}

func (a *App) withCompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || strings.TrimSpace(r.Header.Get("Range")) != "" {
			next.ServeHTTP(w, r)
			return
		}

		if !strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gzw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		writer := &gzipResponseWriter{
			ResponseWriter: w,
			gzipWriter:     gzw,
		}
		defer writer.Close()

		next.ServeHTTP(writer, r)
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

type gzipResponseWriter struct {
	http.ResponseWriter
	gzipWriter *gzip.Writer
	writer     io.Writer
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if !shouldCompressResponse(status, w.Header()) {
		w.ResponseWriter.WriteHeader(status)
		return
	}

	addVaryHeader(w.Header(), "Accept-Encoding")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Del("Content-Length")
	w.writer = w.gzipWriter
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipResponseWriter) Write(payload []byte) (int, error) {
	if w.writer == nil {
		w.WriteHeader(http.StatusOK)
	}
	if w.writer == nil {
		return w.ResponseWriter.Write(payload)
	}
	return w.writer.Write(payload)
}

func (w *gzipResponseWriter) Flush() {
	if w.writer == nil {
		w.WriteHeader(http.StatusOK)
	}
	if w.writer == w.gzipWriter {
		_ = w.gzipWriter.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *gzipResponseWriter) Close() error {
	if w.writer == w.gzipWriter {
		return w.gzipWriter.Close()
	}
	return nil
}

func shouldCompressResponse(status int, header http.Header) bool {
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified {
		return false
	}
	if strings.TrimSpace(header.Get("Content-Encoding")) != "" {
		return false
	}
	return isCompressibleContentType(header.Get("Content-Type"))
}

func isCompressibleContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(contentType, "text/"):
		return true
	case strings.HasPrefix(contentType, "application/javascript"):
		return true
	case strings.HasPrefix(contentType, "application/json"):
		return true
	case strings.HasPrefix(contentType, "application/xml"):
		return true
	case strings.HasPrefix(contentType, "application/xhtml+xml"):
		return true
	case strings.HasPrefix(contentType, "image/svg+xml"):
		return true
	default:
		return false
	}
}

func addVaryHeader(header http.Header, value string) {
	current := header.Values("Vary")
	for _, entry := range current {
		for _, part := range strings.Split(entry, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

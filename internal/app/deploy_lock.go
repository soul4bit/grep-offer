package app

import (
	"net/http"
)

const deployLockMessage = "Сайт обновляется, формы и прогресс временно на паузе."

func (a *App) withDeployLock(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.deployLockActive() || requestIsReadOnly(r) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Retry-After", "30")
		http.Error(w, deployLockMessage, http.StatusServiceUnavailable)
	})
}

func requestIsReadOnly(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

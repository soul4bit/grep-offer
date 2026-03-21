package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"grep-offer/internal/store"

	"golang.org/x/crypto/bcrypt"
)

const defaultPasswordResetTTL = 2 * time.Hour

type PasswordResetCoordinator struct {
	store    *store.Store
	mailer   ConfirmationMailer
	baseURL  string
	resetTTL time.Duration
}

type PasswordResetCoordinatorConfig struct {
	Store    *store.Store
	Mailer   ConfirmationMailer
	BaseURL  string
	ResetTTL time.Duration
}

func NewPasswordResetCoordinator(cfg PasswordResetCoordinatorConfig) *PasswordResetCoordinator {
	ttl := cfg.ResetTTL
	if ttl <= 0 {
		ttl = defaultPasswordResetTTL
	}

	return &PasswordResetCoordinator{
		store:    cfg.Store,
		mailer:   cfg.Mailer,
		baseURL:  strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		resetTTL: ttl,
	}
}

func (c *PasswordResetCoordinator) Enabled() bool {
	return c != nil && c.store != nil && c.mailer != nil
}

func (c *PasswordResetCoordinator) Request(ctx context.Context, email, baseURL string) error {
	if !c.Enabled() {
		return errors.New("password reset disabled")
	}

	user, err := c.store.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil
		}
		return err
	}

	rawToken, err := generateSessionToken()
	if err != nil {
		return err
	}

	resetURL, err := c.resetURL(baseURL, rawToken)
	if err != nil {
		return err
	}

	if err := c.store.CreatePasswordResetToken(ctx, user.ID, rawToken, time.Now().UTC().Add(c.resetTTL)); err != nil {
		return err
	}

	if err := c.mailer.SendPasswordReset(ctx, user.Email, user.Username, resetURL); err != nil {
		_ = c.store.DeletePasswordResetTokensByUserID(ctx, user.ID)
		return err
	}

	return nil
}

func (c *PasswordResetCoordinator) ValidateToken(ctx context.Context, rawToken string) error {
	if !c.Enabled() {
		return errors.New("password reset disabled")
	}

	_, err := c.store.PasswordResetTokenByRawToken(ctx, rawToken)
	return err
}

func (c *PasswordResetCoordinator) Reset(ctx context.Context, rawToken, password string) (int64, error) {
	if !c.Enabled() {
		return 0, errors.New("password reset disabled")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}

	return c.store.ResetPasswordByToken(ctx, rawToken, string(passwordHash))
}

func (c *PasswordResetCoordinator) resetURL(baseURL, rawToken string) (string, error) {
	resolvedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if resolvedBaseURL == "" {
		resolvedBaseURL = c.baseURL
	}
	if resolvedBaseURL == "" {
		return "", errors.New("base URL is not configured")
	}

	return resolvedBaseURL + "/password/reset?token=" + url.QueryEscape(rawToken), nil
}

func (a *App) handleForgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	a.render(w, r, http.StatusOK, "forgot_password", ViewData{
		Notice: noticeFromRequest(r),
	})
}

func (a *App) handleForgotPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	data := ViewData{
		Form: AuthForm{Email: email},
	}

	if validationError := validatePasswordResetRequest(email); validationError != "" {
		data.Error = validationError
		a.render(w, r, http.StatusUnprocessableEntity, "forgot_password", data)
		return
	}

	if a.passwordReset == nil || !a.passwordReset.Enabled() {
		data.Error = "Сброс пароля пока не взлетел. Нужен рабочий SMTP и base URL."
		a.render(w, r, http.StatusServiceUnavailable, "forgot_password", data)
		return
	}

	if err := a.passwordReset.Request(r.Context(), email, requestBaseURL(r)); err != nil {
		a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
			Scope:      "password_reset",
			Action:     "password_reset_requested",
			TargetType: "email",
			TargetKey:  email,
			Status:     "error",
		})
		http.Error(w, "password reset request failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
		Scope:      "password_reset",
		Action:     "password_reset_requested",
		TargetType: "email",
		TargetKey:  email,
	})

	http.Redirect(w, r, "/password/forgot?notice=password-reset-sent", http.StatusSeeOther)
}

func (a *App) handlePasswordResetForm(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" || a.passwordReset == nil || !a.passwordReset.Enabled() {
		http.Redirect(w, r, "/login?notice=password-reset-invalid", http.StatusSeeOther)
		return
	}

	if err := a.passwordReset.ValidateToken(r.Context(), token); err != nil {
		if errors.Is(err, store.ErrPasswordResetTokenNotFound) {
			http.Redirect(w, r, "/login?notice=password-reset-invalid", http.StatusSeeOther)
			return
		}

		http.Error(w, "password reset unavailable", http.StatusInternalServerError)
		return
	}

	a.render(w, r, http.StatusOK, "reset_password", ViewData{
		Notice:             noticeFromRequest(r),
		PasswordResetToken: token,
	})
}

func (a *App) handlePasswordResetSubmit(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	token := strings.TrimSpace(r.FormValue("token"))
	password := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")

	data := ViewData{
		PasswordResetToken: token,
	}

	if validationError := validatePasswordReset(password, confirmPassword); validationError != "" {
		data.Error = validationError
		a.render(w, r, http.StatusUnprocessableEntity, "reset_password", data)
		return
	}

	if token == "" || a.passwordReset == nil || !a.passwordReset.Enabled() {
		http.Redirect(w, r, "/login?notice=password-reset-invalid", http.StatusSeeOther)
		return
	}

	userID, err := a.passwordReset.Reset(r.Context(), token, password)
	if err != nil {
		if errors.Is(err, store.ErrPasswordResetTokenNotFound) {
			a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
				Scope:      "password_reset",
				Action:     "password_reset_completed",
				TargetType: "reset_token",
				TargetKey:  truncateText(token, 24),
				Status:     "warn",
				Details: map[string]string{
					"reason": "invalid_token",
				},
			})
			http.Redirect(w, r, "/login?notice=password-reset-invalid", http.StatusSeeOther)
			return
		}

		a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
			Scope:      "password_reset",
			Action:     "password_reset_completed",
			TargetType: "reset_token",
			TargetKey:  truncateText(token, 24),
			Status:     "error",
		})
		http.Error(w, "password reset failed", http.StatusInternalServerError)
		return
	}

	if err := a.issueSession(r.Context(), w, r, userID); err != nil {
		http.Error(w, "create session failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
		Scope:      "password_reset",
		Action:     "password_reset_completed",
		TargetType: "user",
		TargetKey:  strconv.FormatInt(userID, 10),
	})

	http.Redirect(w, r, "/dashboard?notice=password-reset-complete", http.StatusSeeOther)
}

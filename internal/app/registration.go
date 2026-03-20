package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"grep-offer/internal/store"
)

const defaultRegistrationConfirmationTTL = 72 * time.Hour

type ConfirmationMailer interface {
	SendRegistrationConfirmation(ctx context.Context, email, username, confirmURL string) error
}

type AdminApprovalNotifier interface {
	SendRegistrationRequest(ctx context.Context, requestID int64, username, email string, createdAt time.Time) error
	AnswerCallbackQuery(ctx context.Context, callbackID, text string) error
	MarkRegistrationApproved(ctx context.Context, chatID int64, messageID int, username, email string) error
	MarkRegistrationRejected(ctx context.Context, chatID int64, messageID int, username, email string) error
}

type RegistrationState string

const (
	RegistrationStateNone                 RegistrationState = ""
	RegistrationStateAwaitingApproval     RegistrationState = "awaiting_approval"
	RegistrationStateAwaitingConfirmation RegistrationState = "awaiting_confirmation"
)

type RegistrationCoordinator struct {
	store           *store.Store
	mailer          ConfirmationMailer
	notifier        AdminApprovalNotifier
	baseURL         string
	confirmationTTL time.Duration
}

type RegistrationCoordinatorConfig struct {
	Store           *store.Store
	Mailer          ConfirmationMailer
	Notifier        AdminApprovalNotifier
	BaseURL         string
	ConfirmationTTL time.Duration
}

func NewRegistrationCoordinator(cfg RegistrationCoordinatorConfig) *RegistrationCoordinator {
	ttl := cfg.ConfirmationTTL
	if ttl <= 0 {
		ttl = defaultRegistrationConfirmationTTL
	}

	return &RegistrationCoordinator{
		store:           cfg.Store,
		mailer:          cfg.Mailer,
		notifier:        cfg.Notifier,
		baseURL:         strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		confirmationTTL: ttl,
	}
}

func (c *RegistrationCoordinator) Enabled() bool {
	return c != nil && c.store != nil && c.mailer != nil && c.notifier != nil
}

func (c *RegistrationCoordinator) Submit(ctx context.Context, username, email, passwordHash string) error {
	if !c.Enabled() {
		return errors.New("registration approval workflow disabled")
	}

	if _, err := c.store.UserByEmail(ctx, email); err == nil {
		return store.ErrEmailTaken
	} else if err != nil && !errors.Is(err, store.ErrUserNotFound) {
		return err
	}

	request, err := c.store.CreateRegistrationRequest(ctx, username, email, passwordHash)
	if err != nil {
		return err
	}

	if err := c.notifier.SendRegistrationRequest(ctx, request.ID, request.Username, request.Email, request.CreatedAt); err != nil {
		_ = c.store.DeleteRegistrationRequest(ctx, request.ID)
		return err
	}

	return nil
}

func (c *RegistrationCoordinator) PendingStateByEmail(ctx context.Context, email string) (RegistrationState, error) {
	request, err := c.store.RegistrationRequestByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrRegistrationNotFound) {
			return RegistrationStateNone, nil
		}
		return RegistrationStateNone, err
	}

	if request.AwaitingConfirmation() {
		return RegistrationStateAwaitingConfirmation, nil
	}

	return RegistrationStateAwaitingApproval, nil
}

func (c *RegistrationCoordinator) Approve(ctx context.Context, requestID int64, baseURL string) (*store.RegistrationRequest, error) {
	if !c.Enabled() {
		return nil, errors.New("registration approval workflow disabled")
	}

	rawToken, err := generateSessionToken()
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().UTC().Add(c.confirmationTTL)
	request, err := c.store.ApproveRegistrationRequest(ctx, requestID, rawToken, expiresAt)
	if err != nil {
		return nil, err
	}

	confirmURL, err := c.confirmationURL(baseURL, rawToken)
	if err != nil {
		_ = c.store.ResetRegistrationApproval(ctx, requestID)
		return nil, err
	}

	if err := c.mailer.SendRegistrationConfirmation(ctx, request.Email, request.Username, confirmURL); err != nil {
		_ = c.store.ResetRegistrationApproval(ctx, requestID)
		return nil, err
	}

	return request, nil
}

func (c *RegistrationCoordinator) Reject(ctx context.Context, requestID int64) (*store.RegistrationRequest, error) {
	request, err := c.store.RegistrationRequestByID(ctx, requestID)
	if err != nil {
		return nil, err
	}

	if err := c.store.DeleteRegistrationRequest(ctx, requestID); err != nil {
		return nil, err
	}

	return request, nil
}

func (c *RegistrationCoordinator) Confirm(ctx context.Context, rawToken string) (*store.User, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("registration confirmation unavailable")
	}
	return c.store.ConsumeRegistrationRequest(ctx, rawToken)
}

func (c *RegistrationCoordinator) confirmationURL(baseURL, rawToken string) (string, error) {
	resolvedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if resolvedBaseURL == "" {
		resolvedBaseURL = c.baseURL
	}
	if resolvedBaseURL == "" {
		return "", errors.New("base URL is not configured")
	}

	return resolvedBaseURL + "/register/confirm?token=" + url.QueryEscape(rawToken), nil
}

func (a *App) handleRegisterConfirm(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" || a.registration == nil {
		http.Redirect(w, r, "/login?notice=confirmation-invalid", http.StatusSeeOther)
		return
	}

	user, err := a.registration.Confirm(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrRegistrationTokenNotFound) {
			http.Redirect(w, r, "/login?notice=confirmation-invalid", http.StatusSeeOther)
			return
		}

		http.Error(w, "confirm registration failed", http.StatusInternalServerError)
		return
	}

	if err := a.issueSession(r.Context(), w, r, user.ID); err != nil {
		http.Error(w, "create session failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/dashboard?notice=email-confirmed", http.StatusSeeOther)
}

func (a *App) handleTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	if a.registration == nil || !a.registration.Enabled() || a.telegramWebhookSecret == "" {
		http.Error(w, "telegram webhook disabled", http.StatusServiceUnavailable)
		return
	}

	if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != a.telegramWebhookSecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var update telegramUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "invalid telegram update", http.StatusBadRequest)
		return
	}

	if update.CallbackQuery == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	callback := update.CallbackQuery
	action, requestID, err := parseTelegramAction(callback.Data)
	if err != nil {
		_ = a.registration.notifier.AnswerCallbackQuery(r.Context(), callback.ID, "Непонятная команда.")
		w.WriteHeader(http.StatusOK)
		return
	}

	switch action {
	case "approve":
		request, approveErr := a.registration.Approve(r.Context(), requestID, requestBaseURL(r))
		if approveErr != nil {
			log.Printf("approve registration request %d: %v", requestID, approveErr)
			_ = a.registration.notifier.AnswerCallbackQuery(r.Context(), callback.ID, telegramCallbackErrorText(approveErr))
			w.WriteHeader(http.StatusOK)
			return
		}

		_ = a.registration.notifier.AnswerCallbackQuery(r.Context(), callback.ID, "Письмо с подтверждением отправлено.")
		if callback.Message != nil {
			if err := a.registration.notifier.MarkRegistrationApproved(
				r.Context(),
				callback.Message.Chat.ID,
				callback.Message.MessageID,
				request.Username,
				request.Email,
			); err != nil {
				log.Printf("mark registration approved: %v", err)
			}
		}
	case "reject":
		request, rejectErr := a.registration.Reject(r.Context(), requestID)
		if rejectErr != nil {
			log.Printf("reject registration request %d: %v", requestID, rejectErr)
			_ = a.registration.notifier.AnswerCallbackQuery(r.Context(), callback.ID, telegramCallbackErrorText(rejectErr))
			w.WriteHeader(http.StatusOK)
			return
		}

		_ = a.registration.notifier.AnswerCallbackQuery(r.Context(), callback.ID, "Заявка отклонена.")
		if callback.Message != nil {
			if err := a.registration.notifier.MarkRegistrationRejected(
				r.Context(),
				callback.Message.Chat.ID,
				callback.Message.MessageID,
				request.Username,
				request.Email,
			); err != nil {
				log.Printf("mark registration rejected: %v", err)
			}
		}
	default:
		_ = a.registration.notifier.AnswerCallbackQuery(r.Context(), callback.ID, "Неподдерживаемое действие.")
	}

	w.WriteHeader(http.StatusOK)
}

func (a *App) loginErrorForEmail(ctx context.Context, email string) string {
	if a.registration == nil {
		return "Почта или пароль не совпали. grep ничего не нашел."
	}

	state, err := a.registration.PendingStateByEmail(ctx, email)
	if err != nil || state == RegistrationStateNone {
		return "Почта или пароль не совпали. grep ничего не нашел."
	}

	if state == RegistrationStateAwaitingApproval {
		return "Заявка уже отправлена, но пока ждет апрува в Telegram. Как только ее одобрят, на почту придет письмо."
	}

	return "Апрув уже есть, но почта еще не подтверждена. Проверь письмо и открой ссылку подтверждения."
}

func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}

	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if requestIsSecure(r) {
		scheme = "https"
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}

	if comma := strings.Index(host, ","); comma >= 0 {
		host = strings.TrimSpace(host[:comma])
	}

	return scheme + "://" + host
}

func telegramCallbackErrorText(err error) string {
	switch {
	case errors.Is(err, store.ErrRegistrationNotFound):
		return "Заявка уже исчезла."
	case errors.Is(err, store.ErrRegistrationAlreadyApproved):
		return "Эту заявку уже апрувнули."
	case errors.Is(err, store.ErrEmailTaken):
		return "Такой email уже занят."
	case strings.Contains(strings.ToLower(err.Error()), "base url"):
		return "Не настроен APP_BASE_URL."
	case strings.Contains(strings.ToLower(err.Error()), "smtp"):
		return "Письмо не отправилось. Проверь SMTP."
	default:
		return "Что-то сломалось на сервере."
	}
}

func parseTelegramAction(data string) (string, int64, error) {
	action, rawID, ok := strings.Cut(strings.TrimSpace(data), ":")
	if !ok {
		return "", 0, errors.New("invalid callback data")
	}

	requestID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		return "", 0, err
	}

	return action, requestID, nil
}

type telegramUpdate struct {
	CallbackQuery *telegramCallbackQuery `json:"callback_query"`
}

type telegramCallbackQuery struct {
	ID      string           `json:"id"`
	Data    string           `json:"data"`
	Message *telegramMessage `json:"message"`
	From    *telegramUser    `json:"from"`
}

type telegramMessage struct {
	MessageID int          `json:"message_id"`
	Chat      telegramChat `json:"chat"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

type telegramUser struct {
	ID int64 `json:"id"`
}

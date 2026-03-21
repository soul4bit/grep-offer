package app

import (
	"context"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"grep-offer/internal/store"
)

func (a *App) writeAuditLog(ctx context.Context, r *http.Request, actor *store.User, input store.AuditLogInput) {
	if a == nil || a.store == nil {
		return
	}

	if actor != nil && input.ActorUserID == nil {
		input.ActorUserID = &actor.ID
	}
	input.IPAddress = requestClientIP(r)
	input.UserAgent = truncateText(strings.TrimSpace(r.UserAgent()), 220)

	if err := a.store.CreateAuditLog(ctx, input); err != nil {
		log.Printf("write audit log: %v", err)
	}
}

func (a *App) loadAdminAuditLogs(ctx context.Context) ([]AdminAuditLogRow, error) {
	entries, err := a.store.ListAuditLogs(ctx, 120)
	if err != nil {
		return nil, err
	}

	rows := make([]AdminAuditLogRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, AdminAuditLogRow{
			Scope:        humanizeAuditScope(entry.Scope),
			Action:       humanizeAuditAction(entry.Action),
			ActorLabel:   a.auditActorLabel(ctx, entry),
			TargetLabel:  auditTargetLabel(entry),
			Status:       strings.ToUpper(entry.Status),
			StatusTone:   auditStatusTone(entry.Status),
			Details:      auditDetailsSummary(entry.Details),
			IPAddress:    fallbackText(entry.IPAddress, "unknown ip"),
			UserAgent:    fallbackText(entry.UserAgent, "unknown ua"),
			CreatedLabel: entry.CreatedAt.In(time.FixedZone("MSK", 3*60*60)).Format("02.01.2006 15:04:05"),
		})
	}

	return rows, nil
}

func (a *App) auditActorLabel(ctx context.Context, entry store.AuditLogEntry) string {
	if entry.ActorUserID == nil {
		return "system"
	}

	user, err := a.store.UserByID(ctx, *entry.ActorUserID)
	if err != nil {
		return "user #" + strconv.FormatInt(*entry.ActorUserID, 10)
	}

	return user.Username + " · " + user.Email
}

func requestClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}

	return strings.TrimSpace(r.RemoteAddr)
}

func humanizeAuditScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case "admin":
		return "admin"
	case "auth":
		return "auth"
	case "registration":
		return "registration"
	case "password_reset":
		return "password reset"
	default:
		return fallbackText(scope, "system")
	}
}

func humanizeAuditAction(action string) string {
	switch strings.TrimSpace(action) {
	case "login_succeeded":
		return "успешный вход"
	case "login_failed":
		return "неуспешный вход"
	case "logout":
		return "выход"
	case "registration_submitted":
		return "заявка на регистрацию"
	case "registration_confirmed":
		return "подтверждение почты"
	case "registration_approved":
		return "апрув регистрации"
	case "registration_rejected":
		return "отклонение регистрации"
	case "password_reset_requested":
		return "запрос на сброс"
	case "password_reset_completed":
		return "пароль сброшен"
	case "user_admin_changed":
		return "смена admin-флага"
	case "user_ban_changed":
		return "смена ban-флага"
	case "user_deleted":
		return "удаление пользователя"
	case "article_saved":
		return "сохранение урока"
	case "test_question_created":
		return "создание test-вопроса"
	case "test_question_deleted":
		return "удаление test-вопроса"
	default:
		return strings.ReplaceAll(fallbackText(action, "event"), "_", " ")
	}
}

func auditTargetLabel(entry store.AuditLogEntry) string {
	target := strings.TrimSpace(entry.TargetKey)
	if target == "" {
		return fallbackText(entry.TargetType, "—")
	}

	if entry.TargetType == "" {
		return target
	}

	return entry.TargetType + " · " + target
}

func auditDetailsSummary(details map[string]string) string {
	if len(details) == 0 {
		return "—"
	}

	keys := make([]string, 0, len(details))
	for key := range details {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(details[key])
		if value == "" {
			continue
		}
		parts = append(parts, key+": "+truncateText(value, 120))
	}
	if len(parts) == 0 {
		return "—"
	}

	return strings.Join(parts, " · ")
}

func auditStatusTone(status string) string {
	switch strings.TrimSpace(status) {
	case "warn":
		return "warn"
	case "error":
		return "error"
	default:
		return "ok"
	}
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}

	return string([]rune(value)[:limit]) + "…"
}

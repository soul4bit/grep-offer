package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

func (s *Store) CreateAuditLog(ctx context.Context, input AuditLogInput) error {
	detailsJSON, err := json.Marshal(normalizeAuditDetails(input.Details))
	if err != nil {
		return err
	}

	var actorUserID any
	if input.ActorUserID != nil && *input.ActorUserID > 0 {
		actorUserID = *input.ActorUserID
	}

	_, err = s.db.ExecContext(
		ctx,
		s.bind(`INSERT INTO audit_logs (
			actor_user_id, scope, action, target_type, target_key, status, ip_address, user_agent, details_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		actorUserID,
		strings.TrimSpace(input.Scope),
		strings.TrimSpace(input.Action),
		strings.TrimSpace(input.TargetType),
		strings.TrimSpace(input.TargetKey),
		normalizeAuditStatus(input.Status),
		strings.TrimSpace(input.IPAddress),
		strings.TrimSpace(input.UserAgent),
		string(detailsJSON),
		time.Now().UTC().Unix(),
	)
	return err
}

func (s *Store) ListAuditLogs(ctx context.Context, limit int) ([]AuditLogEntry, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.QueryContext(
		ctx,
		s.bind(`SELECT id, actor_user_id, scope, action, target_type, target_key, status, ip_address, user_agent, details_json, created_at
		FROM audit_logs
		ORDER BY created_at DESC, id DESC
		LIMIT ?`),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]AuditLogEntry, 0, limit)
	for rows.Next() {
		var (
			entry       AuditLogEntry
			actorUserID sql.NullInt64
			detailsJSON string
			createdAt   int64
		)

		if err := rows.Scan(
			&entry.ID,
			&actorUserID,
			&entry.Scope,
			&entry.Action,
			&entry.TargetType,
			&entry.TargetKey,
			&entry.Status,
			&entry.IPAddress,
			&entry.UserAgent,
			&detailsJSON,
			&createdAt,
		); err != nil {
			return nil, err
		}

		if actorUserID.Valid {
			value := actorUserID.Int64
			entry.ActorUserID = &value
		}
		entry.CreatedAt = time.Unix(createdAt, 0).UTC()
		entry.Details = normalizeAuditDetails(nil)
		if strings.TrimSpace(detailsJSON) != "" {
			_ = json.Unmarshal([]byte(detailsJSON), &entry.Details)
		}

		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

func normalizeAuditDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return map[string]string{}
	}

	normalized := make(map[string]string, len(details))
	for key, value := range details {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	if len(normalized) == 0 {
		return map[string]string{}
	}

	return normalized
}

func normalizeAuditStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "warn":
		return "warn"
	case "error":
		return "error"
	default:
		return "ok"
	}
}

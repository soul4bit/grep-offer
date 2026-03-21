package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PasswordResetToken struct {
	UserID    int64
	ExpiresAt time.Time
	CreatedAt time.Time
}

func (s *Store) CreatePasswordResetToken(ctx context.Context, userID int64, rawToken string, expiresAt time.Time) error {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM password_reset_tokens WHERE user_id = ?`), userID); err != nil {
		return err
	}

	if _, err = tx.ExecContext(
		ctx,
		s.bind(`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`),
		hashToken(rawToken),
		userID,
		expiresAt.UTC().Unix(),
		now.Unix(),
	); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (s *Store) PasswordResetTokenByRawToken(ctx context.Context, rawToken string) (*PasswordResetToken, error) {
	row := s.db.QueryRowContext(
		ctx,
		s.bind(`SELECT user_id, expires_at, created_at
		FROM password_reset_tokens
		WHERE token_hash = ? AND expires_at > ?`),
		hashToken(rawToken),
		time.Now().UTC().Unix(),
	)

	var (
		resetToken    PasswordResetToken
		expiresAtUnix int64
		createdAtUnix int64
	)

	if err := row.Scan(&resetToken.UserID, &expiresAtUnix, &createdAtUnix); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPasswordResetTokenNotFound
		}
		return nil, err
	}

	resetToken.ExpiresAt = time.Unix(expiresAtUnix, 0).UTC()
	resetToken.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	return &resetToken, nil
}

func (s *Store) ResetPasswordByToken(ctx context.Context, rawToken, passwordHash string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var userID int64
	if err = tx.QueryRowContext(
		ctx,
		s.bind(`SELECT user_id
		FROM password_reset_tokens
		WHERE token_hash = ? AND expires_at > ?`),
		hashToken(rawToken),
		time.Now().UTC().Unix(),
	).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrPasswordResetTokenNotFound
		}
		return 0, err
	}

	result, err := tx.ExecContext(ctx, s.bind(`UPDATE users SET password_hash = ? WHERE id = ?`), passwordHash, userID)
	if err != nil {
		return 0, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if rowsAffected == 0 {
		return 0, ErrUserNotFound
	}

	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM password_reset_tokens WHERE user_id = ?`), userID); err != nil {
		return 0, err
	}

	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM sessions WHERE user_id = ?`), userID); err != nil {
		return 0, err
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}

	return userID, nil
}

func (s *Store) DeleteExpiredPasswordResetTokens(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM password_reset_tokens WHERE expires_at <= ?`), time.Now().UTC().Unix())
	return err
}

func (s *Store) DeletePasswordResetTokensByUserID(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM password_reset_tokens WHERE user_id = ?`), userID)
	return err
}

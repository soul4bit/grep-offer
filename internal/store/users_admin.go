package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) UserByID(ctx context.Context, userID int64) (*User, error) {
	row := s.db.QueryRowContext(
		ctx,
		s.bind(`SELECT id, username, email, password_hash, is_admin, is_banned, created_at
		FROM users
		WHERE id = ?`),
		userID,
	)

	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}

	return user, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, username, email, password_hash, is_admin, is_banned, created_at
		FROM users
		ORDER BY created_at DESC, id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0, 32)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *user)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

func (s *Store) SetUserAdmin(ctx context.Context, userID int64, isAdmin bool) error {
	_, err := s.db.ExecContext(
		ctx,
		s.bind(`UPDATE users SET is_admin = ? WHERE id = ?`),
		boolToInt(isAdmin),
		userID,
	)
	return err
}

func (s *Store) SetUserBanned(ctx context.Context, userID int64, isBanned bool) error {
	_, err := s.db.ExecContext(
		ctx,
		s.bind(`UPDATE users SET is_banned = ? WHERE id = ?`),
		boolToInt(isBanned),
		userID,
	)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM users WHERE id = ?`), userID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrUserNotFound
	}

	return nil
}

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrEmailTaken                  = errors.New("email already taken")
	ErrSessionNotFound             = errors.New("session not found")
	ErrUserNotFound                = errors.New("user not found")
	ErrRegistrationPending         = errors.New("registration pending")
	ErrRegistrationNotFound        = errors.New("registration not found")
	ErrRegistrationAlreadyApproved = errors.New("registration already approved")
	ErrRegistrationTokenNotFound   = errors.New("registration token not found")
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

type scanner interface {
	Scan(dest ...any) error
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Init(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA journal_mode = WAL;`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);`,
		`CREATE TABLE IF NOT EXISTS registration_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			verification_token_hash TEXT,
			verification_expires_at INTEGER,
			approved_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_registration_requests_email ON registration_requests(email);`,
		`CREATE INDEX IF NOT EXISTS idx_registration_requests_verification_token_hash ON registration_requests(verification_token_hash);`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("exec statement: %w", err)
		}
	}

	return nil
}

func (s *Store) CreateUser(ctx context.Context, username, email, passwordHash string) (*User, error) {
	now := time.Now().UTC()
	username = strings.TrimSpace(username)
	email = normalizeEmail(email)

	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO users (username, email, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		username,
		email,
		passwordHash,
		now.Unix(),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrEmailTaken
		}
		return nil, err
	}

	userID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &User{
		ID:           userID,
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
		CreatedAt:    now,
	}, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, username, email, password_hash, created_at FROM users WHERE email = ?`,
		normalizeEmail(email),
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

func (s *Store) UserBySession(ctx context.Context, rawToken string) (*User, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT u.id, u.username, u.email, u.password_hash, u.created_at
		FROM sessions s
		INNER JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		hashToken(rawToken),
		time.Now().UTC().Unix(),
	)

	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}

	return user, nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64, rawToken string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(rawToken),
		userID,
		expiresAt.UTC().Unix(),
		time.Now().UTC().Unix(),
	)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, rawToken string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(rawToken))
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return ErrSessionNotFound
	}

	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC().Unix())
	return err
}

func scanUser(row scanner) (*User, error) {
	var (
		user      User
		createdAt int64
	)

	if err := row.Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &createdAt); err != nil {
		return nil, err
	}

	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &user, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func hashToken(rawToken string) string {
	hash := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(hash[:])
}

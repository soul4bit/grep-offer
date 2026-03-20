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
	ErrUsernameTaken               = errors.New("username already taken")
	ErrUserBanned                  = errors.New("user banned")
	ErrSessionNotFound             = errors.New("session not found")
	ErrUserNotFound                = errors.New("user not found")
	ErrRegistrationPending         = errors.New("registration pending")
	ErrRegistrationNotFound        = errors.New("registration not found")
	ErrRegistrationAlreadyApproved = errors.New("registration already approved")
	ErrRegistrationTokenNotFound   = errors.New("registration token not found")
	ErrPasswordResetTokenNotFound  = errors.New("password reset token not found")
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	IsAdmin      bool
	IsBanned     bool
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
			is_admin INTEGER NOT NULL DEFAULT 0,
			is_banned INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);`,
		`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE users ADD COLUMN is_banned INTEGER NOT NULL DEFAULT 0;`,
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
		`CREATE TABLE IF NOT EXISTS password_reset_tokens (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_user_id ON password_reset_tokens(user_id);`,
		`CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_expires_at ON password_reset_tokens(expires_at);`,
		`CREATE TABLE IF NOT EXISTS user_roadmap_progress (
			user_id INTEGER NOT NULL,
			checkpoint_key TEXT NOT NULL,
			done INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (user_id, checkpoint_key),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_roadmap_progress_user_id ON user_roadmap_progress(user_id);`,
		`CREATE TABLE IF NOT EXISTS user_lesson_progress (
			user_id INTEGER NOT NULL,
			lesson_slug TEXT NOT NULL,
			done INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (user_id, lesson_slug),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_lesson_progress_user_id ON user_lesson_progress(user_id);`,
		`CREATE TABLE IF NOT EXISTS lesson_test_questions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			lesson_slug TEXT NOT NULL,
			prompt TEXT NOT NULL,
			options_json TEXT NOT NULL,
			correct_option INTEGER NOT NULL,
			explanation TEXT NOT NULL DEFAULT '',
			order_index INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_lesson_test_questions_lesson_slug ON lesson_test_questions(lesson_slug, order_index, id);`,
		`CREATE TABLE IF NOT EXISTS user_lesson_test_results (
			user_id INTEGER NOT NULL,
			lesson_slug TEXT NOT NULL,
			attempts_count INTEGER NOT NULL DEFAULT 0,
			last_wrong_answers INTEGER NOT NULL DEFAULT 0,
			passed INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (user_id, lesson_slug),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_lesson_test_results_user_id ON user_lesson_test_results(user_id);`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			if isIgnorableMigrationError(err) {
				continue
			}
			return fmt.Errorf("exec statement: %w", err)
		}
	}

	return nil
}

func (s *Store) CreateUser(ctx context.Context, username, email, passwordHash string) (*User, error) {
	now := time.Now().UTC()
	username = strings.TrimSpace(username)
	email = normalizeEmail(email)

	if _, err := s.UserByUsername(ctx, username); err == nil {
		return nil, ErrUsernameTaken
	} else if err != nil && !errors.Is(err, ErrUserNotFound) {
		return nil, err
	}

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
		`SELECT id, username, email, password_hash, is_admin, is_banned, created_at FROM users WHERE email = ?`,
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

func (s *Store) UserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, username, email, password_hash, is_admin, is_banned, created_at FROM users WHERE lower(username) = ?`,
		normalizeUsername(username),
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
		`SELECT u.id, u.username, u.email, u.password_hash, u.is_admin, u.is_banned, u.created_at
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

func (s *Store) DeleteSessionsByUserID(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC().Unix())
	return err
}

func scanUser(row scanner) (*User, error) {
	var (
		user      User
		isAdmin   int
		isBanned  int
		createdAt int64
	)

	if err := row.Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &isAdmin, &isBanned, &createdAt); err != nil {
		return nil, err
	}

	user.IsAdmin = isAdmin != 0
	user.IsBanned = isBanned != 0
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &user, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func hashToken(rawToken string) string {
	hash := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(hash[:])
}

func isIgnorableMigrationError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate column name")
}

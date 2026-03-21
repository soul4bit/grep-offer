package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"grep-offer/internal/store"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type userRow struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	IsAdmin      int
	IsBanned     int
	CreatedAt    int64
}

type sessionRow struct {
	TokenHash string
	UserID    int64
	ExpiresAt int64
	CreatedAt int64
}

type registrationRequestRow struct {
	ID                    int64
	Username              string
	Email                 string
	PasswordHash          string
	VerificationTokenHash sql.NullString
	VerificationExpiresAt sql.NullInt64
	ApprovedAt            sql.NullInt64
	CreatedAt             int64
	UpdatedAt             int64
}

type passwordResetTokenRow struct {
	TokenHash string
	UserID    int64
	ExpiresAt int64
	CreatedAt int64
}

type roadmapProgressRow struct {
	UserID        int64
	CheckpointKey string
	Done          int
	UpdatedAt     int64
}

type lessonProgressRow struct {
	UserID     int64
	LessonSlug string
	Done       int
	UpdatedAt  int64
}

type lessonTestQuestionRow struct {
	ID            int64
	LessonSlug    string
	Prompt        string
	OptionsJSON   string
	CorrectOption int
	Explanation   string
	OrderIndex    int
	CreatedAt     int64
}

type lessonTestResultRow struct {
	UserID           int64
	LessonSlug       string
	AttemptsCount    int
	LastWrongAnswers int
	Passed           int
	UpdatedAt        int64
}

func main() {
	sqlitePathFlag := flag.String("sqlite", envOrDefault("SQLITE_PATH", filepath.Join("data", "grep-offer.db")), "path to source SQLite database")
	postgresURLFlag := flag.String("postgres", strings.TrimSpace(os.Getenv("DATABASE_URL")), "target PostgreSQL DATABASE_URL")
	forceFlag := flag.Bool("force", false, "allow migration into a non-empty PostgreSQL database")
	flag.Parse()

	if strings.TrimSpace(*postgresURLFlag) == "" {
		log.Fatal("postgres DATABASE_URL is required")
	}

	sqlitePath := filepath.Clean(strings.TrimSpace(*sqlitePathFlag))
	if _, err := os.Stat(sqlitePath); err != nil {
		log.Fatalf("stat sqlite source %s: %v", sqlitePath, err)
	}

	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		log.Fatalf("open sqlite source: %v", err)
	}
	defer sourceDB.Close()
	sourceDB.SetMaxOpenConns(1)

	targetDB, err := sql.Open("pgx", strings.TrimSpace(*postgresURLFlag))
	if err != nil {
		log.Fatalf("open postgres target: %v", err)
	}
	defer targetDB.Close()
	targetDB.SetMaxOpenConns(5)
	targetDB.SetMaxIdleConns(2)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := sourceDB.PingContext(ctx); err != nil {
		log.Fatalf("ping sqlite source: %v", err)
	}
	if err := targetDB.PingContext(ctx); err != nil {
		log.Fatalf("ping postgres target: %v", err)
	}

	targetStore := store.New(targetDB, "pgx")
	if err := targetStore.Init(ctx); err != nil {
		log.Fatalf("init target store: %v", err)
	}

	if !*forceFlag {
		if err := ensureTargetEmpty(ctx, targetDB); err != nil {
			log.Fatal(err)
		}
	}

	payload, err := loadSQLiteData(ctx, sourceDB)
	if err != nil {
		log.Fatalf("load sqlite data: %v", err)
	}

	if err := migrateToPostgres(ctx, targetDB, payload); err != nil {
		log.Fatalf("migrate to postgres: %v", err)
	}

	log.Printf(
		"migration complete: users=%d sessions=%d registrations=%d reset_tokens=%d roadmap=%d lessons=%d questions=%d results=%d",
		len(payload.users),
		len(payload.sessions),
		len(payload.registrationRequests),
		len(payload.passwordResetTokens),
		len(payload.roadmapProgress),
		len(payload.lessonProgress),
		len(payload.lessonTestQuestions),
		len(payload.lessonTestResults),
	)
}

type sqliteData struct {
	users                []userRow
	sessions             []sessionRow
	registrationRequests []registrationRequestRow
	passwordResetTokens  []passwordResetTokenRow
	roadmapProgress      []roadmapProgressRow
	lessonProgress       []lessonProgressRow
	lessonTestQuestions  []lessonTestQuestionRow
	lessonTestResults    []lessonTestResultRow
}

func loadSQLiteData(ctx context.Context, db *sql.DB) (*sqliteData, error) {
	users, err := readUsers(ctx, db)
	if err != nil {
		return nil, err
	}
	sessions, err := readSessions(ctx, db)
	if err != nil {
		return nil, err
	}
	registrationRequests, err := readRegistrationRequests(ctx, db)
	if err != nil {
		return nil, err
	}
	passwordResetTokens, err := readPasswordResetTokens(ctx, db)
	if err != nil {
		return nil, err
	}
	roadmapProgress, err := readRoadmapProgress(ctx, db)
	if err != nil {
		return nil, err
	}
	lessonProgress, err := readLessonProgress(ctx, db)
	if err != nil {
		return nil, err
	}
	lessonTestQuestions, err := readLessonTestQuestions(ctx, db)
	if err != nil {
		return nil, err
	}
	lessonTestResults, err := readLessonTestResults(ctx, db)
	if err != nil {
		return nil, err
	}

	return &sqliteData{
		users:                users,
		sessions:             sessions,
		registrationRequests: registrationRequests,
		passwordResetTokens:  passwordResetTokens,
		roadmapProgress:      roadmapProgress,
		lessonProgress:       lessonProgress,
		lessonTestQuestions:  lessonTestQuestions,
		lessonTestResults:    lessonTestResults,
	}, nil
}

func migrateToPostgres(ctx context.Context, db *sql.DB, payload *sqliteData) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, row := range payload.users {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO users (id, username, email, password_hash, is_admin, is_banned, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET
				username = EXCLUDED.username,
				email = EXCLUDED.email,
				password_hash = EXCLUDED.password_hash,
				is_admin = EXCLUDED.is_admin,
				is_banned = EXCLUDED.is_banned,
				created_at = EXCLUDED.created_at`,
			row.ID, row.Username, row.Email, row.PasswordHash, row.IsAdmin, row.IsBanned, row.CreatedAt,
		); err != nil {
			return fmt.Errorf("upsert users: %w", err)
		}
	}

	for _, row := range payload.registrationRequests {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO registration_requests (
				id, username, email, password_hash, verification_token_hash, verification_expires_at,
				approved_at, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO UPDATE SET
				username = EXCLUDED.username,
				email = EXCLUDED.email,
				password_hash = EXCLUDED.password_hash,
				verification_token_hash = EXCLUDED.verification_token_hash,
				verification_expires_at = EXCLUDED.verification_expires_at,
				approved_at = EXCLUDED.approved_at,
				created_at = EXCLUDED.created_at,
				updated_at = EXCLUDED.updated_at`,
			row.ID,
			row.Username,
			row.Email,
			row.PasswordHash,
			nullStringValue(row.VerificationTokenHash),
			nullInt64Value(row.VerificationExpiresAt),
			nullInt64Value(row.ApprovedAt),
			row.CreatedAt,
			row.UpdatedAt,
		); err != nil {
			return fmt.Errorf("upsert registration_requests: %w", err)
		}
	}

	for _, row := range payload.lessonTestQuestions {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO lesson_test_questions (
				id, lesson_slug, prompt, options_json, correct_option, explanation, order_index, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET
				lesson_slug = EXCLUDED.lesson_slug,
				prompt = EXCLUDED.prompt,
				options_json = EXCLUDED.options_json,
				correct_option = EXCLUDED.correct_option,
				explanation = EXCLUDED.explanation,
				order_index = EXCLUDED.order_index,
				created_at = EXCLUDED.created_at`,
			row.ID,
			row.LessonSlug,
			row.Prompt,
			row.OptionsJSON,
			row.CorrectOption,
			row.Explanation,
			row.OrderIndex,
			row.CreatedAt,
		); err != nil {
			return fmt.Errorf("upsert lesson_test_questions: %w", err)
		}
	}

	for _, row := range payload.sessions {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO sessions (token_hash, user_id, expires_at, created_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (token_hash) DO UPDATE SET
				user_id = EXCLUDED.user_id,
				expires_at = EXCLUDED.expires_at,
				created_at = EXCLUDED.created_at`,
			row.TokenHash, row.UserID, row.ExpiresAt, row.CreatedAt,
		); err != nil {
			return fmt.Errorf("upsert sessions: %w", err)
		}
	}

	for _, row := range payload.passwordResetTokens {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO password_reset_tokens (token_hash, user_id, expires_at, created_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (token_hash) DO UPDATE SET
				user_id = EXCLUDED.user_id,
				expires_at = EXCLUDED.expires_at,
				created_at = EXCLUDED.created_at`,
			row.TokenHash, row.UserID, row.ExpiresAt, row.CreatedAt,
		); err != nil {
			return fmt.Errorf("upsert password_reset_tokens: %w", err)
		}
	}

	for _, row := range payload.roadmapProgress {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO user_roadmap_progress (user_id, checkpoint_key, done, updated_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, checkpoint_key) DO UPDATE SET
				done = EXCLUDED.done,
				updated_at = EXCLUDED.updated_at`,
			row.UserID, row.CheckpointKey, row.Done, row.UpdatedAt,
		); err != nil {
			return fmt.Errorf("upsert user_roadmap_progress: %w", err)
		}
	}

	for _, row := range payload.lessonProgress {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO user_lesson_progress (user_id, lesson_slug, done, updated_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, lesson_slug) DO UPDATE SET
				done = EXCLUDED.done,
				updated_at = EXCLUDED.updated_at`,
			row.UserID, row.LessonSlug, row.Done, row.UpdatedAt,
		); err != nil {
			return fmt.Errorf("upsert user_lesson_progress: %w", err)
		}
	}

	for _, row := range payload.lessonTestResults {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO user_lesson_test_results (
				user_id, lesson_slug, attempts_count, last_wrong_answers, passed, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id, lesson_slug) DO UPDATE SET
				attempts_count = EXCLUDED.attempts_count,
				last_wrong_answers = EXCLUDED.last_wrong_answers,
				passed = EXCLUDED.passed,
				updated_at = EXCLUDED.updated_at`,
			row.UserID, row.LessonSlug, row.AttemptsCount, row.LastWrongAnswers, row.Passed, row.UpdatedAt,
		); err != nil {
			return fmt.Errorf("upsert user_lesson_test_results: %w", err)
		}
	}

	for _, sequence := range []struct {
		table  string
		column string
	}{
		{table: "users", column: "id"},
		{table: "registration_requests", column: "id"},
		{table: "lesson_test_questions", column: "id"},
	} {
		if _, err = tx.ExecContext(ctx, fmt.Sprintf(`
			SELECT setval(
				pg_get_serial_sequence('%s', '%s'),
				COALESCE((SELECT MAX(%s) FROM %s), 1),
				EXISTS(SELECT 1 FROM %s)
			)`,
			sequence.table,
			sequence.column,
			sequence.column,
			sequence.table,
			sequence.table,
		)); err != nil {
			return fmt.Errorf("reset sequence for %s: %w", sequence.table, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	return nil
}

func ensureTargetEmpty(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{
		"users",
		"registration_requests",
		"password_reset_tokens",
		"sessions",
		"user_roadmap_progress",
		"user_lesson_progress",
		"lesson_test_questions",
		"user_lesson_test_results",
	} {
		var hasRows bool
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s LIMIT 1)`, table)).Scan(&hasRows); err != nil {
			return fmt.Errorf("check target table %s: %w", table, err)
		}
		if hasRows {
			return fmt.Errorf("target PostgreSQL database is not empty: table %s already has rows; rerun with -force if this is expected", table)
		}
	}

	return nil
}

func readUsers(ctx context.Context, db *sql.DB) ([]userRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, username, email, password_hash, is_admin, is_banned, created_at FROM users ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []userRow
	for rows.Next() {
		var record userRow
		if err := rows.Scan(&record.ID, &record.Username, &record.Email, &record.PasswordHash, &record.IsAdmin, &record.IsBanned, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

func readSessions(ctx context.Context, db *sql.DB) ([]sessionRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT token_hash, user_id, expires_at, created_at FROM sessions ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []sessionRow
	for rows.Next() {
		var record sessionRow
		if err := rows.Scan(&record.TokenHash, &record.UserID, &record.ExpiresAt, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

func readRegistrationRequests(ctx context.Context, db *sql.DB) ([]registrationRequestRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, username, email, password_hash, verification_token_hash, verification_expires_at, approved_at, created_at, updated_at
		FROM registration_requests
		ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []registrationRequestRow
	for rows.Next() {
		var record registrationRequestRow
		if err := rows.Scan(
			&record.ID,
			&record.Username,
			&record.Email,
			&record.PasswordHash,
			&record.VerificationTokenHash,
			&record.VerificationExpiresAt,
			&record.ApprovedAt,
			&record.CreatedAt,
			&record.UpdatedAt,
		); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

func readPasswordResetTokens(ctx context.Context, db *sql.DB) ([]passwordResetTokenRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT token_hash, user_id, expires_at, created_at FROM password_reset_tokens ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []passwordResetTokenRow
	for rows.Next() {
		var record passwordResetTokenRow
		if err := rows.Scan(&record.TokenHash, &record.UserID, &record.ExpiresAt, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

func readRoadmapProgress(ctx context.Context, db *sql.DB) ([]roadmapProgressRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id, checkpoint_key, done, updated_at FROM user_roadmap_progress ORDER BY user_id ASC, checkpoint_key ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []roadmapProgressRow
	for rows.Next() {
		var record roadmapProgressRow
		if err := rows.Scan(&record.UserID, &record.CheckpointKey, &record.Done, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

func readLessonProgress(ctx context.Context, db *sql.DB) ([]lessonProgressRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id, lesson_slug, done, updated_at FROM user_lesson_progress ORDER BY user_id ASC, lesson_slug ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []lessonProgressRow
	for rows.Next() {
		var record lessonProgressRow
		if err := rows.Scan(&record.UserID, &record.LessonSlug, &record.Done, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

func readLessonTestQuestions(ctx context.Context, db *sql.DB) ([]lessonTestQuestionRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, lesson_slug, prompt, options_json, correct_option, explanation, order_index, created_at
		FROM lesson_test_questions
		ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []lessonTestQuestionRow
	for rows.Next() {
		var record lessonTestQuestionRow
		if err := rows.Scan(
			&record.ID,
			&record.LessonSlug,
			&record.Prompt,
			&record.OptionsJSON,
			&record.CorrectOption,
			&record.Explanation,
			&record.OrderIndex,
			&record.CreatedAt,
		); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

func readLessonTestResults(ctx context.Context, db *sql.DB) ([]lessonTestResultRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT user_id, lesson_slug, attempts_count, last_wrong_answers, passed, updated_at
		FROM user_lesson_test_results
		ORDER BY user_id ASC, lesson_slug ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []lessonTestResultRow
	for rows.Next() {
		var record lessonTestResultRow
		if err := rows.Scan(
			&record.UserID,
			&record.LessonSlug,
			&record.AttemptsCount,
			&record.LastWrongAnswers,
			&record.Passed,
			&record.UpdatedAt,
		); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

func nullStringValue(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func nullInt64Value(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

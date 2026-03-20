package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrLessonTestQuestionNotFound = errors.New("lesson test question not found")

type LessonTestQuestion struct {
	ID            int64
	LessonSlug    string
	Prompt        string
	Options       []string
	CorrectOption int
	Explanation   string
	OrderIndex    int
}

type LessonTestResult struct {
	LessonSlug       string
	AttemptsCount    int
	LastWrongAnswers int
	Passed           bool
}

func (s *Store) LessonTestQuestions(ctx context.Context, lessonSlug string) ([]LessonTestQuestion, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, lesson_slug, prompt, options_json, correct_option, explanation, order_index
		FROM lesson_test_questions
		WHERE lesson_slug = ?
		ORDER BY order_index ASC, id ASC`,
		strings.TrimSpace(lessonSlug),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	questions := make([]LessonTestQuestion, 0, 8)
	for rows.Next() {
		question, err := scanLessonTestQuestion(rows)
		if err != nil {
			return nil, err
		}
		questions = append(questions, *question)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return questions, nil
}

func (s *Store) ListLessonTestQuestions(ctx context.Context) ([]LessonTestQuestion, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, lesson_slug, prompt, options_json, correct_option, explanation, order_index
		FROM lesson_test_questions
		ORDER BY lesson_slug ASC, order_index ASC, id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	questions := make([]LessonTestQuestion, 0, 32)
	for rows.Next() {
		question, err := scanLessonTestQuestion(rows)
		if err != nil {
			return nil, err
		}
		questions = append(questions, *question)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return questions, nil
}

func (s *Store) CreateLessonTestQuestion(ctx context.Context, lessonSlug, prompt string, options []string, correctOption int, explanation string) (int64, error) {
	options = normalizeLessonOptions(options)
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return 0, err
	}

	var nextOrder int
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(order_index), 0) + 1 FROM lesson_test_questions WHERE lesson_slug = ?`,
		strings.TrimSpace(lessonSlug),
	).Scan(&nextOrder); err != nil {
		return 0, err
	}

	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO lesson_test_questions (
			lesson_slug,
			prompt,
			options_json,
			correct_option,
			explanation,
			order_index,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(lessonSlug),
		strings.TrimSpace(prompt),
		string(optionsJSON),
		correctOption,
		strings.TrimSpace(explanation),
		nextOrder,
		time.Now().UTC().Unix(),
	)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

func (s *Store) DeleteLessonTestQuestion(ctx context.Context, questionID int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM lesson_test_questions WHERE id = ?`, questionID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrLessonTestQuestionNotFound
	}

	return nil
}

func (s *Store) LessonTestResults(ctx context.Context, userID int64) (map[string]LessonTestResult, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT lesson_slug, attempts_count, last_wrong_answers, passed
		FROM user_lesson_test_results
		WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make(map[string]LessonTestResult)
	for rows.Next() {
		var result LessonTestResult
		var passed int

		if err := rows.Scan(&result.LessonSlug, &result.AttemptsCount, &result.LastWrongAnswers, &passed); err != nil {
			return nil, err
		}

		result.Passed = passed != 0
		results[result.LessonSlug] = result
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Store) UpsertLessonTestResult(ctx context.Context, userID int64, lessonSlug string, wrongAnswers int, passed bool) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO user_lesson_test_results (
			user_id,
			lesson_slug,
			attempts_count,
			last_wrong_answers,
			passed,
			updated_at
		) VALUES (?, ?, 1, ?, ?, ?)
		ON CONFLICT(user_id, lesson_slug) DO UPDATE SET
			attempts_count = user_lesson_test_results.attempts_count + 1,
			last_wrong_answers = excluded.last_wrong_answers,
			passed = excluded.passed,
			updated_at = excluded.updated_at`,
		userID,
		strings.TrimSpace(lessonSlug),
		wrongAnswers,
		boolToInt(passed),
		time.Now().UTC().Unix(),
	)
	return err
}

func scanLessonTestQuestion(row scanner) (*LessonTestQuestion, error) {
	var (
		question    LessonTestQuestion
		optionsJSON string
	)

	if err := row.Scan(
		&question.ID,
		&question.LessonSlug,
		&question.Prompt,
		&optionsJSON,
		&question.CorrectOption,
		&question.Explanation,
		&question.OrderIndex,
	); err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(optionsJSON), &question.Options); err != nil {
		return nil, err
	}

	return &question, nil
}

func normalizeLessonOptions(values []string) []string {
	options := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		options = append(options, trimmed)
	}

	return options
}

func lessonTestQuestionExists(err error) bool {
	return !errors.Is(err, sql.ErrNoRows)
}

package store

import (
	"context"
	"time"
)

func (s *Store) LessonProgress(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT lesson_slug, done
		FROM user_lesson_progress
		WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	progress := make(map[string]bool)
	for rows.Next() {
		var (
			lessonSlug string
			done       int
		)

		if err := rows.Scan(&lessonSlug, &done); err != nil {
			return nil, err
		}

		progress[lessonSlug] = done != 0
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return progress, nil
}

func (s *Store) SetLessonProgress(ctx context.Context, userID int64, lessonSlug string, done bool) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO user_lesson_progress (user_id, lesson_slug, done, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, lesson_slug) DO UPDATE SET
			done = excluded.done,
			updated_at = excluded.updated_at`,
		userID,
		lessonSlug,
		boolToInt(done),
		time.Now().UTC().Unix(),
	)
	return err
}

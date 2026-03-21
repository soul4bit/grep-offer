package store

import (
	"context"
	"time"
)

type CheckpointProgress struct {
	CheckpointKey string
	Done          bool
}

func (s *Store) EnsureRoadmapProgress(ctx context.Context, userID int64, checkpoints []CheckpointProgress) error {
	if len(checkpoints) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	nowUnix := time.Now().UTC().Unix()
	stmt, err := tx.PrepareContext(
		ctx,
		s.bind(`INSERT INTO user_roadmap_progress (user_id, checkpoint_key, done, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, checkpoint_key) DO NOTHING`),
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, checkpoint := range checkpoints {
		if checkpoint.CheckpointKey == "" {
			continue
		}

		if _, err = stmt.ExecContext(ctx, userID, checkpoint.CheckpointKey, boolToInt(checkpoint.Done), nowUnix); err != nil {
			return err
		}
	}

	err = tx.Commit()
	return err
}

func (s *Store) RoadmapProgress(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(
		ctx,
		s.bind(`SELECT checkpoint_key, done
		FROM user_roadmap_progress
		WHERE user_id = ?`),
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	progress := make(map[string]bool)
	for rows.Next() {
		var (
			checkpointKey string
			done          int
		)

		if err := rows.Scan(&checkpointKey, &done); err != nil {
			return nil, err
		}

		progress[checkpointKey] = done != 0
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return progress, nil
}

func (s *Store) SetRoadmapCheckpoint(ctx context.Context, userID int64, checkpointKey string, done bool) error {
	_, err := s.db.ExecContext(
		ctx,
		s.bind(`INSERT INTO user_roadmap_progress (user_id, checkpoint_key, done, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, checkpoint_key) DO UPDATE SET
			done = excluded.done,
			updated_at = excluded.updated_at`),
		userID,
		checkpointKey,
		boolToInt(done),
		time.Now().UTC().Unix(),
	)
	return err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}

	return 0
}

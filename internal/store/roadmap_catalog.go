package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrRoadmapStageNotFound  = errors.New("roadmap stage not found")
	ErrRoadmapModuleNotFound = errors.New("roadmap module not found")
)

type RoadmapStage struct {
	ID         int64
	Key        string
	Title      string
	Badge      string
	Summary    string
	Note       string
	OrderIndex int
	Modules    []RoadmapModule
}

type RoadmapModule struct {
	ID         int64
	StageID    int64
	Key        string
	Title      string
	Note       string
	OrderIndex int
}

func (s *Store) EnsureRoadmap(ctx context.Context, stages []RoadmapStage) error {
	if len(stages) == 0 {
		return nil
	}

	var count int
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM roadmap_stages`)).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
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
	for stageIndex, stage := range stages {
		if stage.Key == "" || stage.Title == "" {
			continue
		}

		orderIndex := stage.OrderIndex
		if orderIndex <= 0 {
			orderIndex = stageIndex + 1
		}

		stageID, err := s.insertID(
			ctx,
			tx,
			`INSERT INTO roadmap_stages (stage_key, title, badge, summary, note, order_index, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			stage.Key,
			stage.Title,
			stage.Badge,
			stage.Summary,
			stage.Note,
			orderIndex,
			nowUnix,
			nowUnix,
		)
		if err != nil {
			return err
		}

		for moduleIndex, module := range stage.Modules {
			if module.Key == "" || module.Title == "" {
				continue
			}

			moduleOrder := module.OrderIndex
			if moduleOrder <= 0 {
				moduleOrder = moduleIndex + 1
			}

			if _, err := s.insertID(
				ctx,
				tx,
				`INSERT INTO roadmap_modules (stage_id, checkpoint_key, title, note, order_index, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
				stageID,
				module.Key,
				module.Title,
				module.Note,
				moduleOrder,
				nowUnix,
				nowUnix,
			); err != nil {
				return err
			}
		}
	}

	err = tx.Commit()
	return err
}

func (s *Store) Roadmap(ctx context.Context) ([]RoadmapStage, error) {
	rows, err := s.db.QueryContext(
		ctx,
		s.bind(`SELECT id, stage_key, title, badge, summary, note, order_index
		FROM roadmap_stages
		ORDER BY order_index, id`),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stages := make([]RoadmapStage, 0, 8)
	stageIndexes := make(map[int64]int)
	for rows.Next() {
		var stage RoadmapStage
		if err := rows.Scan(&stage.ID, &stage.Key, &stage.Title, &stage.Badge, &stage.Summary, &stage.Note, &stage.OrderIndex); err != nil {
			return nil, err
		}

		stageIndexes[stage.ID] = len(stages)
		stages = append(stages, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	moduleRows, err := s.db.QueryContext(
		ctx,
		s.bind(`SELECT id, stage_id, checkpoint_key, title, note, order_index
		FROM roadmap_modules
		ORDER BY stage_id, order_index, id`),
	)
	if err != nil {
		return nil, err
	}
	defer moduleRows.Close()

	for moduleRows.Next() {
		var module RoadmapModule
		if err := moduleRows.Scan(&module.ID, &module.StageID, &module.Key, &module.Title, &module.Note, &module.OrderIndex); err != nil {
			return nil, err
		}

		stageIndex, ok := stageIndexes[module.StageID]
		if !ok {
			continue
		}
		stages[stageIndex].Modules = append(stages[stageIndex].Modules, module)
	}
	if err := moduleRows.Err(); err != nil {
		return nil, err
	}

	return stages, nil
}

func (s *Store) RoadmapStageByID(ctx context.Context, stageID int64) (*RoadmapStage, error) {
	row := s.db.QueryRowContext(
		ctx,
		s.bind(`SELECT id, stage_key, title, badge, summary, note, order_index
		FROM roadmap_stages
		WHERE id = ?`),
		stageID,
	)

	var stage RoadmapStage
	if err := row.Scan(&stage.ID, &stage.Key, &stage.Title, &stage.Badge, &stage.Summary, &stage.Note, &stage.OrderIndex); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRoadmapStageNotFound
		}
		return nil, err
	}

	return &stage, nil
}

func (s *Store) RoadmapModuleByID(ctx context.Context, moduleID int64) (*RoadmapModule, error) {
	row := s.db.QueryRowContext(
		ctx,
		s.bind(`SELECT id, stage_id, checkpoint_key, title, note, order_index
		FROM roadmap_modules
		WHERE id = ?`),
		moduleID,
	)

	var module RoadmapModule
	if err := row.Scan(&module.ID, &module.StageID, &module.Key, &module.Title, &module.Note, &module.OrderIndex); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRoadmapModuleNotFound
		}
		return nil, err
	}

	return &module, nil
}

func (s *Store) CreateRoadmapStage(ctx context.Context, stage RoadmapStage) (*RoadmapStage, error) {
	nowUnix := time.Now().UTC().Unix()
	id, err := s.insertID(
		ctx,
		s.db,
		`INSERT INTO roadmap_stages (stage_key, title, badge, summary, note, order_index, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		stage.Key,
		stage.Title,
		stage.Badge,
		stage.Summary,
		stage.Note,
		stage.OrderIndex,
		nowUnix,
		nowUnix,
	)
	if err != nil {
		return nil, err
	}

	return s.RoadmapStageByID(ctx, id)
}

func (s *Store) UpdateRoadmapStage(ctx context.Context, stage RoadmapStage) error {
	result, err := s.db.ExecContext(
		ctx,
		s.bind(`UPDATE roadmap_stages
		SET title = ?, badge = ?, summary = ?, note = ?, order_index = ?, updated_at = ?
		WHERE id = ?`),
		stage.Title,
		stage.Badge,
		stage.Summary,
		stage.Note,
		stage.OrderIndex,
		time.Now().UTC().Unix(),
		stage.ID,
	)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrRoadmapStageNotFound
	}

	return nil
}

func (s *Store) DeleteRoadmapStage(ctx context.Context, stageID int64) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM roadmap_stages WHERE id = ?`), stageID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrRoadmapStageNotFound
	}

	return nil
}

func (s *Store) CreateRoadmapModule(ctx context.Context, module RoadmapModule) (*RoadmapModule, error) {
	nowUnix := time.Now().UTC().Unix()
	id, err := s.insertID(
		ctx,
		s.db,
		`INSERT INTO roadmap_modules (stage_id, checkpoint_key, title, note, order_index, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		module.StageID,
		module.Key,
		module.Title,
		module.Note,
		module.OrderIndex,
		nowUnix,
		nowUnix,
	)
	if err != nil {
		return nil, err
	}

	return s.RoadmapModuleByID(ctx, id)
}

func (s *Store) UpdateRoadmapModule(ctx context.Context, module RoadmapModule) error {
	result, err := s.db.ExecContext(
		ctx,
		s.bind(`UPDATE roadmap_modules
		SET title = ?, note = ?, order_index = ?, updated_at = ?
		WHERE id = ?`),
		module.Title,
		module.Note,
		module.OrderIndex,
		time.Now().UTC().Unix(),
		module.ID,
	)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrRoadmapModuleNotFound
	}

	return nil
}

func (s *Store) DeleteRoadmapModule(ctx context.Context, moduleID int64) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM roadmap_modules WHERE id = ?`), moduleID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrRoadmapModuleNotFound
	}

	return nil
}

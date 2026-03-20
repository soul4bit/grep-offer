package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type RegistrationRequest struct {
	ID                    int64
	Username              string
	Email                 string
	PasswordHash          string
	VerificationTokenHash string
	VerificationExpiresAt *time.Time
	ApprovedAt            *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (r *RegistrationRequest) AwaitingApproval() bool {
	return r != nil && r.ApprovedAt == nil
}

func (r *RegistrationRequest) AwaitingConfirmation() bool {
	return r != nil && r.ApprovedAt != nil
}

func (s *Store) CreateRegistrationRequest(ctx context.Context, username, email, passwordHash string) (*RegistrationRequest, error) {
	now := time.Now().UTC()
	username = strings.TrimSpace(username)
	email = normalizeEmail(email)

	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO registration_requests (username, email, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		username,
		email,
		passwordHash,
		now.Unix(),
		now.Unix(),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrRegistrationPending
		}
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &RegistrationRequest{
		ID:           id,
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (s *Store) RegistrationRequestByEmail(ctx context.Context, email string) (*RegistrationRequest, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, username, email, password_hash, verification_token_hash, verification_expires_at, approved_at, created_at, updated_at
		FROM registration_requests
		WHERE email = ?`,
		normalizeEmail(email),
	)

	request, err := scanRegistrationRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRegistrationNotFound
		}
		return nil, err
	}

	return request, nil
}

func (s *Store) RegistrationRequestByID(ctx context.Context, id int64) (*RegistrationRequest, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, username, email, password_hash, verification_token_hash, verification_expires_at, approved_at, created_at, updated_at
		FROM registration_requests
		WHERE id = ?`,
		id,
	)

	request, err := scanRegistrationRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRegistrationNotFound
		}
		return nil, err
	}

	return request, nil
}

func (s *Store) ApproveRegistrationRequest(ctx context.Context, id int64, rawVerificationToken string, expiresAt time.Time) (*RegistrationRequest, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE registration_requests
		SET verification_token_hash = ?, verification_expires_at = ?, approved_at = ?, updated_at = ?
		WHERE id = ? AND approved_at IS NULL`,
		hashToken(rawVerificationToken),
		expiresAt.UTC().Unix(),
		now.Unix(),
		now.Unix(),
		id,
	)
	if err != nil {
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}

	if rowsAffected == 0 {
		request, lookupErr := s.RegistrationRequestByID(ctx, id)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if request.ApprovedAt != nil {
			return nil, ErrRegistrationAlreadyApproved
		}
		return nil, ErrRegistrationNotFound
	}

	return s.RegistrationRequestByID(ctx, id)
}

func (s *Store) ResetRegistrationApproval(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE registration_requests
		SET verification_token_hash = NULL, verification_expires_at = NULL, approved_at = NULL, updated_at = ?
		WHERE id = ?`,
		time.Now().UTC().Unix(),
		id,
	)
	return err
}

func (s *Store) DeleteRegistrationRequest(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM registration_requests WHERE id = ?`, id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return ErrRegistrationNotFound
	}

	return nil
}

func (s *Store) ConsumeRegistrationRequest(ctx context.Context, rawVerificationToken string) (*User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	nowUnix := time.Now().UTC().Unix()
	row := tx.QueryRowContext(
		ctx,
		`SELECT id, username, email, password_hash, verification_token_hash, verification_expires_at, approved_at, created_at, updated_at
		FROM registration_requests
		WHERE verification_token_hash = ? AND verification_expires_at > ?`,
		hashToken(rawVerificationToken),
		nowUnix,
	)

	request, scanErr := scanRegistrationRequest(row)
	if scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			err = ErrRegistrationTokenNotFound
			return nil, err
		}
		err = scanErr
		return nil, err
	}

	userCreatedAt := time.Now().UTC()
	result, execErr := tx.ExecContext(
		ctx,
		`INSERT INTO users (username, email, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		request.Username,
		request.Email,
		request.PasswordHash,
		userCreatedAt.Unix(),
	)
	if execErr != nil {
		if strings.Contains(strings.ToLower(execErr.Error()), "unique") {
			err = ErrEmailTaken
			return nil, err
		}
		err = execErr
		return nil, err
	}

	userID, idErr := result.LastInsertId()
	if idErr != nil {
		err = idErr
		return nil, err
	}

	if _, execErr = tx.ExecContext(ctx, `DELETE FROM registration_requests WHERE id = ?`, request.ID); execErr != nil {
		err = execErr
		return nil, err
	}

	if commitErr := tx.Commit(); commitErr != nil {
		err = commitErr
		return nil, err
	}

	return &User{
		ID:           userID,
		Username:     request.Username,
		Email:        request.Email,
		PasswordHash: request.PasswordHash,
		CreatedAt:    userCreatedAt,
	}, nil
}

func scanRegistrationRequest(row scanner) (*RegistrationRequest, error) {
	var (
		verificationTokenHash     sql.NullString
		request                   RegistrationRequest
		verificationExpiresAtUnix sql.NullInt64
		approvedAtUnix            sql.NullInt64
		createdAtUnix             int64
		updatedAtUnix             int64
	)

	if err := row.Scan(
		&request.ID,
		&request.Username,
		&request.Email,
		&request.PasswordHash,
		&verificationTokenHash,
		&verificationExpiresAtUnix,
		&approvedAtUnix,
		&createdAtUnix,
		&updatedAtUnix,
	); err != nil {
		return nil, err
	}

	request.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	request.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	if verificationTokenHash.Valid {
		request.VerificationTokenHash = verificationTokenHash.String
	}

	if verificationExpiresAtUnix.Valid {
		expiresAt := time.Unix(verificationExpiresAtUnix.Int64, 0).UTC()
		request.VerificationExpiresAt = &expiresAt
	}

	if approvedAtUnix.Valid {
		approvedAt := time.Unix(approvedAtUnix.Int64, 0).UTC()
		request.ApprovedAt = &approvedAt
	}

	return &request, nil
}

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// SessionStore handles session persistence
type SessionStore struct {
	db *DB
}

// NewSessionStore creates a new session store
func NewSessionStore(db *DB) *SessionStore {
	return &SessionStore{db: db}
}

// Create inserts a new session
func (s *SessionStore) Create(ctx context.Context, session *models.Session) error {
	query := `
		INSERT INTO sessions (
			id, consumer_id, provider, provider_instance_id, offer_id,
			gpu_type, gpu_count, status, error,
			ssh_host, ssh_port, ssh_user, ssh_public_key,
			workload_type, reservation_hours, hard_max_override,
			idle_threshold_minutes, storage_policy,
			price_per_hour, created_at, expires_at, stopped_at
		) VALUES (
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?,
			?, ?, ?, ?
		)
	`

	_, err := s.db.ExecContext(ctx, query,
		session.ID, session.ConsumerID, session.Provider, session.ProviderID, session.OfferID,
		session.GPUType, session.GPUCount, session.Status, session.Error,
		session.SSHHost, session.SSHPort, session.SSHUser, session.SSHPublicKey,
		session.WorkloadType, session.ReservationHrs, session.HardMaxOverride,
		session.IdleThreshold, session.StoragePolicy,
		session.PricePerHour, session.CreatedAt, session.ExpiresAt, nullTime(session.StoppedAt),
	)

	if err != nil {
		// Bug #47 fix: Detect SQLite UNIQUE constraint violation for duplicate active sessions
		// This catches races where two requests pass the app-level check simultaneously
		if strings.Contains(err.Error(), "UNIQUE constraint failed") &&
			strings.Contains(err.Error(), "idx_sessions_consumer_offer_active") {
			return ErrAlreadyExists
		}
		return fmt.Errorf("failed to create session: %w", err)
	}

	return nil
}

// Get retrieves a session by ID
func (s *SessionStore) Get(ctx context.Context, id string) (*models.Session, error) {
	query := `
		SELECT
			id, consumer_id, provider, provider_instance_id, offer_id,
			gpu_type, gpu_count, status, error,
			ssh_host, ssh_port, ssh_user, ssh_public_key,
			workload_type, reservation_hours, hard_max_override,
			idle_threshold_minutes, storage_policy,
			price_per_hour, created_at, expires_at, stopped_at
		FROM sessions
		WHERE id = ?
	`

	session := &models.Session{}
	var stoppedAt sql.NullTime
	var providerID, sshHost, sshUser, sshPublicKey, errorStr sql.NullString
	var sshPort sql.NullInt64

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&session.ID, &session.ConsumerID, &session.Provider, &providerID, &session.OfferID,
		&session.GPUType, &session.GPUCount, &session.Status, &errorStr,
		&sshHost, &sshPort, &sshUser, &sshPublicKey,
		&session.WorkloadType, &session.ReservationHrs, &session.HardMaxOverride,
		&session.IdleThreshold, &session.StoragePolicy,
		&session.PricePerHour, &session.CreatedAt, &session.ExpiresAt, &stoppedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Handle nullable fields
	session.ProviderID = providerID.String
	session.SSHHost = sshHost.String
	session.SSHPort = int(sshPort.Int64)
	session.SSHUser = sshUser.String
	session.SSHPublicKey = sshPublicKey.String
	session.Error = errorStr.String
	if stoppedAt.Valid {
		session.StoppedAt = stoppedAt.Time
	}

	return session, nil
}

// Update updates an existing session
func (s *SessionStore) Update(ctx context.Context, session *models.Session) error {
	query := `
		UPDATE sessions SET
			provider_instance_id = ?,
			status = ?,
			error = ?,
			ssh_host = ?,
			ssh_port = ?,
			ssh_user = ?,
			hard_max_override = ?,
			reservation_hours = ?,
			expires_at = ?,
			stopped_at = ?
		WHERE id = ?
	`

	result, err := s.db.ExecContext(ctx, query,
		session.ProviderID,
		session.Status,
		session.Error,
		session.SSHHost,
		session.SSHPort,
		session.SSHUser,
		session.HardMaxOverride,
		session.ReservationHrs,
		session.ExpiresAt,
		nullTime(session.StoppedAt),
		session.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}

	return nil
}

// ListInternal returns sessions matching the internal filter (used by lifecycle and other internal services)
func (s *SessionStore) ListInternal(ctx context.Context, filter SessionFilter) ([]*models.Session, error) {
	query := `
		SELECT
			id, consumer_id, provider, provider_instance_id, offer_id,
			gpu_type, gpu_count, status, error,
			ssh_host, ssh_port, ssh_user, ssh_public_key,
			workload_type, reservation_hours, hard_max_override,
			idle_threshold_minutes, storage_policy,
			price_per_hour, created_at, expires_at, stopped_at
		FROM sessions
		WHERE 1=1
	`

	var args []interface{}

	if filter.ConsumerID != "" {
		query += " AND consumer_id = ?"
		args = append(args, filter.ConsumerID)
	}

	if filter.Provider != "" {
		query += " AND provider = ?"
		args = append(args, filter.Provider)
	}

	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, status)
		}
		query += fmt.Sprintf(" AND status IN (%s)", strings.Join(placeholders, ","))
	}

	if !filter.ExpiresBeforeTime.IsZero() {
		query += " AND expires_at < ?"
		args = append(args, filter.ExpiresBeforeTime)
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*models.Session
	for rows.Next() {
		session := &models.Session{}
		var stoppedAt sql.NullTime
		var providerID, sshHost, sshUser, sshPublicKey, errorStr sql.NullString
		var sshPort sql.NullInt64

		err := rows.Scan(
			&session.ID, &session.ConsumerID, &session.Provider, &providerID, &session.OfferID,
			&session.GPUType, &session.GPUCount, &session.Status, &errorStr,
			&sshHost, &sshPort, &sshUser, &sshPublicKey,
			&session.WorkloadType, &session.ReservationHrs, &session.HardMaxOverride,
			&session.IdleThreshold, &session.StoragePolicy,
			&session.PricePerHour, &session.CreatedAt, &session.ExpiresAt, &stoppedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}

		// Handle nullable fields
		session.ProviderID = providerID.String
		session.SSHHost = sshHost.String
		session.SSHPort = int(sshPort.Int64)
		session.SSHUser = sshUser.String
		session.SSHPublicKey = sshPublicKey.String
		session.Error = errorStr.String
		if stoppedAt.Valid {
			session.StoppedAt = stoppedAt.Time
		}

		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating sessions: %w", err)
	}

	return sessions, nil
}

// GetActiveSessions returns all sessions in active states
func (s *SessionStore) GetActiveSessions(ctx context.Context) ([]*models.Session, error) {
	return s.ListInternal(ctx, SessionFilter{
		Statuses: []models.SessionStatus{
			models.StatusPending,
			models.StatusProvisioning,
			models.StatusRunning,
		},
	})
}

// GetActiveSessionsByProvider returns active sessions for a specific provider
func (s *SessionStore) GetActiveSessionsByProvider(ctx context.Context, provider string) ([]*models.Session, error) {
	return s.ListInternal(ctx, SessionFilter{
		Provider: provider,
		Statuses: []models.SessionStatus{
			models.StatusPending,
			models.StatusProvisioning,
			models.StatusRunning,
		},
	})
}

// GetExpiredSessions returns sessions past their expiration time
func (s *SessionStore) GetExpiredSessions(ctx context.Context) ([]*models.Session, error) {
	return s.ListInternal(ctx, SessionFilter{
		Statuses: []models.SessionStatus{
			models.StatusRunning,
		},
		ExpiresBeforeTime: time.Now(),
	})
}

// GetSessionsByStatus returns sessions with specific statuses
func (s *SessionStore) GetSessionsByStatus(ctx context.Context, statuses ...models.SessionStatus) ([]*models.Session, error) {
	return s.ListInternal(ctx, SessionFilter{
		Statuses: statuses,
	})
}

// List returns sessions matching the filter (implements provisioner.SessionStore interface)
func (s *SessionStore) List(ctx context.Context, filter models.SessionListFilter) ([]*models.Session, error) {
	// Bug #100 fix: Pass provider filter to internal list function
	return s.ListInternal(ctx, SessionFilter{
		ConsumerID: filter.ConsumerID,
		Provider:   filter.Provider,
		Status:     filter.Status,
		Limit:      filter.Limit,
	})
}

// SessionFilter defines criteria for filtering sessions
type SessionFilter struct {
	ConsumerID        string
	Provider          string
	Status            models.SessionStatus
	Statuses          []models.SessionStatus
	ExpiresBeforeTime time.Time
	Limit             int
}

// nullTime converts a time to sql.NullTime
func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// ProviderStatusCount holds the count of sessions for a provider/status combination
type ProviderStatusCount struct {
	Provider string
	Status   string
	Count    int
}

// CountSessionsByProviderAndStatus returns counts of active sessions grouped by provider and status.
// Only includes sessions that are not stopped or failed (i.e., pending, provisioning, running).
func (s *SessionStore) CountSessionsByProviderAndStatus(ctx context.Context) ([]ProviderStatusCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, status, COUNT(*) as count
		FROM sessions
		WHERE status NOT IN ('stopped', 'failed')
		GROUP BY provider, status
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to count sessions: %w", err)
	}
	defer rows.Close()

	var counts []ProviderStatusCount
	for rows.Next() {
		var c ProviderStatusCount
		if err := rows.Scan(&c.Provider, &c.Status, &c.Count); err != nil {
			return nil, fmt.Errorf("failed to scan count: %w", err)
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}

// GetActiveSessionByConsumerAndOffer returns an active session for the given consumer and offer, if one exists.
// Active sessions are those with status pending, provisioning, or running.
// Returns ErrNotFound if no active session exists.
func (s *SessionStore) GetActiveSessionByConsumerAndOffer(ctx context.Context, consumerID, offerID string) (*models.Session, error) {
	query := `
		SELECT
			id, consumer_id, provider, provider_instance_id, offer_id,
			gpu_type, gpu_count, status, error,
			ssh_host, ssh_port, ssh_user, ssh_public_key,
			workload_type, reservation_hours, hard_max_override,
			idle_threshold_minutes, storage_policy,
			price_per_hour, created_at, expires_at, stopped_at
		FROM sessions
		WHERE consumer_id = ? AND offer_id = ?
		  AND status IN ('pending', 'provisioning', 'running')
		ORDER BY created_at DESC
		LIMIT 1
	`

	session := &models.Session{}
	var stoppedAt sql.NullTime
	var providerID, sshHost, sshUser, sshPublicKey, errorStr sql.NullString
	var sshPort sql.NullInt64

	err := s.db.QueryRowContext(ctx, query, consumerID, offerID).Scan(
		&session.ID, &session.ConsumerID, &session.Provider, &providerID, &session.OfferID,
		&session.GPUType, &session.GPUCount, &session.Status, &errorStr,
		&sshHost, &sshPort, &sshUser, &sshPublicKey,
		&session.WorkloadType, &session.ReservationHrs, &session.HardMaxOverride,
		&session.IdleThreshold, &session.StoragePolicy,
		&session.PricePerHour, &session.CreatedAt, &session.ExpiresAt, &stoppedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get active session: %w", err)
	}

	// Handle nullable fields
	session.ProviderID = providerID.String
	session.SSHHost = sshHost.String
	session.SSHPort = int(sshPort.Int64)
	session.SSHUser = sshUser.String
	session.SSHPublicKey = sshPublicKey.String
	session.Error = errorStr.String
	if stoppedAt.Valid {
		session.StoppedAt = stoppedAt.Time
	}

	return session, nil
}

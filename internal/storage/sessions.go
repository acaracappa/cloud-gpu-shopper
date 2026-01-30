package storage

import (
	"context"
	"database/sql"
	"fmt"
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
			agent_endpoint, agent_token,
			workload_type, reservation_hours, hard_max_override,
			idle_threshold_minutes, storage_policy,
			price_per_hour, created_at, expires_at, last_heartbeat, stopped_at
		) VALUES (
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?,
			?, ?, ?,
			?, ?,
			?, ?, ?, ?, ?
		)
	`

	_, err := s.db.ExecContext(ctx, query,
		session.ID, session.ConsumerID, session.Provider, session.ProviderID, session.OfferID,
		session.GPUType, session.GPUCount, session.Status, session.Error,
		session.SSHHost, session.SSHPort, session.SSHUser, session.SSHPublicKey,
		session.AgentEndpoint, session.AgentToken,
		session.WorkloadType, session.ReservationHrs, session.HardMaxOverride,
		session.IdleThreshold, session.StoragePolicy,
		session.PricePerHour, session.CreatedAt, session.ExpiresAt, nullTime(session.LastHeartbeat), nullTime(session.StoppedAt),
	)

	if err != nil {
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
			agent_endpoint, agent_token,
			workload_type, reservation_hours, hard_max_override,
			idle_threshold_minutes, storage_policy,
			price_per_hour, created_at, expires_at, last_heartbeat, last_idle_seconds, stopped_at
		FROM sessions
		WHERE id = ?
	`

	session := &models.Session{}
	var lastHeartbeat, stoppedAt sql.NullTime
	var providerID, sshHost, sshUser, sshPublicKey, agentEndpoint, agentToken, errorStr sql.NullString
	var sshPort sql.NullInt64

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&session.ID, &session.ConsumerID, &session.Provider, &providerID, &session.OfferID,
		&session.GPUType, &session.GPUCount, &session.Status, &errorStr,
		&sshHost, &sshPort, &sshUser, &sshPublicKey,
		&agentEndpoint, &agentToken,
		&session.WorkloadType, &session.ReservationHrs, &session.HardMaxOverride,
		&session.IdleThreshold, &session.StoragePolicy,
		&session.PricePerHour, &session.CreatedAt, &session.ExpiresAt, &lastHeartbeat, &session.LastIdleSeconds, &stoppedAt,
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
	session.AgentEndpoint = agentEndpoint.String
	session.AgentToken = agentToken.String
	session.Error = errorStr.String
	if lastHeartbeat.Valid {
		session.LastHeartbeat = lastHeartbeat.Time
	}
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
			agent_endpoint = ?,
			hard_max_override = ?,
			reservation_hours = ?,
			expires_at = ?,
			last_heartbeat = ?,
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
		session.AgentEndpoint,
		session.HardMaxOverride,
		session.ReservationHrs,
		session.ExpiresAt,
		nullTime(session.LastHeartbeat),
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

// List returns sessions matching the filter
func (s *SessionStore) List(ctx context.Context, filter SessionFilter) ([]*models.Session, error) {
	query := `
		SELECT
			id, consumer_id, provider, provider_instance_id, offer_id,
			gpu_type, gpu_count, status, error,
			ssh_host, ssh_port, ssh_user, ssh_public_key,
			agent_endpoint, agent_token,
			workload_type, reservation_hours, hard_max_override,
			idle_threshold_minutes, storage_policy,
			price_per_hour, created_at, expires_at, last_heartbeat, last_idle_seconds, stopped_at
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
		query += fmt.Sprintf(" AND status IN (%s)", joinStrings(placeholders, ","))
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
		var lastHeartbeat, stoppedAt sql.NullTime
		var providerID, sshHost, sshUser, sshPublicKey, agentEndpoint, agentToken, errorStr sql.NullString
		var sshPort sql.NullInt64

		err := rows.Scan(
			&session.ID, &session.ConsumerID, &session.Provider, &providerID, &session.OfferID,
			&session.GPUType, &session.GPUCount, &session.Status, &errorStr,
			&sshHost, &sshPort, &sshUser, &sshPublicKey,
			&agentEndpoint, &agentToken,
			&session.WorkloadType, &session.ReservationHrs, &session.HardMaxOverride,
			&session.IdleThreshold, &session.StoragePolicy,
			&session.PricePerHour, &session.CreatedAt, &session.ExpiresAt, &lastHeartbeat, &session.LastIdleSeconds, &stoppedAt,
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
		session.AgentEndpoint = agentEndpoint.String
		session.AgentToken = agentToken.String
		session.Error = errorStr.String
		if lastHeartbeat.Valid {
			session.LastHeartbeat = lastHeartbeat.Time
		}
		if stoppedAt.Valid {
			session.StoppedAt = stoppedAt.Time
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// GetActiveSessions returns all sessions in active states
func (s *SessionStore) GetActiveSessions(ctx context.Context) ([]*models.Session, error) {
	return s.List(ctx, SessionFilter{
		Statuses: []models.SessionStatus{
			models.StatusPending,
			models.StatusProvisioning,
			models.StatusRunning,
		},
	})
}

// GetActiveSessionsByProvider returns active sessions for a specific provider
func (s *SessionStore) GetActiveSessionsByProvider(ctx context.Context, provider string) ([]*models.Session, error) {
	return s.List(ctx, SessionFilter{
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
	return s.List(ctx, SessionFilter{
		Statuses: []models.SessionStatus{
			models.StatusRunning,
		},
		ExpiresBeforeTime: time.Now(),
	})
}

// GetSessionsByStatus returns sessions with specific statuses
func (s *SessionStore) GetSessionsByStatus(ctx context.Context, statuses ...models.SessionStatus) ([]*models.Session, error) {
	return s.List(ctx, SessionFilter{
		Statuses: statuses,
	})
}

// UpdateHeartbeat updates the last heartbeat time for a session
func (s *SessionStore) UpdateHeartbeat(ctx context.Context, id string, heartbeatTime time.Time) error {
	query := `UPDATE sessions SET last_heartbeat = ? WHERE id = ?`

	result, err := s.db.ExecContext(ctx, query, heartbeatTime, id)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
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

// UpdateHeartbeatWithIdle updates the last heartbeat time and idle seconds for a session
func (s *SessionStore) UpdateHeartbeatWithIdle(ctx context.Context, id string, heartbeatTime time.Time, idleSeconds int) error {
	query := `UPDATE sessions SET last_heartbeat = ?, last_idle_seconds = ? WHERE id = ?`

	result, err := s.db.ExecContext(ctx, query, heartbeatTime, idleSeconds, id)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat with idle: %w", err)
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

// joinStrings joins strings with a separator
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

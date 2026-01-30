package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQL database connection
type DB struct {
	*sql.DB
}

// New creates a new database connection
func New(dbPath string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	// Open database with WAL mode for better concurrency
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(1) // SQLite doesn't handle concurrent writes well
	db.SetMaxIdleConns(1)

	return &DB{db}, nil
}

// Migrate runs database migrations
func (db *DB) Migrate(ctx context.Context) error {
	migrations := []string{
		migrationSessions,
		migrationCosts,
		migrationConsumers,
		migrationIndexes,
	}

	for i, migration := range migrations {
		if _, err := db.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("migration %d failed: %w", i+1, err)
		}
	}

	// Run ALTER TABLE migrations (ignore "duplicate column" errors)
	alterMigrations := []string{
		migrationIdleTracking,
		migrationAgentToken,
	}

	for _, migration := range alterMigrations {
		_, _ = db.ExecContext(ctx, migration) // Ignore errors for idempotency
	}

	// Run index migrations that may fail if already exists
	indexMigrations := []string{
		migrationDuplicatePrevention,
	}

	for _, migration := range indexMigrations {
		_, _ = db.ExecContext(ctx, migration) // Ignore errors for idempotency
	}

	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}

const migrationSessions = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	consumer_id TEXT NOT NULL,
	provider TEXT NOT NULL,
	provider_instance_id TEXT,
	offer_id TEXT NOT NULL,
	gpu_type TEXT NOT NULL,
	gpu_count INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'pending',
	error TEXT,

	-- Connection details
	ssh_host TEXT,
	ssh_port INTEGER,
	ssh_user TEXT,
	ssh_public_key TEXT,

	-- Agent connection
	agent_endpoint TEXT,

	-- Configuration
	workload_type TEXT NOT NULL,
	reservation_hours INTEGER NOT NULL,
	hard_max_override INTEGER NOT NULL DEFAULT 0,
	idle_threshold_minutes INTEGER NOT NULL DEFAULT 0,
	storage_policy TEXT NOT NULL DEFAULT 'destroy',

	-- Cost tracking
	price_per_hour REAL NOT NULL,

	-- Timestamps
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	expires_at DATETIME NOT NULL,
	last_heartbeat DATETIME,
	stopped_at DATETIME
);
`

const migrationCosts = `
CREATE TABLE IF NOT EXISTS costs (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	consumer_id TEXT NOT NULL,
	provider TEXT NOT NULL,
	gpu_type TEXT NOT NULL,
	hour DATETIME NOT NULL,
	amount REAL NOT NULL,
	currency TEXT NOT NULL DEFAULT 'USD',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

	FOREIGN KEY (session_id) REFERENCES sessions(id)
);
`

const migrationConsumers = `
CREATE TABLE IF NOT EXISTS consumers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	api_key_hash TEXT NOT NULL,
	budget_limit REAL NOT NULL DEFAULT 0,
	webhook_url TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	current_spend REAL NOT NULL DEFAULT 0,
	alert_sent INTEGER NOT NULL DEFAULT 0
);
`

const migrationIndexes = `
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_sessions_consumer_id ON sessions(consumer_id);
CREATE INDEX IF NOT EXISTS idx_sessions_provider ON sessions(provider);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_costs_session_id ON costs(session_id);
CREATE INDEX IF NOT EXISTS idx_costs_consumer_id ON costs(consumer_id);
CREATE INDEX IF NOT EXISTS idx_costs_hour ON costs(hour);
CREATE INDEX IF NOT EXISTS idx_consumers_api_key_hash ON consumers(api_key_hash);
`

// migrationDuplicatePrevention adds a partial unique index to prevent duplicate active sessions
// for the same consumer and offer. This is a belt-and-suspenders approach to catch any race conditions.
const migrationDuplicatePrevention = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_consumer_offer_active
ON sessions(consumer_id, offer_id)
WHERE status IN ('pending', 'provisioning', 'running');
`

const migrationIdleTracking = `
ALTER TABLE sessions ADD COLUMN last_idle_seconds INTEGER NOT NULL DEFAULT 0;
`

const migrationAgentToken = `
ALTER TABLE sessions ADD COLUMN agent_token TEXT;
`

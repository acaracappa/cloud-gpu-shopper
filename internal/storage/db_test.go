package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_CreatesDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Verify file was created
	_, err = os.Stat(dbPath)
	assert.NoError(t, err)
}

func TestNew_CreatesParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nested", "dir", "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Verify directory structure was created
	_, err = os.Stat(filepath.Dir(dbPath))
	assert.NoError(t, err)
}

func TestNew_InvalidPath(t *testing.T) {
	// Try to create database in a location that doesn't allow writes
	// Using /dev/null as a path that will fail
	_, err := New("/dev/null/test.db")
	assert.Error(t, err)
}

func TestDB_Migrate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Run migrations
	err = db.Migrate(context.Background())
	require.NoError(t, err)

	// Verify tables exist
	tables := []string{"sessions", "costs", "consumers"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		assert.NoError(t, err, "table %s should exist", table)
		assert.Equal(t, table, name)
	}
}

func TestDB_Migrate_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Run migrations multiple times - should not error
	ctx := context.Background()
	err = db.Migrate(ctx)
	require.NoError(t, err)

	err = db.Migrate(ctx)
	require.NoError(t, err)

	err = db.Migrate(ctx)
	require.NoError(t, err)
}

func TestDB_Close(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)

	err = db.Close()
	assert.NoError(t, err)

	// Operations should fail after close
	err = db.Ping()
	assert.Error(t, err)
}

func TestDB_WALMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Check journal mode is WAL
	var mode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	require.NoError(t, err)
	assert.Equal(t, "wal", mode)
}

// Helper function to create test database
func newTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)

	err = db.Migrate(context.Background())
	require.NoError(t, err)

	t.Cleanup(func() {
		db.Close()
	})

	return db
}

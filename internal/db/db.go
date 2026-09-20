package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

// Open opens a SQLite database at the specified path and configures WAL mode and timeouts
func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database %q: %w", dbPath, err)
	}

	// SQLite supports one writer at a time, so limit open connections or pool appropriately
	conn.SetMaxOpenConns(10)
	conn.SetMaxIdleConns(5)

	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	database := &DB{DB: conn}
	if err := database.Migrate(); err != nil {
		database.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return database, nil
}

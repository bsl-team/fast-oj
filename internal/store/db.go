package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var ErrNotFound = errors.New("not found")

// notFound turns sql.ErrNoRows into a single sentinel the API layer can map to
// 404 without importing database/sql everywhere.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func nowMS() int64 { return time.Now().UnixMilli() }

type DB struct {
	SQL  *sql.DB
	Path string
}

func dsn(path string) string {
	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"temp_store(MEMORY)",
		"mmap_size(268435456)",
	} {
		q.Add("_pragma", p)
	}
	return "file:" + filepath.ToSlash(filepath.Clean(path)) + "?" + q.Encode()
}

func Open(ctx context.Context, path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// WAL readers run in parallel; a single writer avoids SQLITE_BUSY churn.
	sqldb.SetMaxOpenConns(8)
	sqldb.SetMaxIdleConns(8)
	sqldb.SetConnMaxLifetime(time.Hour)

	db := &DB{SQL: sqldb, Path: path}
	if err := db.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("connect %s: %w", path, err)
	}
	if err := db.migrate(ctx); err != nil {
		sqldb.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error { return db.SQL.Close() }

func (db *DB) PingContext(ctx context.Context) error { return db.SQL.PingContext(ctx) }

func (db *DB) JournalMode(ctx context.Context) (string, error) {
	var mode string
	if err := db.SQL.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return "", err
	}
	return mode, nil
}

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.SQL.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT    PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("migration bookkeeping: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var applied int
		err := db.SQL.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE name = ?`, name).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check migration %s: %w", name, err)
		}

		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		err = db.InTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("apply %s: %w", name, err)
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, name, nowMS())
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) InTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

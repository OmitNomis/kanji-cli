// Package store owns both SQLite databases: the prebuilt, read-only dictionary
// and the small writable progress file. They are opened on a single connection
// with the dictionary attached, so queries can join review state against
// dictionary rows in one statement.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed sql/*.sql
var schemaFS embed.FS

// DictAlias is the schema name the dictionary is attached under.
const DictAlias = "dict"

// Store is a handle on the two databases. It holds exactly one connection:
// ATTACH is per-connection state, and a single-user CLI has no use for a pool.
type Store struct {
	db       *sql.DB
	dictPath string
	progPath string
}

// Open opens the progress database (creating it if needed), attaches the
// dictionary read-only alongside it, and applies the progress schema.
//
// The pragmas are tuned for the target hardware: a 4 MB page cache rather than
// SQLite's default, WAL so a review write does not block a read, and
// synchronous=NORMAL because losing the last answer to a power cut is an
// acceptable trade for not fsyncing a spinning disk every keystroke.
func Open(ctx context.Context, dictPath, progressPath string) (*Store, error) {
	if _, err := os.Stat(dictPath); err != nil {
		return nil, fmt.Errorf("dictionary not found at %s: %w (run `kanjidata build` to create it)", dictPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(progressPath), 0o755); err != nil {
		return nil, fmt.Errorf("create progress directory: %w", err)
	}

	dsn := progressPath + "?" + pragmas(
		"busy_timeout(5000)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"cache_size(-4000)",
		"foreign_keys(OFF)",
	)

	db := sql.OpenDB(attachingConnector(dsn, dictPath))

	// A single-user CLI has no use for a pool, and serialising on one connection
	// keeps two queries from contending for the same database lock.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	s := &Store{db: db, dictPath: dictPath, progPath: progressPath}

	if err := s.applySchema(ctx, "sql/progress.sql"); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.verifyDict(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenDict opens a dictionary database for writing. Used only by the importer,
// which trades every durability guarantee for speed: the file is rebuilt from
// scratch on failure anyway.
//
// temp_store is deliberately left at its default of FILE. The build ends with a
// VACUUM, and SQLite builds the vacuumed copy in temp storage, so an in-memory
// temp store would mean holding the whole dictionary in RAM to compact it.
func OpenDict(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create dictionary directory: %w", err)
	}

	dsn := path + "?" + pragmas(
		"busy_timeout(5000)",
		"journal_mode(OFF)",
		"synchronous(OFF)",
		"cache_size(-65536)",
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open dictionary database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, dictPath: path}
	if err := s.applySchema(ctx, "sql/dict.sql"); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// DB exposes the underlying handle for the importer's bulk inserts.
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the connection.
func (s *Store) Close() error { return s.db.Close() }

// applySchema runs one of the embedded .sql files. The statements are all
// CREATE ... IF NOT EXISTS, so this is safe on every open.
func (s *Store) applySchema(ctx context.Context, name string) error {
	ddl, err := schemaFS.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read embedded schema %s: %w", name, err)
	}
	if _, err := s.db.ExecContext(ctx, string(ddl)); err != nil {
		return fmt.Errorf("apply schema %s: %w", name, err)
	}
	return nil
}

// verifyDict makes sure the attached file is actually a built dictionary and
// not an empty placeholder, which otherwise fails much later with a confusing
// "no such table" error.
func (s *Store) verifyDict(ctx context.Context) error {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+DictAlias+`.item`).Scan(&n)
	if err != nil {
		return fmt.Errorf("dictionary at %s is not usable: %w (run `kanjidata build`)", s.dictPath, err)
	}
	if n == 0 {
		return fmt.Errorf("dictionary at %s has no review items (run `kanjidata build`)", s.dictPath)
	}
	return nil
}

// pragmas assembles modernc.org/sqlite's _pragma query parameters, which are
// re-applied to every new connection rather than just the first.
func pragmas(list ...string) string {
	out := ""
	for i, p := range list {
		if i > 0 {
			out += "&"
		}
		out += "_pragma=" + p
	}
	return out
}

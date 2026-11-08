// Package db opens the Postgres pool, runs transactions and applies per-module migrations.
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is satisfied by *pgxpool.Pool, *pgxpool.Conn and pgx.Tx, so query helpers work both
// inside and outside a transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Connect opens a pool and checks that the database answers.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// InTx runs fn in a transaction. It commits when fn returns nil and rolls back otherwise.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// IsUniqueViolation reports whether err is a unique-constraint violation, optionally on a
// specific constraint.
func IsUniqueViolation(err error, constraint string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && (constraint == "" || pg.ConstraintName == constraint)
}

// IsNotFound reports whether err means "no rows".
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Migration is one SQL file of a module.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations reads files named NNNN_description.sql from the root of fsys, sorted by
// version. Versions must be unique and positive.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("db: read migrations: %w", err)
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".sql" {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(prefix)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("db: migration %q must be named NNNN_description.sql", e.Name())
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("db: migrations %q and %q share version %d", prev, e.Name(), v)
		}
		seen[v] = e.Name()
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: e.Name(), SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate applies the module's migrations that have not run yet, each in its own transaction,
// and records them in schema_migrations. A Postgres advisory lock makes concurrent starts
// (several app copies) wait for each other instead of racing.
// It returns the names of the migrations it applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool, module string, fsys fs.FS) ([]string, error) {
	migs, err := LoadMigrations(fsys)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", module, err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquire: %w", err)
	}
	defer conn.Release()

	const lockID = 7_406_211_001 // arbitrary, constant for every app built on the core
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return nil, fmt.Errorf("db: lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		module     text        NOT NULL,
		version    integer     NOT NULL,
		name       text        NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now(),
		PRIMARY KEY (module, version))`); err != nil {
		return nil, fmt.Errorf("db: create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations WHERE module = $1", module)
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	for _, v := range versions {
		applied[v] = true
	}

	var done []string
	for _, m := range migs {
		if applied[m.Version] {
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (module, version, name) VALUES ($1, $2, $3)", module, m.Version, m.Name)
			return err
		})
		if err != nil {
			return done, fmt.Errorf("db: %s/%s: %w", module, m.Name, err)
		}
		done = append(done, module+"/"+m.Name)
	}
	return done, nil
}

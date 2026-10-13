// Package store is the PostgreSQL persistence layer (Cloud SQL in production).
//
// Every query lives in this package. Business code receives a Q value that
// works either on the connection pool or inside a transaction, so the same
// query methods are reused in both contexts (sqlc-like pattern, hand-written).
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a single-row lookup finds nothing.
var ErrNotFound = errors.New("not found")

// DBTX is satisfied by both *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Q exposes every query method on top of a DBTX.
type Q struct{ db DBTX }

// DB owns the connection pool.
type DB struct {
	Pool *pgxpool.Pool
	Q
}

// Advisory lock keys. Seat-consuming operations are serialised with LockPool
// so two instances can never hand out the same last seat.
const (
	LockPool       int64 = 0x6763616C6D01 // "gcalm" + 01
	LockMigrations int64 = 0x6763616C6D02
	lockJobsBase   int64 = 0x6763616C6D10 // + hash(job name)
)

// Open connects to PostgreSQL and checks connectivity.
func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if cfg.MaxConns < 8 {
		cfg.MaxConns = 8
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database ping: %w", err)
	}
	return &DB{Pool: pool, Q: Q{db: pool}}, nil
}

// Close releases the pool.
func (d *DB) Close() { d.Pool.Close() }

// Tx runs fn inside a transaction (committed if fn returns nil).
func (d *DB) Tx(ctx context.Context, fn func(Q) error) error {
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error { return fn(Q{db: tx}) })
}

// WithLock runs fn while holding a session-level advisory lock. It blocks
// until the lock is available or ctx is done.
func (d *DB) WithLock(ctx context.Context, key int64, fn func() error) error {
	conn, err := d.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer func() {
		// Unlock with a fresh context: ctx may already be cancelled.
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			// The connection is unusable, destroy it so the lock is released.
			slog.Error("advisory unlock failed, closing connection", "err", err)
			_ = conn.Conn().Close(uctx)
		}
	}()
	return fn()
}

// TryWithLock runs fn only if the advisory lock is free. ran=false means
// another process holds it (used by jobs to avoid overlapping runs).
func (d *DB) TryWithLock(ctx context.Context, key int64, fn func() error) (ran bool, err error) {
	conn, err := d.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			_ = conn.Conn().Close(uctx)
		}
	}()
	return true, fn()
}

// JobLockKey returns the advisory lock key of a job.
func JobLockKey(job string) int64 {
	var h int64 = 7
	for _, c := range job {
		h = h*31 + int64(c)
	}
	return lockJobsBase + (h & 0xFFFF)
}

// Migrate applies the embedded SQL migrations in lexical order. Each file is
// applied once, inside a transaction, under an advisory lock so concurrent
// instances starting together do not race.
func (d *DB) Migrate(ctx context.Context) error {
	return d.WithLock(ctx, LockMigrations, func() error {
		if _, err := d.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
			return err
		}
		files, err := fs.Glob(migrationsFS, "migrations/*.sql")
		if err != nil {
			return err
		}
		sort.Strings(files)
		for _, f := range files {
			version := strings.TrimSuffix(strings.TrimPrefix(f, "migrations/"), ".sql")
			var exists bool
			if err := d.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			sqlBytes, err := migrationsFS.ReadFile(f)
			if err != nil {
				return err
			}
			if err := pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
					return fmt.Errorf("migration %s: %w", version, err)
				}
				_, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES ($1)", version)
				return err
			}); err != nil {
				return err
			}
			slog.Info("migration applied", "version", version)
		}
		return nil
	})
}

// --- small helpers -----------------------------------------------------------

// one scans exactly one row into T using `db` struct tags.
func one[T any](ctx context.Context, q DBTX, sql string, args ...any) (*T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[T])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

// many scans all rows into a slice of T.
func many[T any](ctx context.Context, q DBTX, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
}

// scalar returns a single value.
func scalar[T any](ctx context.Context, q DBTX, sql string, args ...any) (T, error) {
	var v T
	err := q.QueryRow(ctx, sql, args...).Scan(&v)
	return v, err
}

// IsUniqueViolation reports whether err is a PostgreSQL unique violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

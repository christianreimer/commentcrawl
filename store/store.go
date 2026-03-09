// Package store provides SQLite persistence for discovery candidates,
// verification results, and comment pages. Uses sqlc for query generation.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	"github.com/christianreimer/commentcrawl/discovery"
	"github.com/christianreimer/commentcrawl/store/db"
	"github.com/christianreimer/commentcrawl/verification"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed db/migrations/001_initial.sql
var migrationSQL string

// DisqusCandidate represents a domain found with Disqus integration via WAT scanning.
type DisqusCandidate struct {
	Domain          string
	Hostname        string
	SampleURL       string
	DisqusShortname string
}

// DB wraps a SQLite database connection and sqlc queries.
type DB struct {
	conn    *sql.DB
	queries *db.Queries
}

// Open opens (or creates) a SQLite database at path and runs migrations.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if _, err := conn.Exec(migrationSQL); err != nil {
		conn.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &DB{conn: conn, queries: db.New(conn)}, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}

// InsertCandidates writes candidates to the database, skipping duplicates.
func (d *DB) InsertCandidates(ctx context.Context, candidates []discovery.Candidate) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	q := d.queries.WithTx(tx)
	for _, c := range candidates {
		if err := q.InsertCandidate(ctx, db.InsertCandidateParams{
			Domain:    c.Domain,
			Hostname:  c.Hostname,
			SampleUrl: c.SampleURL,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertResults writes verification results, replacing any existing rows.
func (d *DB) InsertResults(ctx context.Context, results []verification.Result) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	q := d.queries.WithTx(tx)
	for _, r := range results {
		if err := q.UpsertResult(ctx, db.UpsertResultParams{
			Domain:           r.Domain,
			WpConfirmed:      boolToInt64(r.WPConfirmed),
			CommentsEndpoint: boolToInt64(r.CommentsEndpoint),
			CommentCountHint: int64(r.CommentCountHint),
			ApiRoot:          r.APIRoot,
			Error:            r.Error,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertPages writes pages, replacing any existing rows.
func (d *DB) InsertPages(ctx context.Context, pages []verification.Page) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	q := d.queries.WithTx(tx)
	for _, p := range pages {
		if err := q.UpsertPage(ctx, db.UpsertPageParams{
			Domain:               p.Domain,
			PostID:               int64(p.PostID),
			Url:                  p.URL,
			Title:                p.Title,
			CommentCountInSample: int64(p.CommentCountInSample),
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertDisqusCandidates writes Disqus candidates to the database, skipping duplicates.
func (d *DB) InsertDisqusCandidates(ctx context.Context, candidates []DisqusCandidate) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	q := d.queries.WithTx(tx)
	for _, c := range candidates {
		if err := q.InsertDisqusCandidate(ctx, db.InsertDisqusCandidateParams{
			Domain:          c.Domain,
			Hostname:        c.Hostname,
			SampleUrl:       c.SampleURL,
			DisqusShortname: c.DisqusShortname,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReadUnverifiedDomains returns WP candidate domains that have no result row yet.
func (d *DB) ReadUnverifiedDomains(ctx context.Context) ([]string, error) {
	return d.queries.ListUnverifiedDomains(ctx)
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

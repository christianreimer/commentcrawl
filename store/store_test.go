package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/christianreimer/commentcrawl/discovery"
	"github.com/christianreimer/commentcrawl/verification"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAndClose(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestInsertCandidates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	candidates := []discovery.Candidate{
		{Domain: "example.com", Hostname: "www.example.com", SampleURL: "https://example.com/wp-json/"},
		{Domain: "test.org", Hostname: "test.org", SampleURL: "https://test.org/wp-content/"},
	}

	if err := db.InsertCandidates(ctx, candidates); err != nil {
		t.Fatalf("InsertCandidates: %v", err)
	}

	// Insert duplicates — should succeed (INSERT OR IGNORE).
	dupes := []discovery.Candidate{
		{Domain: "example.com", Hostname: "www.example.com", SampleURL: "https://example.com/other"},
	}
	if err := db.InsertCandidates(ctx, dupes); err != nil {
		t.Fatalf("InsertCandidates (duplicates): %v", err)
	}
}

func TestInsertDisqusCandidates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	candidates := []DisqusCandidate{
		{Domain: "blog.com", Hostname: "blog.com", SampleURL: "https://blog.com/post", DisqusShortname: "blogshort"},
		{Domain: "news.io", Hostname: "news.io", SampleURL: "https://news.io/article", DisqusShortname: "newsshort"},
	}

	if err := db.InsertDisqusCandidates(ctx, candidates); err != nil {
		t.Fatalf("InsertDisqusCandidates: %v", err)
	}

	// Duplicates should be ignored.
	if err := db.InsertDisqusCandidates(ctx, candidates[:1]); err != nil {
		t.Fatalf("InsertDisqusCandidates (duplicates): %v", err)
	}
}

func TestInsertResults(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	results := []verification.Result{
		{
			Domain:           "example.com",
			WPConfirmed:      true,
			CommentsEndpoint: true,
			CommentCountHint: 42,
			APIRoot:          "https://example.com/wp-json/wp/v2",
		},
		{
			Domain: "blog.com",
			Error:  "no WordPress API header",
		},
	}

	if err := db.InsertResults(ctx, results); err != nil {
		t.Fatalf("InsertResults: %v", err)
	}
}

func TestInsertPages(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	pages := []verification.Page{
		{Domain: "example.com", PostID: 1, URL: "https://example.com/hello", Title: "Hello World", CommentCountInSample: 10},
		{Domain: "example.com", PostID: 2, URL: "https://example.com/bye", Title: "Goodbye", CommentCountInSample: 5},
	}

	if err := db.InsertPages(ctx, pages); err != nil {
		t.Fatalf("InsertPages: %v", err)
	}
}

func TestReadUnverifiedDomains(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// WP candidates: A, B, C
	wpCandidates := []discovery.Candidate{
		{Domain: "a.com", Hostname: "a.com", SampleURL: "https://a.com/wp-json/"},
		{Domain: "b.com", Hostname: "b.com", SampleURL: "https://b.com/wp-json/"},
		{Domain: "c.com", Hostname: "c.com", SampleURL: "https://c.com/wp-json/"},
	}
	if err := db.InsertCandidates(ctx, wpCandidates); err != nil {
		t.Fatalf("InsertCandidates: %v", err)
	}

	// Mark A as verified by inserting a result.
	results := []verification.Result{
		{Domain: "a.com", WPConfirmed: true, CommentsEndpoint: true, CommentCountHint: 5},
	}
	if err := db.InsertResults(ctx, results); err != nil {
		t.Fatalf("InsertResults: %v", err)
	}

	// Unverified should be B, C (sorted).
	domains, err := db.ReadUnverifiedDomains(ctx)
	if err != nil {
		t.Fatalf("ReadUnverifiedDomains: %v", err)
	}

	expected := []string{"b.com", "c.com"}
	if len(domains) != len(expected) {
		t.Fatalf("got %d domains, want %d: %v", len(domains), len(expected), domains)
	}
	for i, d := range domains {
		if d != expected[i] {
			t.Errorf("domain[%d] = %q, want %q", i, d, expected[i])
		}
	}
}

func TestInsertResultsUpsert(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Insert initial result.
	first := []verification.Result{
		{
			Domain:           "example.com",
			WPConfirmed:      true,
			CommentsEndpoint: false,
			CommentCountHint: 0,
			APIRoot:          "https://example.com/wp-json/wp/v2",
		},
	}
	if err := db.InsertResults(ctx, first); err != nil {
		t.Fatalf("InsertResults (first): %v", err)
	}

	// Upsert with different values.
	second := []verification.Result{
		{
			Domain:           "example.com",
			WPConfirmed:      true,
			CommentsEndpoint: true,
			CommentCountHint: 99,
			APIRoot:          "https://example.com/wp-json/wp/v2",
		},
	}
	if err := db.InsertResults(ctx, second); err != nil {
		t.Fatalf("InsertResults (second): %v", err)
	}

	// Verify the second write replaced the first by reading results via the
	// underlying queries. If domain has a result, it should NOT appear in
	// unverified domains.

	// Insert a candidate for example.com so it would appear if not verified.
	if err := db.InsertCandidates(ctx, []discovery.Candidate{
		{Domain: "example.com", Hostname: "example.com", SampleURL: "https://example.com/"},
	}); err != nil {
		t.Fatalf("InsertCandidates: %v", err)
	}

	domains, err := db.ReadUnverifiedDomains(ctx)
	if err != nil {
		t.Fatalf("ReadUnverifiedDomains: %v", err)
	}
	if len(domains) != 0 {
		t.Errorf("expected 0 unverified domains after upsert, got %v", domains)
	}
}

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/christianreimer/commentcrawl/store"
	"github.com/christianreimer/commentcrawl/verification"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

func verifyCmd() *cobra.Command {
	var (
		dbPath  string
		workers int
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "verify-wp",
		Short: "Verify WordPress domains and find pages with comments",
		Long: `Reads unverified WordPress candidate domains from the database and
checks each one for a working WP REST API comments endpoint. Domains
with confirmed comments are saved back with their comment counts and
sample pages.

Run discover-wp first to populate the candidate list.

Examples:
  # Verify all unverified domains in the default database
  commentcrawl verify-wp

  # Use more workers for faster verification
  commentcrawl verify-wp --workers 30 --timeout 5s

  # Verify domains in a specific database
  commentcrawl verify-wp --db results.db`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			domains, err := db.ReadUnverifiedDomains(ctx)
			if err != nil {
				return fmt.Errorf("read candidates: %w", err)
			}

			if len(domains) == 0 {
				fmt.Println("No unverified domains found. Run 'discover-wp' first.")
				return nil
			}

			return runVerification(ctx, db, domains, workers, timeout)
		},
	}

	cmd.Flags().StringVarP(&dbPath, "db", "d", "commentcrawl.db", "SQLite database path")
	cmd.Flags().IntVar(&workers, "workers", 15, "Concurrent HTTP workers")
	cmd.Flags().DurationVar(&timeout, "timeout", 8*time.Second, "Per-request timeout")

	return cmd
}

func runVerification(ctx context.Context, db *store.DB, domains []string, workers int, timeout time.Duration) error {
	bar := progressbar.NewOptions(len(domains),
		progressbar.OptionSetDescription("Verifying"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetPredictTime(true),
	)

	var done atomic.Int64
	results, pages := verification.VerifyAll(ctx, domains, verification.Options{
		Workers: workers,
		Timeout: timeout,
	}, func(r verification.Result, p []verification.Page) {
		done.Add(1)
		bar.Set(int(done.Load()))
	})
	bar.Finish()
	fmt.Println()

	// Save all results to database
	if err := db.InsertResults(ctx, results); err != nil {
		return fmt.Errorf("save results: %w", err)
	}
	if len(pages) > 0 {
		if err := db.InsertPages(ctx, pages); err != nil {
			return fmt.Errorf("save pages: %w", err)
		}
	}
	slog.Info("Results saved to database")

	// Filter confirmed WP sites
	var confirmed []verification.Result
	for _, r := range results {
		if r.CommentsEndpoint {
			confirmed = append(confirmed, r)
		}
	}
	sort.Slice(confirmed, func(i, j int) bool {
		return confirmed[i].CommentCountHint > confirmed[j].CommentCountHint
	})

	printSummary(confirmed, pages)
	return nil
}

func printSummary(confirmed []verification.Result, pages []verification.Page) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  Sites with WordPress comments: %d\n", len(confirmed))
	fmt.Println(strings.Repeat("=", 70))

	for i, r := range confirmed {
		if i >= 30 {
			break
		}
		fmt.Printf("  %-35s  comments=%-6d  %s\n",
			r.Domain, r.CommentCountHint, r.APIRoot)
	}

	if len(pages) > 0 {
		fmt.Printf("\n  Top pages with comments (%d total):\n", len(pages))
		fmt.Println(strings.Repeat("-", 70))
		for i, p := range pages {
			if i >= 30 {
				break
			}
			title := p.Title
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			fmt.Printf("  %-30s  %s\n", p.Domain, p.URL)
			fmt.Printf("  %30s  %s (comments: %d)\n", "", title, p.CommentCountInSample)
		}
	}

	fmt.Println(strings.Repeat("=", 70))
}

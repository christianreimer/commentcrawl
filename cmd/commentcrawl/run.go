package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/christianreimer/commentcrawl/discovery"
	"github.com/christianreimer/commentcrawl/store"
	"github.com/spf13/cobra"
)

func runCmd() *cobra.Command {
	var (
		crawl      string
		partitions int
		dbPath     string
		workers    int
		timeout    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run both stages: discover WordPress domains then verify comments",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Stage 1
			candidates, err := discovery.Discover(ctx, discovery.Options{
				Crawl:         crawl,
				MaxPartitions: partitions,
			}, nil)
			if err != nil {
				return err
			}

			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			if err := db.InsertCandidates(ctx, candidates); err != nil {
				return fmt.Errorf("save candidates: %w", err)
			}
			slog.Info("Candidates saved", "db", dbPath, "count", len(candidates))

			// Stage 2
			domains := make([]string, 0, len(candidates))
			for _, c := range candidates {
				if c.Domain != "" {
					domains = append(domains, c.Domain)
				}
			}

			return runVerification(ctx, db, domains, workers, timeout)
		},
	}

	cmd.Flags().StringVar(&crawl, "crawl", "CC-MAIN-2024-22", "Common Crawl crawl ID")
	cmd.Flags().IntVar(&partitions, "partitions", 5, "Number of Parquet partitions to scan")
	cmd.Flags().StringVarP(&dbPath, "db", "d", "commentcrawl.db", "SQLite database path")
	cmd.Flags().IntVar(&workers, "workers", 15, "Concurrent HTTP workers for Stage 2")
	cmd.Flags().DurationVar(&timeout, "timeout", 8*time.Second, "Per-request timeout for Stage 2")

	return cmd
}

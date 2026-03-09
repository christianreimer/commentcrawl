package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/christianreimer/commentcrawl/discovery"
	"github.com/christianreimer/commentcrawl/store"
	"github.com/spf13/cobra"
)

func discoverCmd() *cobra.Command {
	var (
		crawl      string
		partitions int
		dbPath     string
	)

	cmd := &cobra.Command{
		Use:   "discover-wp",
		Short: "Discover WordPress domains from Common Crawl",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

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
			fmt.Printf("\nStage 1 done. %d candidate domains saved to %s\n", len(candidates), dbPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&crawl, "crawl", "CC-MAIN-2024-22", "Common Crawl crawl ID")
	cmd.Flags().IntVar(&partitions, "partitions", 5, "Number of Parquet partitions to scan")
	cmd.Flags().StringVarP(&dbPath, "db", "d", "commentcrawl.db", "SQLite database path")

	return cmd
}

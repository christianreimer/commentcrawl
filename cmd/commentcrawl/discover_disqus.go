package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/christianreimer/commentcrawl/discovery/wat"
	"github.com/christianreimer/commentcrawl/store"
	"github.com/spf13/cobra"
)

func discoverDisqusCmd() *cobra.Command {
	var (
		crawl      string
		partitions int
		workers    int
		dbPath     string
	)

	cmd := &cobra.Command{
		Use:   "discover-disqus",
		Short: "Discover Disqus-enabled sites by scanning Common Crawl WAT files",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			candidates, err := wat.Discover(ctx, wat.Options{
				Crawl:         crawl,
				MaxPartitions: partitions,
				Workers:       workers,
			}, nil)
			if err != nil {
				return err
			}

			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			dbCandidates := make([]store.DisqusCandidate, len(candidates))
			for i, c := range candidates {
				dbCandidates[i] = store.DisqusCandidate{
					Domain:          c.Domain,
					Hostname:        c.Hostname,
					SampleURL:       c.SampleURL,
					DisqusShortname: c.DisqusShortname,
				}
			}

			if err := db.InsertDisqusCandidates(ctx, dbCandidates); err != nil {
				return fmt.Errorf("save disqus candidates: %w", err)
			}

			slog.Info("Disqus candidates saved", "db", dbPath, "count", len(candidates))
			fmt.Printf("\nDisqus discovery done. %d candidate domains saved to %s\n", len(candidates), dbPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&crawl, "crawl", "CC-MAIN-2024-22", "Common Crawl crawl ID")
	cmd.Flags().IntVar(&partitions, "partitions", 100, "Number of WAT files to scan")
	cmd.Flags().IntVar(&workers, "workers", 3, "Concurrent WAT file downloads")
	cmd.Flags().StringVarP(&dbPath, "db", "d", "commentcrawl.db", "SQLite database path")

	return cmd
}

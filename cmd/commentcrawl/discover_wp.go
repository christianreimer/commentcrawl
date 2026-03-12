package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/christianreimer/commentcrawl/discovery"
	"github.com/christianreimer/commentcrawl/store"
	"github.com/spf13/cobra"
)

func discoverCmd() *cobra.Command {
	var (
		crawl      string
		partitions int
		all        bool
		dbPath     string
		resume     bool
		delay      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "discover-wp",
		Short: "Discover WordPress domains from Common Crawl",
		Long: `Scans Common Crawl Parquet index files to find domains that use
WordPress comments. Discovered domains are saved to the SQLite database
for later verification with the verify-wp command.

Results are written to the database after each partition is scanned.

Use --partitions to scan a chunk at a time, then run again with --resume
to continue from where you left off.

Examples:
  # Scan 5 partitions (default)
  commentcrawl discover-wp

  # Scan more partitions for broader coverage
  commentcrawl discover-wp --partitions 50

  # Resume from where we left off, scan the next 10
  commentcrawl discover-wp --partitions 10 --resume

  # Scan all partitions (will resume if --resume is set)
  commentcrawl discover-wp --all --resume

  # Use a specific crawl and database
  commentcrawl discover-wp --crawl CC-MAIN-2024-22 --db results.db`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && cmd.Flags().Changed("partitions") {
				return fmt.Errorf("--all and --partitions are mutually exclusive")
			}
			ctx := context.Background()

			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			// Determine starting offset.
			offset := 0
			if resume {
				maxIdx, err := db.GetMaxWPScannedPartition(ctx, crawl)
				if err != nil {
					return fmt.Errorf("reading scan progress: %w", err)
				}
				if maxIdx >= 0 {
					offset = maxIdx + 1
					scanned, _ := db.CountWPScannedPartitions(ctx, crawl)
					slog.Info("Resuming scan", "crawl", crawl, "from_partition", offset, "previously_scanned", scanned)
				}
			}

			maxPartitions := partitions
			if all {
				maxPartitions = 0 // 0 means all remaining
			}

			var totalSaved atomic.Int64

			candidates, err := discovery.Discover(ctx, discovery.Options{
				Crawl:         crawl,
				MaxPartitions: maxPartitions,
				Offset:        offset,
				Delay:         delay,
			}, func(result discovery.PartitionResult, total int) {
				// Write to DB after each partition completes.
				if result.Err != nil {
					// Record the failed partition so we don't retry it on resume.
					if err := db.RecordWPScanProgress(ctx, crawl, result.Index, result.URL, 0); err != nil {
						slog.Error("Failed to record scan progress", "partition", result.Index, "error", err)
					}
					return
				}

				if err := db.InsertCandidatesWithProgress(ctx, result.Candidates, crawl, result.Index, result.URL); err != nil {
					slog.Error("Failed to save partition results",
						"partition", result.Index, "error", err)
					return
				}

				totalSaved.Add(int64(len(result.Candidates)))
				slog.Info("Partition saved to DB",
					"partition", result.Index,
					"of", total,
					"candidates", len(result.Candidates),
					"total_saved", totalSaved.Load())
			})
			if err != nil {
				return err
			}

			scanned, _ := db.CountWPScannedPartitions(ctx, crawl)
			fmt.Printf("\nWP discovery done. %d unique candidates (%d new this run). %d total partitions scanned for crawl %s.\n",
				len(candidates), totalSaved.Load(), scanned, crawl)
			return nil
		},
	}

	cmd.Flags().StringVar(&crawl, "crawl", "CC-MAIN-2024-22", "Common Crawl crawl ID")
	cmd.Flags().IntVar(&partitions, "partitions", 5, "Number of Parquet partitions to scan in this run")
	cmd.Flags().BoolVar(&all, "all", false, "Scan all remaining partitions")
	cmd.Flags().StringVarP(&dbPath, "db", "d", "commentcrawl.db", "SQLite database path")
	cmd.Flags().BoolVar(&resume, "resume", false, "Resume from last scanned partition")
	cmd.Flags().DurationVar(&delay, "delay", 2*time.Second, "Delay between partition queries (e.g. 2s, 500ms)")

	return cmd
}

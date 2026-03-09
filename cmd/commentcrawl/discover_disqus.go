package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/christianreimer/commentcrawl/discovery/wat"
	"github.com/christianreimer/commentcrawl/store"
	"github.com/spf13/cobra"
)

func discoverDisqusCmd() *cobra.Command {
	var (
		crawl      string
		partitions int
		all        bool
		workers    int
		dbPath     string
		resume     bool
	)

	cmd := &cobra.Command{
		Use:   "discover-disqus",
		Short: "Discover Disqus-enabled sites by scanning Common Crawl WAT files",
		Long: `Scans Common Crawl WAT files to discover Disqus-enabled sites.
Results are written to the database after each partition is scanned.

Use --partitions to scan a chunk at a time (e.g. 1000), then run
again with --resume to continue from where you left off.

Examples:
  # Scan first 1000 partitions
  commentcrawl discover-disqus --partitions 1000

  # Resume from where we left off, scan the next 1000
  commentcrawl discover-disqus --partitions 1000 --resume

  # Scan all partitions (will resume if --resume is set)
  commentcrawl discover-disqus --all --resume`,
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
				maxIdx, err := db.GetMaxScannedPartition(ctx, crawl)
				if err != nil {
					return fmt.Errorf("reading scan progress: %w", err)
				}
				if maxIdx >= 0 {
					offset = maxIdx + 1
					scanned, _ := db.CountScannedPartitions(ctx, crawl)
					slog.Info("Resuming scan", "crawl", crawl, "from_partition", offset, "previously_scanned", scanned)
				}
			}

			maxPartitions := partitions
			if all {
				maxPartitions = 0 // 0 means all remaining
			}

			var totalSaved atomic.Int64

			_, err = wat.DiscoverPartitioned(ctx, wat.Options{
				Crawl:         crawl,
				MaxPartitions: maxPartitions,
				Offset:        offset,
				Workers:       workers,
			}, func(result wat.PartitionResult, total int) {
				// Write to DB after each partition completes.
				if result.Err != nil {
					// Record the failed partition so we don't retry it on resume.
					if err := db.RecordScanProgress(ctx, crawl, result.Index, result.URL, 0); err != nil {
						slog.Error("Failed to record scan progress", "partition", result.Index, "error", err)
					}
					return
				}

				candidates := make([]store.DisqusCandidate, len(result.Candidates))
				for i, c := range result.Candidates {
					candidates[i] = store.DisqusCandidate{
						Domain:          c.Domain,
						Hostname:        c.Hostname,
						SampleURL:       c.SampleURL,
						DisqusShortname: c.DisqusShortname,
					}
				}

				if err := db.InsertDisqusCandidatesWithProgress(ctx, candidates, crawl, result.Index, result.URL); err != nil {
					slog.Error("Failed to save partition results",
						"partition", result.Index, "error", err)
					return
				}

				totalSaved.Add(int64(len(candidates)))
				slog.Info("Partition saved to DB",
					"partition", result.Index,
					"of", total,
					"candidates", len(candidates),
					"total_saved", totalSaved.Load())
			})
			if err != nil {
				return err
			}

			scanned, _ := db.CountScannedPartitions(ctx, crawl)
			fmt.Printf("\nDisqus discovery done. %d candidates saved. %d total partitions scanned for crawl %s.\n",
				totalSaved.Load(), scanned, crawl)
			return nil
		},
	}

	cmd.Flags().StringVar(&crawl, "crawl", "CC-MAIN-2024-22", "Common Crawl crawl ID")
	cmd.Flags().IntVar(&partitions, "partitions", 100, "Number of partitions to scan in this run")
	cmd.Flags().BoolVar(&all, "all", false, "Scan all remaining WAT files")
	cmd.Flags().IntVar(&workers, "workers", 3, "Concurrent WAT file downloads")
	cmd.Flags().StringVarP(&dbPath, "db", "d", "commentcrawl.db", "SQLite database path")
	cmd.Flags().BoolVar(&resume, "resume", false, "Resume from last scanned partition")

	return cmd
}

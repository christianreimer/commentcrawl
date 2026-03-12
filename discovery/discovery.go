// Package discovery implements Stage 1 of the WordPress comment finder pipeline:
// querying Common Crawl's columnar Parquet index via DuckDB to discover
// candidate WordPress domains.
package discovery

import (
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

// Candidate represents a WordPress domain found in Common Crawl.
type Candidate struct {
	Domain    string
	Hostname  string
	SampleURL string
}

// PartitionResult holds the outcome of scanning a single partition.
type PartitionResult struct {
	Index      int
	URL        string
	Candidates []Candidate
	Err        error
}

// Options controls Stage 1 behavior.
type Options struct {
	Crawl         string        // Common Crawl crawl ID, e.g. "CC-MAIN-2024-22"
	MaxPartitions int           // Number of parquet partitions to scan
	Offset        int           // Partition index to start from (for resume)
	Delay         time.Duration // Delay between partition queries
}

const maxRetries = 3

// retryBackoff returns an exponential backoff duration: 5s, 15s, 45s.
func retryBackoff(attempt int) time.Duration {
	d := 5 * time.Second
	for range attempt {
		d *= 3
	}
	return d
}

// FetchParquetURLs downloads the cc-index-table.paths.gz manifest for a crawl
// and returns up to maxPartitions HTTPS URLs for the warc subset parquet files,
// starting from the given offset.
func FetchParquetURLs(ctx context.Context, crawl string, maxPartitions, offset int) ([]string, error) {
	manifestURL := fmt.Sprintf(
		"https://data.commoncrawl.org/crawl-data/%s/cc-index-table.paths.gz",
		crawl,
	)
	slog.Info("Fetching partition manifest", "url", manifestURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create manifest request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest returned HTTP %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decompress manifest: %w", err)
	}
	defer gz.Close()

	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var warcPaths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.Contains(line, "subset=warc/") {
			warcPaths = append(warcPaths, line)
		}
	}

	if len(warcPaths) == 0 {
		return nil, fmt.Errorf("no warc subset partitions found in manifest")
	}

	sort.Strings(warcPaths)

	if offset >= len(warcPaths) {
		slog.Info("All partitions already scanned", "total", len(warcPaths), "offset", offset)
		return nil, nil
	}
	warcPaths = warcPaths[offset:]

	n := maxPartitions
	if n <= 0 || n > len(warcPaths) {
		n = len(warcPaths)
	}

	urls := make([]string, n)
	for i := 0; i < n; i++ {
		urls[i] = "https://data.commoncrawl.org/" + warcPaths[i]
	}
	slog.Info("Found warc partitions", "total", len(warcPaths)+offset, "offset", offset, "using", n)
	return urls, nil
}

// Discover queries Common Crawl parquet files via DuckDB over HTTPS and returns
// deduplicated WordPress candidate domains. The onPartition callback is invoked
// after each partition completes with the result for that partition.
func Discover(ctx context.Context, opts Options, onPartition func(result PartitionResult, total int)) ([]Candidate, error) {
	urls, err := FetchParquetURLs(ctx, opts.Crawl, opts.MaxPartitions, opts.Offset)
	if err != nil {
		return nil, err
	}

	if len(urls) == 0 {
		slog.Info("No partitions to scan")
		return nil, nil
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "INSTALL httpfs; LOAD httpfs"); err != nil {
		return nil, fmt.Errorf("install httpfs: %w", err)
	}

	slog.Info("Stage 1 — Querying Common Crawl columnar index",
		"crawl", opts.Crawl, "partitions", len(urls), "offset", opts.Offset)

	seen := make(map[string]Candidate)

	for i, url := range urls {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		absIdx := opts.Offset + i
		slog.Info("Scanning partition", "n", i+1, "total", len(urls), "index", absIdx, "url", url)

		if strings.ContainsAny(url, `'";\`) {
			slog.Warn("Skipping partition with suspicious URL", "url", url)
			if onPartition != nil {
				onPartition(PartitionResult{Index: absIdx, URL: url, Err: fmt.Errorf("suspicious URL")}, len(urls))
			}
			continue
		}

		query := fmt.Sprintf(`
			SELECT DISTINCT
				url_host_registered_domain AS domain,
				url_host_name              AS hostname,
				url                        AS sample_url
			FROM read_parquet('%s', hive_partitioning=false)
			WHERE fetch_status = 200
			  AND content_mime_detected = 'text/html'
			  AND (
				    url_path LIKE '%%/wp-json/%%'
				 OR url_path LIKE '%%/wp-content/%%'
				 OR url_path LIKE '%%/wp-includes/%%'
				 OR url_host_registered_domain = 'wordpress.com'
			  )
			LIMIT 50000
		`, url)

		var partCandidates []Candidate
		var queryErr error

		for attempt := range maxRetries {
			rows, err := db.QueryContext(ctx, query)
			if err != nil {
				queryErr = err
				backoff := retryBackoff(attempt)
				slog.Warn("Partition query failed, retrying",
					"index", absIdx, "attempt", attempt+1, "of", maxRetries,
					"backoff", backoff, "error", err)
				select {
				case <-time.After(backoff):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}

			queryErr = nil
			partCandidates = nil
			for rows.Next() {
				var c Candidate
				var domain, hostname, sampleURL sql.NullString
				if err := rows.Scan(&domain, &hostname, &sampleURL); err != nil {
					slog.Warn("Row scan error", "error", err)
					continue
				}
				if !domain.Valid || domain.String == "" {
					continue
				}
				c.Domain = domain.String
				c.Hostname = hostname.String
				c.SampleURL = sampleURL.String

				if _, exists := seen[c.Domain]; !exists {
					seen[c.Domain] = c
					partCandidates = append(partCandidates, c)
				}
			}
			rows.Close()
			break
		}

		if queryErr != nil {
			slog.Warn("Partition failed after retries", "index", absIdx, "error", queryErr)
			if onPartition != nil {
				onPartition(PartitionResult{Index: absIdx, URL: url, Err: queryErr}, len(urls))
			}
			continue
		}

		slog.Info("Partition results", "n", i+1, "index", absIdx, "found", len(partCandidates))
		if onPartition != nil {
			onPartition(PartitionResult{Index: absIdx, URL: url, Candidates: partCandidates}, len(urls))
		}

		if opts.Delay > 0 && i < len(urls)-1 {
			slog.Debug("Sleeping between partitions", "delay", opts.Delay)
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	candidates := make([]Candidate, 0, len(seen))
	for _, c := range seen {
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Domain < candidates[j].Domain
	})

	slog.Info("Stage 1 complete", "unique_candidates", len(candidates))
	return candidates, nil
}

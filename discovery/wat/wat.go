// Package wat implements Disqus discovery by scanning Common Crawl WAT
// (Web Archive Transformation) files. WAT files contain pre-extracted HTML
// metadata as JSON, including script src URLs. By scanning for disqus.com
// references, we discover Disqus-enabled domains without live-checking.
package wat

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var (
	// Matches //SHORTNAME.disqus.com/embed.js in script src URLs.
	disqusEmbedRe = regexp.MustCompile(`//(\w[\w-]*)\.disqus\.com/embed\.js`)
	// Matches SHORTNAME.disqus.com in any URL (links, scripts, etc.).
	disqusSubdomainRe = regexp.MustCompile(`(?:^|//)(\w[\w-]*)\.disqus\.com`)
)

// DisqusCandidate represents a domain found with a Disqus embed in WAT metadata.
type DisqusCandidate struct {
	Domain          string
	Hostname        string
	SampleURL       string
	DisqusShortname string
}

// Options controls WAT-based Disqus discovery.
type Options struct {
	Crawl         string // Common Crawl crawl ID, e.g. "CC-MAIN-2024-22"
	MaxPartitions int    // Number of WAT files to scan (0 = all)
	Offset        int    // Partition index to start from (for resuming)
	Workers       int    // Concurrent WAT file downloads
}

func (o *Options) defaults() {
	if o.Crawl == "" {
		o.Crawl = "CC-MAIN-2024-22"
	}
	if o.MaxPartitions < 0 {
		o.MaxPartitions = 0
	}
	if o.Workers <= 0 {
		o.Workers = 3
	}
}

// FetchWATURLs downloads the wat.paths.gz manifest for a crawl and returns
// up to maxPartitions HTTPS URLs for WAT files.
func FetchWATURLs(ctx context.Context, crawl string, maxPartitions int) ([]string, error) {
	manifestURL := fmt.Sprintf(
		"https://data.commoncrawl.org/crawl-data/%s/wat.paths.gz",
		crawl,
	)
	slog.Info("Fetching WAT manifest", "url", manifestURL)

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

	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasSuffix(line, ".warc.wat.gz") {
			paths = append(paths, line)
		}
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("no WAT files found in manifest")
	}

	sort.Strings(paths)

	n := len(paths)
	if maxPartitions > 0 && maxPartitions < n {
		n = maxPartitions
	}

	urls := make([]string, n)
	for i := 0; i < n; i++ {
		urls[i] = "https://data.commoncrawl.org/" + paths[i]
	}
	slog.Info("Found WAT files", "total", len(paths), "using", n)
	return urls, nil
}

// ScanWATFile streams a single WAT file and extracts Disqus candidates.
func ScanWATFile(ctx context.Context, watURL string) ([]DisqusCandidate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, watURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CommentCrawl/1.0)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch WAT file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("WAT file returned HTTP %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decompress WAT: %w", err)
	}
	defer gz.Close()

	return parseWATRecords(gz)
}

// parseWATRecords reads WARC records from a WAT stream and extracts Disqus candidates.
func parseWATRecords(r io.Reader) ([]DisqusCandidate, error) {
	seen := make(map[string]DisqusCandidate)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 2*1024*1024), 10*1024*1024)

	var targetURI string

	for scanner.Scan() {
		line := scanner.Bytes()

		// Capture WARC-Target-URI from WARC headers
		if bytes.HasPrefix(line, []byte("WARC-Target-URI:")) {
			targetURI = strings.TrimSpace(string(line[len("WARC-Target-URI:"):]))
			continue
		}

		// Fast pre-filter: skip lines that don't contain disqus.com
		if !bytes.Contains(line, []byte("disqus.com")) {
			continue
		}

		// Try to parse as JSON envelope
		shortname := extractDisqusFromJSON(line)
		if shortname == "" {
			continue
		}

		if targetURI == "" {
			continue
		}

		parsed, err := url.Parse(targetURI)
		if err != nil {
			continue
		}

		domain := extractRegisteredDomain(parsed.Hostname())
		if domain == "" {
			continue
		}

		if _, exists := seen[domain]; !exists {
			seen[domain] = DisqusCandidate{
				Domain:          domain,
				Hostname:        parsed.Hostname(),
				SampleURL:       targetURI,
				DisqusShortname: shortname,
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan WAT records: %w", err)
	}

	candidates := make([]DisqusCandidate, 0, len(seen))
	for _, c := range seen {
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Domain < candidates[j].Domain
	})
	return candidates, nil
}

// watEnvelope is the minimal JSON structure for extracting script and link URLs from WAT records.
type watEnvelope struct {
	Envelope struct {
		PayloadMetadata struct {
			HTTPResponseMetadata struct {
				HTMLMetadata struct {
					Scripts []struct {
						URL string `json:"url"`
					} `json:"Scripts"`
					Links []struct {
						URL string `json:"url"`
					} `json:"Links"`
				} `json:"HTML-Metadata"`
			} `json:"HTTP-Response-Metadata"`
		} `json:"Payload-Metadata"`
	} `json:"Envelope"`
}

// extractDisqusFromJSON parses a WAT JSON line and returns the Disqus shortname.
// Checks both Scripts (for static <script src>) and Links (for noscript fallback
// links like SHORTNAME.disqus.com/?url=...).
func extractDisqusFromJSON(data []byte) string {
	var env watEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return ""
	}

	hm := env.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata

	// Check script src URLs for embed.js pattern
	for _, s := range hm.Scripts {
		if m := disqusEmbedRe.FindStringSubmatch(s.URL); len(m) > 1 {
			return m[1]
		}
	}

	// Check link URLs for SHORTNAME.disqus.com subdomains
	for _, l := range hm.Links {
		if shortname := extractShortname(l.URL); shortname != "" {
			return shortname
		}
	}

	return ""
}

// extractShortname pulls the Disqus shortname from a URL like
// "http://SHORTNAME.disqus.com/..." or "//SHORTNAME.disqus.com/...".
// Returns empty string if not a valid Disqus subdomain URL.
func extractShortname(rawURL string) string {
	m := disqusSubdomainRe.FindStringSubmatch(rawURL)
	if len(m) < 2 {
		return ""
	}
	name := strings.ToLower(m[1])
	// Skip bare disqus.com (no subdomain) and Disqus's own subdomains
	switch name {
	case "www", "disqus", "help", "blog", "docs", "https", "http":
		return ""
	}
	return name
}

// extractRegisteredDomain returns a best-effort registered domain from a hostname.
// For example, "www.example.com" -> "example.com", "blog.example.co.uk" -> "example.co.uk".
func extractRegisteredDomain(hostname string) string {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	if hostname == "" {
		return ""
	}

	parts := strings.Split(hostname, ".")
	if len(parts) < 2 {
		return hostname
	}

	// Handle common two-part TLDs
	twoPartTLDs := map[string]bool{
		"co.uk": true, "co.jp": true, "co.kr": true, "co.nz": true,
		"co.za": true, "co.in": true, "co.id": true, "co.il": true,
		"com.au": true, "com.br": true, "com.cn": true, "com.mx": true,
		"com.tw": true, "com.sg": true, "com.hk": true, "com.ar": true,
		"com.tr": true, "org.uk": true, "org.au": true, "net.au": true,
		"ac.uk": true, "gov.uk": true,
	}

	if len(parts) >= 3 {
		possibleTLD := parts[len(parts)-2] + "." + parts[len(parts)-1]
		if twoPartTLDs[possibleTLD] {
			if len(parts) >= 3 {
				return parts[len(parts)-3] + "." + possibleTLD
			}
			return possibleTLD
		}
	}

	return parts[len(parts)-2] + "." + parts[len(parts)-1]
}

// PartitionResult contains the results of scanning a single WAT partition.
type PartitionResult struct {
	Index      int               // Partition index in the full URL list
	URL        string            // WAT file URL
	Candidates []DisqusCandidate // Candidates found in this partition
	Err        error             // Non-nil if scanning failed
}

// Discover scans WAT files from Common Crawl and returns deduplicated
// Disqus candidates. The onPartition callback is invoked after each WAT
// file completes with the partition index, total count, and candidates found.
func Discover(ctx context.Context, opts Options, onPartition func(i, total, found int)) ([]DisqusCandidate, error) {
	results, err := DiscoverPartitioned(ctx, opts, nil)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]DisqusCandidate)
	for _, r := range results {
		for _, c := range r.Candidates {
			if _, exists := seen[c.Domain]; !exists {
				seen[c.Domain] = c
			}
		}
	}

	candidates := make([]DisqusCandidate, 0, len(seen))
	for _, c := range seen {
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Domain < candidates[j].Domain
	})
	return candidates, nil
}

// DiscoverPartitioned scans WAT files and calls onResult after each partition
// completes. The offset field in Options controls which partition index to
// start from (for resuming). Returns all partition results.
func DiscoverPartitioned(ctx context.Context, opts Options, onResult func(result PartitionResult, total int)) ([]PartitionResult, error) {
	opts.defaults()

	// Fetch all URLs (we need the full list for consistent indexing).
	urls, err := FetchWATURLs(ctx, opts.Crawl, 0)
	if err != nil {
		return nil, err
	}

	// Determine the range of partitions to scan.
	startIdx := opts.Offset
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx >= len(urls) {
		slog.Info("Offset beyond available partitions", "offset", startIdx, "total", len(urls))
		return nil, nil
	}

	endIdx := len(urls)
	if opts.MaxPartitions > 0 && startIdx+opts.MaxPartitions < endIdx {
		endIdx = startIdx + opts.MaxPartitions
	}

	scanURLs := urls[startIdx:endIdx]

	slog.Info("Disqus discovery — Scanning WAT files",
		"crawl", opts.Crawl, "offset", startIdx, "count", len(scanURLs),
		"total_available", len(urls), "workers", opts.Workers)

	var (
		mu      sync.Mutex
		results []PartitionResult
		sem     = make(chan struct{}, opts.Workers)
		wg      sync.WaitGroup
	)

	for i, watURL := range scanURLs {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(localIdx int, absIdx int, u string) {
			defer wg.Done()
			defer func() { <-sem }()

			candidates, err := ScanWATFile(ctx, u)

			pr := PartitionResult{
				Index:      absIdx,
				URL:        u,
				Candidates: candidates,
				Err:        err,
			}

			if err != nil {
				slog.Warn("WAT file failed", "partition", absIdx, "error", err)
			} else {
				slog.Info("WAT partition scanned",
					"partition", absIdx, "of", len(urls),
					"found", len(candidates))
			}

			mu.Lock()
			results = append(results, pr)
			mu.Unlock()

			if onResult != nil {
				onResult(pr, len(urls))
			}
		}(i, startIdx+i, watURL)
	}

	wg.Wait()

	slog.Info("Disqus discovery batch complete",
		"partitions_scanned", len(results),
		"offset", startIdx, "end", endIdx)
	return results, nil
}

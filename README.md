# commentcrawl

Find sites with public comments (WordPress and Disqus) using Common Crawl data.

Multi-stage pipeline:
1. **WP Discovery** — Queries Common Crawl's columnar Parquet index via DuckDB to find WordPress domains.
2. **Disqus Discovery** — Scans Common Crawl WAT files for Disqus embeds (`SHORTNAME.disqus.com` in script/link metadata).
3. **Verification** — Live-checks each candidate domain's WP REST API for accessible comments and scans homepage HTML for Disqus embeds. Discovers top pages with comments per confirmed site.

## CLI Usage

```bash
go build -o commentcrawl ./cmd/commentcrawl
```

### Run both stages

Discover WordPress candidates from Common Crawl, then verify each one for WordPress comments and Disqus embeds:

```bash
./commentcrawl run --partitions 5
```

### Discover WordPress candidates

Scan Common Crawl Parquet index partitions for domains containing WordPress paths (`/wp-json/`, `/wp-content/`, `/wp-includes/`):

```bash
./commentcrawl discover-wp --crawl CC-MAIN-2024-22 --partitions 10
```

### Discover Disqus candidates

Scan Common Crawl WAT (Web Archive Transformation) files for pages embedding Disqus. WAT files contain pre-extracted HTML metadata — the scanner checks script src URLs and link hrefs for `SHORTNAME.disqus.com` patterns:

```bash
./commentcrawl discover-disqus --partitions 100 --workers 3
```

Each WAT file is ~200 MB compressed (~90,000 files per crawl). Default scans 100 partitions with 3 concurrent downloads.

### Verify domains

Verify previously discovered domains from both WordPress and Disqus discovery. Only processes domains not yet in the results table, so re-running picks up where you left off. Checks for both WordPress comments and Disqus embeds:

```bash
./commentcrawl verify-wp --workers 20 --timeout 10s
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--crawl` | `CC-MAIN-2024-22` | Common Crawl crawl ID |
| `--partitions` | `5` | Number of Parquet partitions to scan |
| `-d`, `--db` | `commentcrawl.db` | SQLite database path |
| `--workers` | `15` | Concurrent HTTP workers for verification |
| `--timeout` | `8s` | Per-request timeout for verification |

## What it detects

- **WordPress comments** — Probes for the `api.w.org` Link header, then checks `/wp-json/wp/v2/comments` for accessible comments. Fetches top 10 pages with comments per site.
- **Disqus comments** — Scans homepage HTML for `SHORTNAME.disqus.com/embed.js` or `disqus_shortname` variable assignments. Extracts and stores the Disqus forum name.

## Database

Results are stored in a SQLite database (`commentcrawl.db` by default) with four tables:

| Table | Contents |
|-------|----------|
| `wp_candidates` | WordPress discoveries (domain, hostname, sample_url) |
| `disqus_candidates` | Disqus discoveries from WAT scanning (domain, hostname, sample_url, disqus_shortname) |
| `results` | Verification results per domain (wp_confirmed, comments_endpoint, comment_count_hint, api_root, disqus_detected, disqus_shortname, error) |
| `pages` | Individual pages with comments (domain, post_id, url, title, comment_count_in_sample) |

## Using as a Library

```bash
go get github.com/christianreimer/commentcrawl
```

### Discover WordPress domains from Common Crawl

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/christianreimer/commentcrawl/discovery"
)

func main() {
	ctx := context.Background()

	candidates, err := discovery.Discover(ctx, discovery.Options{
		Crawl:         "CC-MAIN-2024-22",
		MaxPartitions: 3,
	}, func(i, total, found int) {
		fmt.Printf("Partition %d/%d: %d new domains\n", i+1, total, found)
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, c := range candidates {
		fmt.Printf("%s  %s\n", c.Domain, c.SampleURL)
	}
}
```

### Discover Disqus sites from Common Crawl WAT files

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/christianreimer/commentcrawl/discovery/wat"
)

func main() {
	ctx := context.Background()

	candidates, err := wat.Discover(ctx, wat.Options{
		Crawl:         "CC-MAIN-2024-22",
		MaxPartitions: 10,
		Workers:       3,
	}, func(i, total, found int) {
		fmt.Printf("WAT %d/%d: %d new Disqus domains\n", i+1, total, found)
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, c := range candidates {
		fmt.Printf("%s  disqus=%s  %s\n", c.Domain, c.DisqusShortname, c.SampleURL)
	}
}
```

### Verify a single domain

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/christianreimer/commentcrawl/verification"
)

func main() {
	ctx := context.Background()

	result, pages := verification.CheckDomain(ctx, "example.com", verification.Options{
		Timeout:  10 * time.Second,
		MaxPages: 10,
	})

	fmt.Printf("WordPress: %v, Comments: %v, Count: %d\n",
		result.WPConfirmed, result.CommentsEndpoint, result.CommentCountHint)
	fmt.Printf("Disqus: %v, Forum: %s\n",
		result.DisqusDetected, result.DisqusShortname)

	for _, p := range pages {
		fmt.Printf("  %s — %s (%d comments)\n", p.URL, p.Title, p.CommentCountInSample)
	}
}
```

### Verify multiple domains concurrently

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/christianreimer/commentcrawl/verification"
)

func main() {
	ctx := context.Background()

	domains := []string{"bloodandfrogs.com", "blogdodina.com", "example.com"}

	results, pages := verification.VerifyAll(ctx, domains, verification.Options{
		Workers:  10,
		Timeout:  8 * time.Second,
		MaxPages: 5,
	}, func(r verification.Result, p []verification.Page) {
		status := "no comments"
		if r.CommentsEndpoint {
			status = fmt.Sprintf("%d WP comments", r.CommentCountHint)
		}
		if r.DisqusDetected {
			status += fmt.Sprintf(" disqus=%s", r.DisqusShortname)
		}
		if !r.CommentsEndpoint && !r.DisqusDetected && r.Error != "" {
			status = r.Error
		}
		fmt.Printf("%-40s %s\n", r.Domain, status)
	})

	fmt.Printf("\n%d domains checked, %d pages found\n", len(results), len(pages))
}
```

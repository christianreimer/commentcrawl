# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

WordPress & Disqus Comment Finder — a multi-stage pipeline that discovers sites with public comments using Common Crawl data:

1. **Stage 1a (WP Discovery):** Queries Common Crawl's columnar Parquet index via DuckDB over HTTPS to find WordPress domains (looks for `/wp-json/`, `/wp-content/`, `/wp-includes/` paths).
2. **Stage 1b (Disqus Discovery):** Scans Common Crawl WAT (Web Archive Transformation) files for Disqus embeds. WAT files contain pre-extracted HTML metadata including script src URLs and link hrefs. Searches for `SHORTNAME.disqus.com` patterns in Scripts and Links arrays.
3. **Stage 2 (Verification):** Live-checks each candidate domain's WP REST API for accessible comments and scans homepage HTML for Disqus embeds. Fetches top 10 pages with comments per confirmed site.

## Building & Running

```bash
go build -o commentcrawl ./cmd/commentcrawl

# Run WP discovery + verification
./commentcrawl run --partitions 5

# Stage 1a — discover WordPress candidates
./commentcrawl discover-wp --crawl CC-MAIN-2024-22 --partitions 10

# Stage 1b — discover Disqus candidates from WAT files
./commentcrawl discover-disqus --partitions 100 --workers 3

# Stage 2 — verify all unverified domains (from both discovery paths)
./commentcrawl verify-wp --workers 20 --timeout 10s
```

Results are stored in SQLite (`commentcrawl.db` by default). Schema managed via sqlc (`cd store && sqlc generate`).

## Package Structure

- `discovery/` — Stage 1a: manifest fetching, DuckDB parquet queries over HTTPS, deduplication
- `discovery/wat/` — Stage 1b: WAT file streaming, WARC record parsing, Disqus shortname extraction from Scripts/Links
- `verification/` — Stage 2: WordPress API detection, Disqus embed detection, comments endpoint checking, top-pages fetching with bounded concurrency
- `store/` — SQLite persistence via sqlc-generated queries. Schema in `store/db/migrations/`, queries in `store/db/queries/`
- `cmd/commentcrawl/` — Cobra CLI with `discover-wp`, `discover-disqus`, `verify-wp`, and `run` subcommands

## Architecture Notes

- DuckDB (via `go-duckdb` CGo bindings) reads remote Parquet files directly over HTTPS — no local download needed. Each partition is ~400 MB streamed.
- Parquet filenames contain a crawl-specific UUID. The `FetchParquetURLs` function downloads the `cc-index-table.paths.gz` manifest to resolve actual filenames.
- WAT files (~200 MB compressed each, ~90,000 per crawl) are streamed line-by-line. A `bytes.Contains(line, "disqus.com")` pre-filter skips >99.9% of records before JSON parsing.
- WAT discovery checks both `Scripts[].url` (static `<script src>`) and `Links[].url` (noscript fallback links like `SHORTNAME.disqus.com/?url=...`) since the standard Disqus embed loads `embed.js` dynamically via inline JS.
- Stage 2 uses goroutines with a semaphore for bounded concurrency (default 15 workers).
- WordPress detection: checks `Link` header for `api.w.org`, with HEAD→GET and HTTPS→HTTP fallbacks.
- Disqus detection (Stage 2): scans homepage HTML body for `SHORTNAME.disqus.com/embed.js` or `disqus_shortname` variable.
- The WP comments endpoint must be queried without `status=approved` — that parameter triggers 401 on most sites. The default response already returns only approved comments.
- The `verify-wp` command processes domains from both `wp_candidates` and `disqus_candidates` tables via a UNION query, skipping already-verified domains.
- Default crawl is `CC-MAIN-2024-22` — pass `--crawl` for newer crawls.

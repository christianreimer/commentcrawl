// Package verification implements Stage 2 of the WordPress comment finder
// pipeline: live-checking candidate domains for WordPress REST API endpoints,
// Disqus embeds, and discovering pages with comments.
package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	wpAPIRel   = "api.w.org"
	maxBodyLen = 512 * 1024 // 512KB cap for HTML body reads
)

// Disqus detection patterns.
var (
	disqusEmbedRe     = regexp.MustCompile(`//(\w[\w-]*)\.disqus\.com/embed\.js`)
	disqusShortnameRe = regexp.MustCompile(`disqus_shortname\s*=\s*['"](\w[\w-]*)['"]`)
)

// Result is the per-domain verification outcome.
type Result struct {
	Domain           string
	WPConfirmed      bool
	CommentsEndpoint bool
	CommentCountHint int
	APIRoot          string
	DisqusDetected   bool
	DisqusShortname  string
	Error            string
}

// Page represents a WordPress post that has comments.
type Page struct {
	Domain               string
	PostID               int
	URL                  string
	Title                string
	CommentCountInSample int
}

// Options controls Stage 2 behavior.
type Options struct {
	Workers   int
	Timeout   time.Duration
	MaxPages  int    // top N pages per domain (default 10)
	UserAgent string // custom User-Agent header
}

func (o *Options) defaults() {
	if o.Workers <= 0 {
		o.Workers = 15
	}
	if o.Timeout <= 0 {
		o.Timeout = 8 * time.Second
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 10
	}
	if o.UserAgent == "" {
		o.UserAgent = "Mozilla/5.0 (compatible; CommentCrawl/1.0)"
	}
}

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

// probeResult holds the output of probing a domain's homepage.
type probeResult struct {
	linkHeader string // Link response header value
	htmlBody   string // HTML body (up to maxBodyLen), empty if only HEAD succeeded
	err        error
}

// probeDomain does HEAD then GET on the domain to extract the Link header
// and (when a GET is performed) the HTML body for Disqus detection.
func probeDomain(ctx context.Context, client *http.Client, baseURL, userAgent string) probeResult {
	// Try HEAD first
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, baseURL, nil)
	if err != nil {
		return probeResult{err: err}
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return probeResult{err: err}
	}
	resp.Body.Close()

	linkHeader := resp.Header.Get("Link")
	if strings.Contains(linkHeader, wpAPIRel) {
		// HEAD found WP. Issue a GET too so we can scan body for Disqus.
		body := fetchBody(ctx, client, baseURL, userAgent)
		return probeResult{linkHeader: linkHeader, htmlBody: body}
	}

	// HEAD didn't find WP Link header — try GET (some servers block HEAD)
	body, getLink, err := fetchBodyAndLink(ctx, client, baseURL, userAgent)
	if err != nil {
		return probeResult{err: err}
	}
	return probeResult{linkHeader: getLink, htmlBody: body}
}

// fetchBody does a GET and returns the body text (up to maxBodyLen).
func fetchBody(ctx context.Context, client *http.Client, url, userAgent string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyLen))
	return string(data)
}

// fetchBodyAndLink does a GET and returns (body, linkHeader, error).
func fetchBodyAndLink(ctx context.Context, client *http.Client, url, userAgent string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyLen))
	return string(data), resp.Header.Get("Link"), nil
}

// discoverAPIRoot probes a domain for a WordPress API root URL.
// Also returns the HTML body (when available) for Disqus detection.
// Returns (apiRoot, errorString, htmlBody).
func discoverAPIRoot(ctx context.Context, client *http.Client, domain, userAgent string) (string, string, string) {
	baseURL := "https://" + domain

	pr := probeDomain(ctx, client, baseURL, userAgent)
	if pr.err != nil {
		// Check for TLS error — try HTTP fallback
		if isTLSError(pr.err) {
			httpBase := "http://" + domain
			body, linkHeader, err := fetchBodyAndLink(ctx, client, httpBase, userAgent)
			if err != nil {
				return "", fmt.Sprintf("SSL error + fallback failed: %v", err), ""
			}

			apiRoot := ""
			if strings.Contains(linkHeader, wpAPIRel) {
				apiRoot = httpBase + "/wp-json/"
			}
			// Even if WP not found, return body for Disqus check
			if apiRoot == "" {
				return "", "SSL error, no WordPress API header", body
			}
			return apiRoot, "", body
		}
		return "", pr.err.Error(), ""
	}

	if !strings.Contains(pr.linkHeader, wpAPIRel) {
		return "", "no WordPress API header", pr.htmlBody
	}

	// Extract API root from Link header
	for _, part := range strings.Split(pr.linkHeader, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, wpAPIRel) && strings.HasPrefix(part, "<") {
			idx := strings.Index(part, ">")
			if idx > 1 {
				return part[1:idx], "", pr.htmlBody
			}
		}
	}

	return baseURL + "/wp-json/", "", pr.htmlBody
}

func isTLSError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "tls:") || strings.Contains(errStr, "certificate")
}

// detectDisqus scans HTML for Disqus embed patterns and returns the shortname,
// or empty string if not found.
func detectDisqus(html string) string {
	if m := disqusEmbedRe.FindStringSubmatch(html); len(m) > 1 {
		return m[1]
	}
	if m := disqusShortnameRe.FindStringSubmatch(html); len(m) > 1 {
		return m[1]
	}
	return ""
}

// fetchTopCommentedPages fetches comments, collects unique post IDs, then
// looks up each post to get its URL and title.
func fetchTopCommentedPages(ctx context.Context, client *http.Client, apiRoot, domain, userAgent string, maxPages int) []Page {
	commentsURL := strings.TrimRight(apiRoot, "/") + "/wp/v2/comments?per_page=100"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, commentsURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var comments []struct {
		Post int `json:"post"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return nil
	}

	// Count comments per post
	postCounts := make(map[int]int)
	for _, c := range comments {
		if c.Post > 0 {
			postCounts[c.Post]++
		}
	}

	// Sort by count desc, take top N
	type postCount struct {
		id    int
		count int
	}
	sorted := make([]postCount, 0, len(postCounts))
	for id, count := range postCounts {
		sorted = append(sorted, postCount{id, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})
	if len(sorted) > maxPages {
		sorted = sorted[:maxPages]
	}

	postsBase := strings.TrimRight(apiRoot, "/") + "/wp/v2/posts"
	var pages []Page
	for _, pc := range sorted {
		postURL := fmt.Sprintf("%s/%d", postsBase, pc.id)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, postURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		var post struct {
			Link  string          `json:"link"`
			Title json.RawMessage `json:"title"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&post); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		title := extractTitle(post.Title)
		pages = append(pages, Page{
			Domain:               domain,
			PostID:               pc.id,
			URL:                  post.Link,
			Title:                title,
			CommentCountInSample: pc.count,
		})
	}

	return pages
}

func extractTitle(raw json.RawMessage) string {
	var obj struct {
		Rendered string `json:"rendered"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Rendered != "" {
		return obj.Rendered
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// CheckDomain probes a single domain for WordPress comments and Disqus embeds.
func CheckDomain(ctx context.Context, domain string, opts Options) (Result, []Page) {
	opts.defaults()
	client := newClient(opts.Timeout)

	result := Result{Domain: domain}

	apiRoot, errMsg, htmlBody := discoverAPIRoot(ctx, client, domain, opts.UserAgent)

	// Check for Disqus regardless of whether WordPress was detected
	if htmlBody != "" {
		if shortname := detectDisqus(htmlBody); shortname != "" {
			result.DisqusDetected = true
			result.DisqusShortname = shortname
		}
	}

	if apiRoot == "" {
		result.Error = errMsg
		return result, nil
	}

	result.WPConfirmed = true
	result.APIRoot = apiRoot

	// Check WP comments endpoint
	commentsURL := strings.TrimRight(apiRoot, "/") + "/wp/v2/comments?per_page=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, commentsURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	req.Header.Set("User-Agent", opts.UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			result.Error = "comments endpoint requires auth"
		case http.StatusForbidden:
			result.Error = "comments endpoint forbidden"
		default:
			result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return result, nil
	}

	// Parse X-WP-Total
	totalStr := resp.Header.Get("X-WP-Total")
	total, _ := strconv.Atoi(totalStr)
	result.CommentCountHint = total

	// Validate response body
	var data json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		result.Error = "invalid JSON"
		return result, nil
	}

	trimmed := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trimmed, "[") {
		result.Error = "invalid comments response"
		return result, nil
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		result.Error = "invalid comments array"
		return result, nil
	}
	if len(arr) == 0 && total == 0 {
		result.Error = "no comments"
		return result, nil
	}

	result.CommentsEndpoint = true

	pages := fetchTopCommentedPages(ctx, client, apiRoot, domain, opts.UserAgent, opts.MaxPages)
	return result, pages
}

// VerifyAll checks multiple domains concurrently. The onResult callback fires
// for each completed domain (for progress reporting).
func VerifyAll(ctx context.Context, domains []string, opts Options, onResult func(Result, []Page)) ([]Result, []Page) {
	opts.defaults()

	slog.Info("Stage 2 — Live-verifying domains",
		"count", len(domains), "workers", opts.Workers, "timeout", opts.Timeout)

	var (
		mu       sync.Mutex
		results  []Result
		allPages []Page
		sem      = make(chan struct{}, opts.Workers)
		wg       sync.WaitGroup
	)

	for _, domain := range domains {
		wg.Add(1)
		sem <- struct{}{}
		go func(d string) {
			defer wg.Done()
			defer func() { <-sem }()

			result, pages := CheckDomain(ctx, d, opts)

			mu.Lock()
			results = append(results, result)
			allPages = append(allPages, pages...)
			mu.Unlock()

			if onResult != nil {
				onResult(result, pages)
			}
		}(domain)
	}

	wg.Wait()

	wpConfirmed := 0
	disqusFound := 0
	for _, r := range results {
		if r.CommentsEndpoint {
			wpConfirmed++
		}
		if r.DisqusDetected {
			disqusFound++
		}
	}
	slog.Info("Stage 2 complete",
		"wp_comments", wpConfirmed, "disqus", disqusFound,
		"total", len(domains), "pages_found", len(allPages))

	return results, allPages
}

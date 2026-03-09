# Unit Tests Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add comprehensive unit tests covering all pure functions and HTTP-dependent logic via httptest servers across the three main packages.

**Architecture:** Test pure functions directly with table-driven tests. Test HTTP-dependent functions using `net/http/httptest` servers that simulate WordPress and Disqus responses. Test database layer with in-memory SQLite.

**Tech Stack:** Go stdlib `testing`, `net/http/httptest`, `strings.NewReader`, in-memory SQLite

---

### Task 1: WAT — extractRegisteredDomain

**Files:**
- Create: `discovery/wat/wat_test.go`

**Step 1: Write the test**

```go
package wat

import "testing"

func TestExtractRegisteredDomain(t *testing.T) {
	tests := []struct {
		hostname string
		want     string
	}{
		{"www.example.com", "example.com"},
		{"example.com", "example.com"},
		{"blog.example.co.uk", "example.co.uk"},
		{"example.co.uk", "example.co.uk"},
		{"sub.deep.example.com", "example.com"},
		{"example.com.", "example.com"},        // trailing dot
		{"EXAMPLE.COM", "example.com"},          // case normalization
		{"", ""},                                 // empty
		{"localhost", "localhost"},               // single label
		{"blog.example.com.au", "example.com.au"},
		{"www.example.org.uk", "example.org.uk"},
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			got := extractRegisteredDomain(tt.hostname)
			if got != tt.want {
				t.Errorf("extractRegisteredDomain(%q) = %q, want %q", tt.hostname, got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./discovery/wat/ -run TestExtractRegisteredDomain -v`
Expected: PASS

---

### Task 2: WAT — extractShortname

**Files:**
- Modify: `discovery/wat/wat_test.go`

**Step 1: Write the test**

```go
func TestExtractShortname(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   string
	}{
		{"embed.js URL", "//myblog.disqus.com/embed.js", "myblog"},
		{"http prefix", "http://myblog.disqus.com/foo", "myblog"},
		{"https prefix", "https://myblog.disqus.com/foo", ""},  // regex uses ^|// so https:// matches
		{"with query", "//cool-site.disqus.com/?url=http://x", "cool-site"},
		{"www filtered", "//www.disqus.com/embed.js", ""},
		{"disqus filtered", "//disqus.disqus.com/embed.js", ""},
		{"help filtered", "//help.disqus.com/something", ""},
		{"blog filtered", "//blog.disqus.com/something", ""},
		{"docs filtered", "//docs.disqus.com/something", ""},
		{"no match", "https://example.com/page", ""},
		{"empty", "", ""},
		{"uppercase normalized", "//MyBlog.disqus.com/embed.js", "myblog"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractShortname(tt.rawURL)
			if got != tt.want {
				t.Errorf("extractShortname(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./discovery/wat/ -run TestExtractShortname -v`
Expected: PASS

---

### Task 3: WAT — extractDisqusFromJSON

**Files:**
- Modify: `discovery/wat/wat_test.go`

**Step 1: Write the test**

```go
func TestExtractDisqusFromJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			"script embed.js",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//myblog.disqus.com/embed.js"}],"Links":[]}}}}}`,
			"myblog",
		},
		{
			"link subdomain",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[],"Links":[{"url":"//coolsite.disqus.com/?url=http://example.com"}]}}}}}`,
			"coolsite",
		},
		{
			"script takes priority over link",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//fromscript.disqus.com/embed.js"}],"Links":[{"url":"//fromlink.disqus.com/?url=x"}]}}}}}`,
			"fromscript",
		},
		{
			"no disqus",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//cdn.example.com/app.js"}],"Links":[]}}}}}`,
			"",
		},
		{
			"invalid JSON",
			`not json at all`,
			"",
		},
		{
			"empty scripts and links",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[],"Links":[]}}}}}`,
			"",
		},
		{
			"filtered www subdomain in link",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[],"Links":[{"url":"//www.disqus.com/foo"}]}}}}}`,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDisqusFromJSON([]byte(tt.json))
			if got != tt.want {
				t.Errorf("extractDisqusFromJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./discovery/wat/ -run TestExtractDisqusFromJSON -v`
Expected: PASS

---

### Task 4: WAT — parseWATRecords

**Files:**
- Modify: `discovery/wat/wat_test.go`

**Step 1: Write the test**

```go
func TestParseWATRecords(t *testing.T) {
	// Simulate a WAT file with WARC headers and JSON metadata lines
	watContent := `WARC/1.0
WARC-Type: metadata
WARC-Target-URI: http://www.example.com/page1

{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//myblog.disqus.com/embed.js"}],"Links":[]}}}}}
WARC/1.0
WARC-Type: metadata
WARC-Target-URI: http://www.other.com/page2

{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[],"Links":[{"url":"//otherblog.disqus.com/?url=x"}]}}}}}
WARC/1.0
WARC-Type: metadata
WARC-Target-URI: http://www.nodisqus.com/page3

{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//cdn.jquery.com/jquery.js"}],"Links":[]}}}}}
`

	candidates, err := parseWATRecords(strings.NewReader(watContent))
	if err != nil {
		t.Fatalf("parseWATRecords() error: %v", err)
	}

	if len(candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(candidates))
	}

	// Results are sorted by domain
	if candidates[0].Domain != "example.com" {
		t.Errorf("candidates[0].Domain = %q, want %q", candidates[0].Domain, "example.com")
	}
	if candidates[0].DisqusShortname != "myblog" {
		t.Errorf("candidates[0].DisqusShortname = %q, want %q", candidates[0].DisqusShortname, "myblog")
	}

	if candidates[1].Domain != "other.com" {
		t.Errorf("candidates[1].Domain = %q, want %q", candidates[1].Domain, "other.com")
	}
	if candidates[1].DisqusShortname != "otherblog" {
		t.Errorf("candidates[1].DisqusShortname = %q, want %q", candidates[1].DisqusShortname, "otherblog")
	}
}

func TestParseWATRecords_Deduplication(t *testing.T) {
	// Same domain appears twice — should deduplicate
	watContent := `WARC-Target-URI: http://www.example.com/page1
{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//myblog.disqus.com/embed.js"}],"Links":[]}}}}}
WARC-Target-URI: http://blog.example.com/page2
{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//myblog.disqus.com/embed.js"}],"Links":[]}}}}}
`

	candidates, err := parseWATRecords(strings.NewReader(watContent))
	if err != nil {
		t.Fatalf("parseWATRecords() error: %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("got %d candidates, want 1 (should deduplicate)", len(candidates))
	}
}
```

Add `"strings"` to the imports at top of the test file.

**Step 2: Run test to verify it passes**

Run: `go test ./discovery/wat/ -run TestParseWATRecords -v`
Expected: PASS

---

### Task 5: Verification — detectDisqus

**Files:**
- Create: `verification/verification_test.go`

**Step 1: Write the test**

```go
package verification

import "testing"

func TestDetectDisqus(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			"embed.js pattern",
			`<script src="//myblog.disqus.com/embed.js"></script>`,
			"myblog",
		},
		{
			"shortname variable single quotes",
			`<script>var disqus_shortname = 'cool-blog';</script>`,
			"cool-blog",
		},
		{
			"shortname variable double quotes",
			`<script>var disqus_shortname = "anotherblog";</script>`,
			"anotherblog",
		},
		{
			"embed.js takes priority",
			`<script src="//froembed.disqus.com/embed.js"></script>
			 <script>var disqus_shortname = 'fromvar';</script>`,
			"froembed",
		},
		{
			"no disqus",
			`<html><body><p>Hello world</p></body></html>`,
			"",
		},
		{
			"empty string",
			"",
			"",
		},
		{
			"disqus in text but not pattern",
			`<p>Check out disqus.com for comments</p>`,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectDisqus(tt.html)
			if got != tt.want {
				t.Errorf("detectDisqus() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./verification/ -run TestDetectDisqus -v`
Expected: PASS

---

### Task 6: Verification — extractTitle

**Files:**
- Modify: `verification/verification_test.go`

**Step 1: Write the test**

```go
import "encoding/json"

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			"rendered object",
			`{"rendered": "Hello World"}`,
			"Hello World",
		},
		{
			"plain string",
			`"Just a String Title"`,
			"Just a String Title",
		},
		{
			"rendered with HTML entities",
			`{"rendered": "Tips &amp; Tricks"}`,
			"Tips &amp; Tricks",
		},
		{
			"empty rendered",
			`{"rendered": ""}`,
			"",
		},
		{
			"null",
			`null`,
			"",
		},
		{
			"empty",
			``,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTitle(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Errorf("extractTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./verification/ -run TestExtractTitle -v`
Expected: PASS

---

### Task 7: Verification — isTLSError

**Files:**
- Modify: `verification/verification_test.go`

**Step 1: Write the test**

```go
import "errors"

func TestIsTLSError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"tls error", errors.New("tls: handshake failure"), true},
		{"certificate error", errors.New("x509: certificate signed by unknown authority"), true},
		{"connection refused", errors.New("dial tcp: connection refused"), false},
		{"timeout", errors.New("context deadline exceeded"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTLSError(tt.err)
			if got != tt.want {
				t.Errorf("isTLSError() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./verification/ -run TestIsTLSError -v`
Expected: PASS

---

### Task 8: Verification — Options.defaults

**Files:**
- Modify: `verification/verification_test.go`

**Step 1: Write the test**

```go
import "time"

func TestOptionsDefaults(t *testing.T) {
	t.Run("zero value gets defaults", func(t *testing.T) {
		opts := Options{}
		opts.defaults()

		if opts.Workers != 15 {
			t.Errorf("Workers = %d, want 15", opts.Workers)
		}
		if opts.Timeout != 8*time.Second {
			t.Errorf("Timeout = %v, want 8s", opts.Timeout)
		}
		if opts.MaxPages != 10 {
			t.Errorf("MaxPages = %d, want 10", opts.MaxPages)
		}
		if opts.UserAgent == "" {
			t.Error("UserAgent should not be empty")
		}
	})

	t.Run("custom values preserved", func(t *testing.T) {
		opts := Options{Workers: 5, Timeout: 3 * time.Second, MaxPages: 20, UserAgent: "custom/1.0"}
		opts.defaults()

		if opts.Workers != 5 {
			t.Errorf("Workers = %d, want 5", opts.Workers)
		}
		if opts.Timeout != 3*time.Second {
			t.Errorf("Timeout = %v, want 3s", opts.Timeout)
		}
		if opts.MaxPages != 20 {
			t.Errorf("MaxPages = %d, want 20", opts.MaxPages)
		}
		if opts.UserAgent != "custom/1.0" {
			t.Errorf("UserAgent = %q, want %q", opts.UserAgent, "custom/1.0")
		}
	})
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./verification/ -run TestOptionsDefaults -v`
Expected: PASS

---

### Task 9: Verification — CheckDomain with httptest (WP site with comments)

**Files:**
- Modify: `verification/verification_test.go`

**Step 1: Write the test**

```go
import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
)

func TestCheckDomain_WPWithComments(t *testing.T) {
	mux := http.NewServeMux()

	// Homepage HEAD/GET returns WP Link header
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<http://example.com/wp-json/>; rel="https://api.w.org/"`)
		fmt.Fprint(w, "<html><body>Hello</body></html>")
	})

	// Comments endpoint
	mux.HandleFunc("/wp-json/wp/v2/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-WP-Total", "42")
		fmt.Fprint(w, `[{"id":1,"post":10},{"id":2,"post":10},{"id":3,"post":20}]`)
	})

	// Post lookup
	mux.HandleFunc("/wp-json/wp/v2/posts/10", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"link":"http://example.com/hello","title":{"rendered":"Hello Post"}}`)
	})
	mux.HandleFunc("/wp-json/wp/v2/posts/20", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"link":"http://example.com/world","title":{"rendered":"World Post"}}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Extract host from test server URL (strip http://)
	host := srv.Listener.Addr().String()

	result, pages := CheckDomain(context.Background(), host, Options{
		Timeout: 5 * time.Second,
	})

	if !result.WPConfirmed {
		t.Error("expected WPConfirmed=true")
	}
	if !result.CommentsEndpoint {
		t.Error("expected CommentsEndpoint=true")
	}
	if result.CommentCountHint != 42 {
		t.Errorf("CommentCountHint = %d, want 42", result.CommentCountHint)
	}
	if len(pages) != 2 {
		t.Errorf("got %d pages, want 2", len(pages))
	}
}
```

Note: CheckDomain prepends "https://" to the domain but httptest uses HTTP. This test needs the test server to handle the HTTPS->HTTP fallback. We need to use `httptest.NewTLSServer` instead, or override the domain to point at the httptest URL. Since `CheckDomain` constructs the URL internally, we'll use `httptest.NewTLSServer` and configure the client to trust its cert. However, since `CheckDomain` creates its own client internally, we should test via the lower-level `discoverAPIRoot` + `fetchTopCommentedPages` functions directly with the httptest URL, or use a TLS test server that will naturally be tried first.

**Revised approach:** Use `httptest.NewTLSServer` so the `https://host:port` probe succeeds. But `CheckDomain` creates its own `http.Client` that won't trust the test cert, so the TLS probe will fail, and it'll fall back to HTTP (which also won't work for a TLS-only server).

**Best approach:** Test the building blocks directly using the httptest server URL. `CheckDomain` is an integration function — test it by calling the sub-functions that accept a `*http.Client` and URL directly.

**Step 2: Run test to verify it passes**

Run: `go test ./verification/ -run TestCheckDomain_WPWithComments -v`
Expected: PASS

---

### Task 10: Verification — CheckDomain via httptest (Disqus-only site)

Similar to Task 9 but the homepage has no WP Link header, just Disqus embed in body. Tests that Disqus detection works even when WP is not found.

---

### Task 11: Verification — CheckDomain via httptest (no comments)

Tests a WP site where the comments endpoint returns empty `[]` and X-WP-Total: 0.

---

### Task 12: Store — database round-trip tests

**Files:**
- Create: `store/store_test.go`

Tests `Open` (in-memory), `InsertCandidates`, `InsertDisqusCandidates`, `InsertResults`, `InsertPages`, `ReadUnverifiedDomains`, and the UNION deduplication logic.

---

### Task 13: Run all tests + verify coverage

Run: `go test ./... -v -count=1`
Run: `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`

---

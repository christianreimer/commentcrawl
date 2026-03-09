package wat

import (
	"strings"
	"testing"
)

func TestExtractRegisteredDomain(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		want     string
	}{
		{"simple domain", "example.com", "example.com"},
		{"www prefix", "www.example.com", "example.com"},
		{"deep subdomain", "a.b.c.example.com", "example.com"},
		{"co.uk two-part TLD", "www.example.co.uk", "example.co.uk"},
		{"co.uk bare", "example.co.uk", "example.co.uk"},
		{"com.au two-part TLD", "blog.example.com.au", "example.com.au"},
		{"org.uk two-part TLD", "shop.example.org.uk", "example.org.uk"},
		{"co.jp two-part TLD", "www.example.co.jp", "example.co.jp"},
		{"trailing dot", "www.example.com.", "example.com"},
		{"case normalization", "WWW.Example.COM", "example.com"},
		{"mixed case co.uk", "Blog.Example.CO.UK", "example.co.uk"},
		{"empty string", "", ""},
		{"single label", "localhost", "localhost"},
		{"two labels only", "example.org", "example.org"},
		{"only dot", ".", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRegisteredDomain(tt.hostname)
			if got != tt.want {
				t.Errorf("extractRegisteredDomain(%q) = %q, want %q", tt.hostname, got, tt.want)
			}
		})
	}
}

func TestExtractShortname(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   string
	}{
		{"embed.js with //", "//mysite.disqus.com/embed.js", "mysite"},
		{"embed.js with https", "https://mysite.disqus.com/embed.js", "mysite"},
		{"embed.js with http", "http://mysite.disqus.com/embed.js", "mysite"},
		{"subdomain link", "//coolblog.disqus.com/?url=http://example.com", "coolblog"},
		{"https subdomain link", "https://testsite.disqus.com/some/path", "testsite"},
		{"filtered www", "//www.disqus.com/something", ""},
		{"filtered disqus", "//disqus.disqus.com/something", ""},
		{"filtered help", "//help.disqus.com/something", ""},
		{"filtered blog", "//blog.disqus.com/something", ""},
		{"filtered docs", "//docs.disqus.com/something", ""},
		{"filtered https", "//https.disqus.com/something", ""},
		{"filtered http", "//http.disqus.com/something", ""},
		{"no match", "https://example.com/page", ""},
		{"empty string", "", ""},
		{"case normalization", "//MyBlog.disqus.com/embed.js", "myblog"},
		{"hyphenated shortname", "//my-cool-blog.disqus.com/embed.js", "my-cool-blog"},
		{"bare disqus.com no subdomain", "https://disqus.com/home/", ""},
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

func TestExtractDisqusFromJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "script embed.js",
			json: `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//myblog.disqus.com/embed.js"}],"Links":[]}}}}}`,
			want: "myblog",
		},
		{
			name: "link subdomain",
			json: `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[],"Links":[{"url":"https://coolsite.disqus.com/?url=http://example.com"}]}}}}}`,
			want: "coolsite",
		},
		{
			name: "script takes priority over link",
			json: `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//fromblog.disqus.com/embed.js"}],"Links":[{"url":"https://fromlink.disqus.com/?url=x"}]}}}}}`,
			want: "fromblog",
		},
		{
			name: "no disqus references",
			json: `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"https://cdn.example.com/app.js"}],"Links":[{"url":"https://example.com/page"}]}}}}}`,
			want: "",
		},
		{
			name: "invalid JSON",
			json: `{this is not valid json`,
			want: "",
		},
		{
			name: "empty scripts and links",
			json: `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[],"Links":[]}}}}}`,
			want: "",
		},
		{
			name: "filtered subdomain in link only",
			json: `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[],"Links":[{"url":"https://www.disqus.com/home/"}]}}}}}`,
			want: "",
		},
		{
			name: "null scripts and links",
			json: `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{}}}}}`,
			want: "",
		},
		{
			name: "multiple scripts first has disqus",
			json: `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//first.disqus.com/embed.js"},{"url":"//second.disqus.com/embed.js"}],"Links":[]}}}}}`,
			want: "first",
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

func TestParseWATRecords(t *testing.T) {
	t.Run("basic extraction", func(t *testing.T) {
		content := strings.Join([]string{
			"WARC/1.0",
			"WARC-Type: metadata",
			"WARC-Target-URI: https://www.example.com/blog/post-1",
			"Content-Length: 123",
			"",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//myblog.disqus.com/embed.js"}],"Links":[]}}}}}`,
			"",
		}, "\n")

		candidates, err := parseWATRecords(strings.NewReader(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(candidates))
		}
		c := candidates[0]
		if c.Domain != "example.com" {
			t.Errorf("Domain = %q, want %q", c.Domain, "example.com")
		}
		if c.Hostname != "www.example.com" {
			t.Errorf("Hostname = %q, want %q", c.Hostname, "www.example.com")
		}
		if c.DisqusShortname != "myblog" {
			t.Errorf("DisqusShortname = %q, want %q", c.DisqusShortname, "myblog")
		}
		if c.SampleURL != "https://www.example.com/blog/post-1" {
			t.Errorf("SampleURL = %q, want %q", c.SampleURL, "https://www.example.com/blog/post-1")
		}
	})

	t.Run("deduplication of same domain", func(t *testing.T) {
		content := strings.Join([]string{
			"WARC-Target-URI: https://www.example.com/post-1",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//site1.disqus.com/embed.js"}],"Links":[]}}}}}`,
			"WARC-Target-URI: https://blog.example.com/post-2",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//site1.disqus.com/embed.js"}],"Links":[]}}}}}`,
		}, "\n")

		candidates, err := parseWATRecords(strings.NewReader(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate (deduplicated), got %d", len(candidates))
		}
		if candidates[0].Domain != "example.com" {
			t.Errorf("Domain = %q, want %q", candidates[0].Domain, "example.com")
		}
	})

	t.Run("multiple different domains", func(t *testing.T) {
		content := strings.Join([]string{
			"WARC-Target-URI: https://www.alpha.com/page",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//alpha.disqus.com/embed.js"}],"Links":[]}}}}}`,
			"WARC-Target-URI: https://www.beta.org/page",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//beta.disqus.com/embed.js"}],"Links":[]}}}}}`,
		}, "\n")

		candidates, err := parseWATRecords(strings.NewReader(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}
		// Results are sorted by domain
		if candidates[0].Domain != "alpha.com" {
			t.Errorf("first Domain = %q, want %q", candidates[0].Domain, "alpha.com")
		}
		if candidates[1].Domain != "beta.org" {
			t.Errorf("second Domain = %q, want %q", candidates[1].Domain, "beta.org")
		}
	})

	t.Run("no disqus lines skipped", func(t *testing.T) {
		content := strings.Join([]string{
			"WARC-Target-URI: https://www.example.com/page",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"https://cdn.example.com/app.js"}],"Links":[]}}}}}`,
			"Some random WARC header line",
			"Another non-JSON line with no disqus reference",
		}, "\n")

		candidates, err := parseWATRecords(strings.NewReader(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 0 {
			t.Fatalf("expected 0 candidates, got %d", len(candidates))
		}
	})

	t.Run("no target URI before disqus line", func(t *testing.T) {
		// disqus.com is in the line so it passes the pre-filter, but no WARC-Target-URI was set
		content := `{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//orphan.disqus.com/embed.js"}],"Links":[]}}}}}}` + "\n"

		candidates, err := parseWATRecords(strings.NewReader(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 0 {
			t.Fatalf("expected 0 candidates (no target URI), got %d", len(candidates))
		}
	})

	t.Run("empty input", func(t *testing.T) {
		candidates, err := parseWATRecords(strings.NewReader(""))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 0 {
			t.Fatalf("expected 0 candidates, got %d", len(candidates))
		}
	})

	t.Run("target URI carries forward", func(t *testing.T) {
		// The WARC-Target-URI should persist and apply to subsequent JSON lines
		content := strings.Join([]string{
			"WARC-Target-URI: https://www.example.com/page",
			"some non-disqus line",
			"another line without the keyword",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[{"url":"//myblog.disqus.com/embed.js"}],"Links":[]}}}}}`,
		}, "\n")

		candidates, err := parseWATRecords(strings.NewReader(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(candidates))
		}
		if candidates[0].SampleURL != "https://www.example.com/page" {
			t.Errorf("SampleURL = %q, want %q", candidates[0].SampleURL, "https://www.example.com/page")
		}
	})

	t.Run("link-based detection", func(t *testing.T) {
		content := strings.Join([]string{
			"WARC-Target-URI: https://www.example.com/page",
			`{"Envelope":{"Payload-Metadata":{"HTTP-Response-Metadata":{"HTML-Metadata":{"Scripts":[],"Links":[{"url":"https://myshortname.disqus.com/?url=http://example.com/page"}]}}}}}`,
		}, "\n")

		candidates, err := parseWATRecords(strings.NewReader(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(candidates))
		}
		if candidates[0].DisqusShortname != "myshortname" {
			t.Errorf("DisqusShortname = %q, want %q", candidates[0].DisqusShortname, "myshortname")
		}
	})
}

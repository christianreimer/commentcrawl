package verification

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// extractTitle
// ---------------------------------------------------------------------------

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "rendered object",
			raw:  json.RawMessage(`{"rendered":"Hello"}`),
			want: "Hello",
		},
		{
			name: "plain string",
			raw:  json.RawMessage(`"My Title"`),
			want: "My Title",
		},
		{
			name: "empty rendered",
			raw:  json.RawMessage(`{"rendered":""}`),
			want: "",
		},
		{
			name: "null",
			raw:  json.RawMessage(`null`),
			want: "",
		},
		{
			name: "empty input",
			raw:  json.RawMessage(``),
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTitle(tc.raw)
			if got != tc.want {
				t.Errorf("extractTitle() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isTLSError
// ---------------------------------------------------------------------------

func TestIsTLSError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "tls error",
			err:  fmt.Errorf("remote error: tls: handshake failure"),
			want: true,
		},
		{
			name: "certificate error",
			err:  fmt.Errorf("x509: certificate signed by unknown authority"),
			want: true,
		},
		{
			name: "non-TLS connection refused",
			err:  fmt.Errorf("dial tcp: connection refused"),
			want: false,
		},
		{
			name: "non-TLS timeout",
			err:  fmt.Errorf("context deadline exceeded"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isTLSError(tc.err)
			if got != tc.want {
				t.Errorf("isTLSError() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Options.defaults
// ---------------------------------------------------------------------------

func TestOptionsDefaults(t *testing.T) {
	t.Run("zero value gets correct defaults", func(t *testing.T) {
		var o Options
		o.defaults()

		if o.Workers != 15 {
			t.Errorf("Workers = %d, want 15", o.Workers)
		}
		if o.Timeout != 8*time.Second {
			t.Errorf("Timeout = %v, want 8s", o.Timeout)
		}
		if o.MaxPages != 10 {
			t.Errorf("MaxPages = %d, want 10", o.MaxPages)
		}
		if o.UserAgent == "" {
			t.Error("UserAgent should not be empty")
		}
	})

	t.Run("custom values preserved", func(t *testing.T) {
		o := Options{
			Workers:   5,
			Timeout:   3 * time.Second,
			MaxPages:  20,
			UserAgent: "CustomBot/1.0",
		}
		o.defaults()

		if o.Workers != 5 {
			t.Errorf("Workers = %d, want 5", o.Workers)
		}
		if o.Timeout != 3*time.Second {
			t.Errorf("Timeout = %v, want 3s", o.Timeout)
		}
		if o.MaxPages != 20 {
			t.Errorf("MaxPages = %d, want 20", o.MaxPages)
		}
		if o.UserAgent != "CustomBot/1.0" {
			t.Errorf("UserAgent = %q, want %q", o.UserAgent, "CustomBot/1.0")
		}
	})
}

// ---------------------------------------------------------------------------
// CheckDomain — httptest-based integration tests
//
// CheckDomain always starts with https://<domain>. To make the HTTPS probe
// produce a real certificate error (caught by isTLSError), and then have the
// HTTP fallback succeed, we run a dual-protocol server on a single port:
//   - TLS connections get a self-signed certificate → client rejects → isTLSError=true
//   - Plain HTTP connections get served normally → fallback succeeds
//
// The server peeks at the first byte of each connection: a TLS ClientHello
// starts with 0x16, everything else is plain HTTP.
// ---------------------------------------------------------------------------

// wpHandlerOpts controls what the test HTTP handler returns.
type wpHandlerOpts struct {
	linkHeader     string // Link header on "/" (empty = no WP)
	homeBody       string // HTML body for "/"
	commentsJSON   string // JSON for /wp-json/wp/v2/comments
	commentsStatus int    // status code for comments endpoint (0 = 200)
	wpTotal        string // X-WP-Total header value
	postJSON       string // JSON for /wp-json/wp/v2/posts/{id}
}

func newWPHandler(o wpHandlerOpts) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if o.linkHeader != "" {
			w.Header().Set("Link", o.linkHeader)
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, o.homeBody)
	})

	mux.HandleFunc("/wp-json/wp/v2/comments", func(w http.ResponseWriter, r *http.Request) {
		status := o.commentsStatus
		if status == 0 {
			status = http.StatusOK
		}
		if o.wpTotal != "" {
			w.Header().Set("X-WP-Total", o.wpTotal)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, o.commentsJSON)
	})

	mux.HandleFunc("/wp-json/wp/v2/posts/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, o.postJSON)
	})

	return mux
}

// peekConn wraps a net.Conn and allows peeked bytes to be re-read.
type peekConn struct {
	net.Conn
	peeked []byte
	offset int
}

func (c *peekConn) Read(b []byte) (int, error) {
	if c.offset < len(c.peeked) {
		n := copy(b, c.peeked[c.offset:])
		c.offset += n
		return n, nil
	}
	return c.Conn.Read(b)
}

// chanListener is a net.Listener backed by a channel, used to feed
// connections into http.Server.Serve one at a time.
type chanListener struct {
	ch   chan net.Conn
	done chan struct{}
	addr net.Addr
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{
		ch:   make(chan net.Conn, 16),
		done: make(chan struct{}),
		addr: addr,
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

// startDualServer starts a TCP listener that serves both TLS (self-signed)
// and plain HTTP on the same port. The first byte of each connection
// determines the protocol: 0x16 = TLS ClientHello, else plain HTTP.
//
// TLS connections undergo a handshake with a self-signed certificate. The
// Go default HTTP client rejects the untrusted cert, producing a certificate
// error that isTLSError catches, which triggers the HTTP fallback path in
// discoverAPIRoot.
func startDualServer(t *testing.T, handler http.Handler) (addr string, cleanup func()) {
	t.Helper()

	// Borrow the TLS config (self-signed cert) from httptest.
	tmpTS := httptest.NewTLSServer(handler)
	tlsCfg := tmpTS.TLS.Clone()
	tmpTS.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = ln.Addr().String()

	httpLn := newChanListener(ln.Addr())
	httpServer := &http.Server{Handler: handler}
	go httpServer.Serve(httpLn)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				// Peek at first byte to determine protocol.
				buf := make([]byte, 1)
				n, err := c.Read(buf)
				if err != nil || n == 0 {
					c.Close()
					return
				}
				pc := &peekConn{Conn: c, peeked: buf[:n]}

				if buf[0] == 0x16 {
					// TLS ClientHello — do a TLS handshake with the
					// self-signed cert. The client will reject it
					// (certificate error), which is exactly what we want.
					tlsConn := tls.Server(pc, tlsCfg)
					_ = tlsConn.Handshake()
					tlsConn.Close()
				} else {
					// Plain HTTP — feed into the HTTP server.
					httpLn.ch <- pc
				}
			}(conn)
		}
	}()

	return addr, func() {
		ln.Close()
		httpLn.Close()
		httpServer.Close()
	}
}

func TestCheckDomain_WPWithComments(t *testing.T) {
	handler := newWPHandler(wpHandlerOpts{
		linkHeader:   `<http://example.com/wp-json/>; rel="https://api.w.org"`,
		homeBody:     `<html><body>Welcome</body></html>`,
		commentsJSON: `[{"id":1,"post":10}]`,
		wpTotal:      "42",
		postJSON:     `{"link":"http://example.com/hello-world","title":{"rendered":"Hello World"}}`,
	})
	addr, cleanup := startDualServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	result, pages := CheckDomain(ctx, addr, Options{Timeout: 5 * time.Second})

	if !result.WPConfirmed {
		t.Errorf("expected WPConfirmed=true, got error=%q", result.Error)
	}
	if !result.CommentsEndpoint {
		t.Errorf("expected CommentsEndpoint=true, error=%q", result.Error)
	}
	if result.CommentCountHint != 42 {
		t.Errorf("CommentCountHint = %d, want 42", result.CommentCountHint)
	}
	if len(pages) == 0 {
		t.Error("expected at least one page")
	} else {
		if pages[0].Title != "Hello World" {
			t.Errorf("page title = %q, want %q", pages[0].Title, "Hello World")
		}
	}
}

func TestCheckDomain_NoWP(t *testing.T) {
	handler := newWPHandler(wpHandlerOpts{
		homeBody: `<html><body>Just a regular site</body></html>`,
	})
	addr, cleanup := startDualServer(t, handler)
	defer cleanup()

	result, _ := CheckDomain(context.Background(), addr, Options{Timeout: 5 * time.Second})

	if result.WPConfirmed {
		t.Error("expected WPConfirmed=false")
	}
	if result.Error == "" {
		t.Error("expected non-empty Error for non-WP site")
	}
}

func TestCheckDomain_WPNoComments(t *testing.T) {
	handler := newWPHandler(wpHandlerOpts{
		linkHeader:   `<http://example.com/wp-json/>; rel="https://api.w.org"`,
		homeBody:     `<html><body>WordPress site</body></html>`,
		commentsJSON: `[]`,
		wpTotal:      "0",
	})
	addr, cleanup := startDualServer(t, handler)
	defer cleanup()

	result, pages := CheckDomain(context.Background(), addr, Options{Timeout: 5 * time.Second})

	if !result.WPConfirmed {
		t.Errorf("expected WPConfirmed=true, error=%q", result.Error)
	}
	if result.CommentsEndpoint {
		t.Error("expected CommentsEndpoint=false")
	}
	if result.Error != "no comments" {
		t.Errorf("Error = %q, want %q", result.Error, "no comments")
	}
	if len(pages) != 0 {
		t.Errorf("expected no pages, got %d", len(pages))
	}
}

func TestCheckDomain_Unreachable(t *testing.T) {
	// Create a listener then close it immediately to get a refused port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	result, _ := CheckDomain(context.Background(), addr, Options{Timeout: 2 * time.Second})

	if result.Error == "" {
		t.Error("expected non-empty Error for unreachable site")
	}
}

func TestCheckDomain_Comments401(t *testing.T) {
	handler := newWPHandler(wpHandlerOpts{
		linkHeader:     `<http://example.com/wp-json/>; rel="https://api.w.org"`,
		homeBody:       `<html></html>`,
		commentsStatus: http.StatusUnauthorized,
		commentsJSON:   `{"code":"rest_forbidden"}`,
	})
	addr, cleanup := startDualServer(t, handler)
	defer cleanup()

	result, _ := CheckDomain(context.Background(), addr, Options{Timeout: 5 * time.Second})

	if !result.WPConfirmed {
		t.Errorf("expected WPConfirmed=true, error=%q", result.Error)
	}
	if result.Error != "comments endpoint requires auth" {
		t.Errorf("Error = %q, want %q", result.Error, "comments endpoint requires auth")
	}
}

// ---------------------------------------------------------------------------
// VerifyAll
// ---------------------------------------------------------------------------

func TestVerifyAll(t *testing.T) {
	// Server 1: WP site with comments
	addr1, cleanup1 := startDualServer(t, newWPHandler(wpHandlerOpts{
		linkHeader:   `<http://s1.example.com/wp-json/>; rel="https://api.w.org"`,
		homeBody:     `<html></html>`,
		commentsJSON: `[{"id":1,"post":1}]`,
		wpTotal:      "5",
		postJSON:     `{"link":"http://s1.example.com/p","title":"Post 1"}`,
	}))
	defer cleanup1()

	// Server 2: Non-WP site
	addr2, cleanup2 := startDualServer(t, newWPHandler(wpHandlerOpts{
		homeBody: `<html><body>Regular site</body></html>`,
	}))
	defer cleanup2()

	// Server 3: WP site, no comments
	addr3, cleanup3 := startDualServer(t, newWPHandler(wpHandlerOpts{
		linkHeader:   `<http://s3.example.com/wp-json/>; rel="https://api.w.org"`,
		homeBody:     `<html></html>`,
		commentsJSON: `[]`,
		wpTotal:      "0",
	}))
	defer cleanup3()

	domains := []string{addr1, addr2, addr3}

	var callbackCount int32
	opts := Options{Workers: 3, Timeout: 5 * time.Second}
	results, pages := VerifyAll(context.Background(), domains, opts, func(r Result, p []Page) {
		atomic.AddInt32(&callbackCount, 1)
	})

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if int(atomic.LoadInt32(&callbackCount)) != 3 {
		t.Errorf("callback fired %d times, want 3", callbackCount)
	}

	// Build a map for easier assertion.
	rm := make(map[string]Result)
	for _, r := range results {
		rm[r.Domain] = r
	}

	if !rm[addr1].WPConfirmed {
		t.Errorf("server1: expected WPConfirmed=true, error=%q", rm[addr1].Error)
	}
	if !rm[addr1].CommentsEndpoint {
		t.Errorf("server1: expected CommentsEndpoint=true, error=%q", rm[addr1].Error)
	}

	if rm[addr2].WPConfirmed {
		t.Errorf("server2: expected WPConfirmed=false")
	}

	if !rm[addr3].WPConfirmed {
		t.Errorf("server3: expected WPConfirmed=true, error=%q", rm[addr3].Error)
	}
	if rm[addr3].CommentsEndpoint {
		t.Errorf("server3: expected CommentsEndpoint=false")
	}

	// At least server1 should produce pages.
	if len(pages) == 0 {
		t.Error("expected at least one page from server1")
	}
}

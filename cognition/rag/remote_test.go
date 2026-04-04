package rag

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeHTTPURL(t *testing.T) {
	u, host, err := normalizeHTTPURL("https://example.com/a?x=1#frag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "example.com" {
		t.Fatalf("expected host example.com, got %q", host)
	}
	if strings.Contains(u, "#frag") {
		t.Fatalf("expected fragment removed, got %q", u)
	}
}

func TestNormalizeHTTPURLRejectsUnsupportedScheme(t *testing.T) {
	if _, _, err := normalizeHTTPURL("file:///tmp/a.txt"); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

func TestNormalizeHTTPURLRejectsInvalidDomain(t *testing.T) {
	if _, _, err := normalizeHTTPURL("https://-bad-domain-.com/path"); err == nil {
		t.Fatal("expected invalid domain error")
	}
}

func TestExtractLinks(t *testing.T) {
	base := "https://example.com/docs/index.html"
	html := `<a href="/a">A</a><a href="https://example.com/b">B</a><a href="mailto:test@example.com">M</a>`
	links := extractLinks(base, []byte(html))
	if len(links) != 2 {
		t.Fatalf("expected 2 HTTP links, got %d: %#v", len(links), links)
	}
	if links[0] != "https://example.com/a" && links[1] != "https://example.com/a" {
		t.Fatalf("expected resolved relative link, got %#v", links)
	}
}

func TestParseHFFirstRows(t *testing.T) {
	body := []byte(`{
		"rows":[
			{"row":{"text":"hello","label":1}},
			{"row":{"text":"world","label":0}}
		]
	}`)
	rows, err := parseHFFirstRows(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !strings.Contains(rows[0], "text: hello") {
		t.Fatalf("unexpected flattened row: %q", rows[0])
	}
}

func TestIsDomainAllowed(t *testing.T) {
	seeds := map[string]bool{"example.com": true}
	if !isDomainAllowed("example.com", seeds, nil, true) {
		t.Fatal("expected seed domain allowed")
	}
	if isDomainAllowed("other.com", seeds, nil, true) {
		t.Fatal("expected non-seed domain blocked when crawling")
	}
	allow := map[string]bool{"other.com": true}
	if !isDomainAllowed("other.com", seeds, allow, true) {
		t.Fatal("expected explicit allowlist domain to pass")
	}
}

func TestPreflightURLReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := srv.Client()
	opts := DefaultRemoteIndexOptions()
	if err := preflightURLReachable(client, srv.URL+"/doc", opts); err != nil {
		t.Fatalf("expected preflight success, got error: %v", err)
	}
}

func TestExtractHFErrorMessageJSON(t *testing.T) {
	msg := extractHFErrorMessage([]byte(`{"error":"The split train does not exist."}`))
	if !strings.Contains(msg, "split train") {
		t.Fatalf("expected parsed error message, got %q", msg)
	}
}

func TestFetchHFSplitHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/splits" {
			_, _ = w.Write([]byte(`{"splits":[{"config":"default","split":"validation"},{"config":"default","split":"test"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	opts := DefaultRemoteIndexOptions()
	hint, err := fetchHFSplitHint(srv.Client(), srv.URL, "demo/ds", opts)
	if err != nil {
		t.Fatalf("expected split hint success, got error: %v", err)
	}
	if !strings.Contains(hint, "default") || !strings.Contains(hint, "validation") {
		t.Fatalf("unexpected hint: %q", hint)
	}
}

func TestNormalizeRemoteOptionsChunkTitleDefaults(t *testing.T) {
	opts := normalizeRemoteOptions(RemoteIndexOptions{})
	if !opts.GenerateChunkTitles {
		t.Fatal("expected GenerateChunkTitles enabled by default")
	}
	if opts.ChunkTitleMaxChars <= 0 {
		t.Fatalf("expected positive ChunkTitleMaxChars, got %d", opts.ChunkTitleMaxChars)
	}
}

func TestParseFetchedContentDefaultsToHTMLOnly(t *testing.T) {
	text, isHTML, err := parseFetchedContent("https://example.com/data.txt", "text/plain", []byte("hello"), false)
	if err == nil {
		t.Fatalf("expected non-html to be rejected, got text=%q html=%t", text, isHTML)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "only html pages") {
		t.Fatalf("expected html-only policy error, got: %v", err)
	}
}

func TestParseFetchedContentAllowsTextWhenExtensionsSpecified(t *testing.T) {
	text, isHTML, err := parseFetchedContent("https://example.com/data.txt", "text/plain", []byte("hello world"), true)
	if err != nil {
		t.Fatalf("expected text/plain to parse when extension mode active, got: %v", err)
	}
	if isHTML {
		t.Fatalf("expected non-html content, got html=true")
	}
	if !strings.Contains(text, "hello world") {
		t.Fatalf("unexpected parsed text: %q", text)
	}
}

func TestURLAllowedByExtensionStrict(t *testing.T) {
	allowed := map[string]bool{".pdf": true, ".md": true}
	if !urlAllowedByExtension("https://example.com/a.pdf", allowed) {
		t.Fatal("expected .pdf URL allowed")
	}
	if urlAllowedByExtension("https://example.com/", allowed) {
		t.Fatal("expected extensionless URL to be blocked in strict mode")
	}
	if urlAllowedByExtension("https://example.com/a.html", allowed) {
		t.Fatal("expected non-allowlisted extension to be blocked")
	}
}

func TestURLAllowedForFetchDefaultHTMLOnly(t *testing.T) {
	if !urlAllowedForFetch("https://example.com/path", nil) {
		t.Fatal("expected extensionless URL allowed for HTML-page crawling")
	}
	if !urlAllowedForFetch("https://example.com/page.html", nil) {
		t.Fatal("expected .html URL allowed")
	}
	if urlAllowedForFetch("https://example.com/_next/static/app.css", nil) {
		t.Fatal("expected .css URL blocked in default HTML-only mode")
	}
	if urlAllowedForFetch("https://example.com/app.js", nil) {
		t.Fatal("expected .js URL blocked in default HTML-only mode")
	}
}

func TestURLAllowedForFetchWithExplicitExtensions(t *testing.T) {
	allowed := map[string]bool{".md": true}
	if !urlAllowedForFetch("https://example.com/readme.md", allowed) {
		t.Fatal("expected allowlisted extension to pass")
	}
	if urlAllowedForFetch("https://example.com/index.html", allowed) {
		t.Fatal("expected non-allowlisted extension blocked when strict extensions are set")
	}
}

func TestParseKaggleRowsCSV(t *testing.T) {
	body := []byte("name,score\nalice,10\nbob,12\n")
	rows, err := parseKaggleRows("train.csv", body, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !strings.Contains(rows[0], "name: alice") || !strings.Contains(rows[0], "score: 10") {
		t.Fatalf("unexpected row content: %q", rows[0])
	}
}

func TestParseKaggleRowsJSONL(t *testing.T) {
	body := []byte("{\"text\":\"a\"}\n{\"text\":\"b\"}\n")
	rows, err := parseKaggleRows("records.jsonl", body, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row due maxRecords cap, got %d", len(rows))
	}
}

func TestResolveKaggleCredentialsFromAlias(t *testing.T) {
	t.Setenv("KAGGLE_USERNAME", "")
	t.Setenv("KAGGLE_KEY", "")
	t.Setenv("KAGGLE_API_KEY", "demo-user:demo-key")
	user, key, err := resolveKaggleCredentials(DefaultRemoteIndexOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "demo-user" || key != "demo-key" {
		t.Fatalf("unexpected credentials: %q %q", user, key)
	}
}

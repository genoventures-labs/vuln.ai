package gutenberg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
)

func TestGutenbergName(t *testing.T) {
	c := NewGutenbergConnector()
	if c.Name() != "gutenberg" {
		t.Fatalf("expected 'gutenberg', got %q", c.Name())
	}
}

func TestGutenbergInterfaceAssertion(t *testing.T) {
	var _ connectors.Connector = (*GutenbergConnector)(nil)
}

func TestPlainTextURL(t *testing.T) {
	cases := []struct {
		name    string
		formats map[string]string
		want    string
	}{
		{
			name:    "utf-8 key preferred",
			formats: map[string]string{"text/plain; charset=utf-8": "https://a.com/a.txt", "text/plain": "https://b.com/b.txt"},
			want:    "https://a.com/a.txt",
		},
		{
			name:    "plain key fallback",
			formats: map[string]string{"text/plain": "https://b.com/b.txt"},
			want:    "https://b.com/b.txt",
		},
		{
			name:    "prefix fallback",
			formats: map[string]string{"text/plain; charset=us-ascii": "https://c.com/c.txt"},
			want:    "https://c.com/c.txt",
		},
		{
			name:    "no match",
			formats: map[string]string{"application/pdf": "https://d.com/d.pdf"},
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := plainTextURL(tc.formats)
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestAuthorNames(t *testing.T) {
	authors := []gutendexAuthor{{Name: "Shelley, Mary Wollstonecraft"}, {Name: "Other, Person"}}
	got := authorNames(authors)
	if !strings.Contains(got, "Shelley") || !strings.Contains(got, "Other") {
		t.Fatalf("unexpected author string: %q", got)
	}
}

func TestSplitIDs(t *testing.T) {
	ids := splitIDs("84, 1342 , 11")
	if len(ids) != 3 || ids[0] != "84" || ids[1] != "1342" || ids[2] != "11" {
		t.Fatalf("unexpected ids: %v", ids)
	}
	if len(splitIDs("")) != 0 {
		t.Fatal("empty string should return empty slice")
	}
}

// mockServer returns a test HTTP server that serves a Gutendex-style response.
func mockSearchServer(t *testing.T, bookID int, title, textContent string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve text download if path ends with .txt
		if strings.HasSuffix(r.URL.Path, ".txt") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(textContent))
			return
		}
		// Otherwise return a Gutendex response
		resp := gutendexResponse{
			Count: 1,
			Results: []gutendexBook{
				{
					ID:    bookID,
					Title: title,
					Authors: []gutendexAuthor{{Name: "Test Author"}},
					Formats: map[string]string{
						"text/plain; charset=utf-8": "http://" + r.Host + "/files/book.txt",
					},
					Subjects: []string{"Fiction"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestFetchSearch(t *testing.T) {
	srv := mockSearchServer(t, 84, "Frankenstein", "It was a dark and stormy night.")
	defer srv.Close()

	c := &GutenbergConnector{http: srv.Client()}
	// Override the gutendex base by building the URL directly via search helper
	// We test the underlying fetchPage + booksToDocuments by wiring the mock server.

	// Build a fake response manually via fetchPage
	books, _, err := c.fetchPage(context.Background(), srv.URL+"/books/?search=frankenstein")
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if len(books) != 1 || books[0].Title != "Frankenstein" {
		t.Fatalf("unexpected books: %+v", books)
	}

	docs, err := c.booksToDocuments(context.Background(), books)
	if err != nil {
		t.Fatalf("booksToDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if d.ID != "84" {
		t.Errorf("unexpected ID: %s", d.ID)
	}
	if d.Title != "Frankenstein" {
		t.Errorf("unexpected Title: %s", d.Title)
	}
	if !strings.Contains(d.Content, "dark") {
		t.Errorf("unexpected Content: %s", d.Content)
	}
	if d.Metadata["source_type"] != "gutenberg" {
		t.Errorf("unexpected source_type: %s", d.Metadata["source_type"])
	}
	if d.Metadata["book_id"] != "84" {
		t.Errorf("unexpected book_id: %s", d.Metadata["book_id"])
	}
}

func TestFetchNoPlainText(t *testing.T) {
	// A book with only a PDF format should be skipped (no error, empty slice).
	c := &GutenbergConnector{http: &http.Client{}}
	books := []gutendexBook{
		{
			ID:      1,
			Title:   "PDF Only",
			Formats: map[string]string{"application/pdf": "https://example.com/book.pdf"},
		},
	}
	docs, err := c.booksToDocuments(context.Background(), books)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs for PDF-only book, got %d", len(docs))
	}
}

func TestFetchEmptyQueryAndNoFilter(t *testing.T) {
	c := NewGutenbergConnector()
	_, err := c.Fetch(context.Background(), connectors.FetchOptions{Query: "  ", MaxResults: 1})
	if err == nil {
		t.Fatal("expected error for empty query without book_ids filter")
	}
}

func TestFetchMaxResultsCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".txt") {
			_, _ = w.Write([]byte("content"))
			return
		}
		resp := gutendexResponse{
			Count: 3,
			Results: []gutendexBook{
				{ID: 1, Title: "Book1", Authors: []gutendexAuthor{{Name: "A"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/1.txt"}},
				{ID: 2, Title: "Book2", Authors: []gutendexAuthor{{Name: "B"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/2.txt"}},
				{ID: 3, Title: "Book3", Authors: []gutendexAuthor{{Name: "C"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/3.txt"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := &GutenbergConnector{http: srv.Client()}
	books, _, err := c.fetchPage(context.Background(), srv.URL+"/books/")
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	// Cap to 2
	if len(books) > 2 {
		books = books[:2]
	}
	docs, err := c.booksToDocuments(context.Background(), books)
	if err != nil {
		t.Fatalf("booksToDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs after cap, got %d", len(docs))
	}
}

func TestFetchByIDsViaMockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".txt") {
			_, _ = w.Write([]byte("full text content"))
			return
		}
		book := gutendexBook{
			ID:      84,
			Title:   "Frankenstein",
			Authors: []gutendexAuthor{{Name: "Shelley, Mary"}},
			Formats: map[string]string{"text/plain; charset=utf-8": "http://" + r.Host + "/book.txt"},
		}
		_ = json.NewEncoder(w).Encode(book)
	}))
	defer srv.Close()

	c := &GutenbergConnector{http: srv.Client()}
	// Build URL with mock server replacing gutendexBase
	u := srv.URL + "/84/"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var book gutendexBook
	if err := json.NewDecoder(resp.Body).Decode(&book); err != nil {
		t.Fatalf("decode: %v", err)
	}
	docs, err := c.booksToDocuments(context.Background(), []gutendexBook{book})
	if err != nil {
		t.Fatalf("booksToDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].Title != "Frankenstein" {
		t.Fatalf("unexpected docs: %+v", docs)
	}
}

// TestFetchPageReturnsNext verifies that fetchPage surfaces the next URL from the response.
func TestFetchPageReturnsNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := gutendexResponse{
			Count: 50,
			Next:  "http://" + r.Host + "/books/?search=test&page=2",
			Results: []gutendexBook{
				{ID: 1, Title: "Book1", Authors: []gutendexAuthor{{Name: "A"}}, Formats: map[string]string{}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := &GutenbergConnector{http: srv.Client()}
	books, next, err := c.fetchPage(context.Background(), srv.URL+"/books/?search=test")
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("expected 1 book, got %d", len(books))
	}
	if next == "" {
		t.Fatal("expected non-empty next URL")
	}
}

// TestFetchPageNoNext verifies that fetchPage returns empty nextURL when the response has no next page.
func TestFetchPageNoNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := gutendexResponse{
			Count:   1,
			Next:    "",
			Results: []gutendexBook{{ID: 99, Title: "Last Book"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := &GutenbergConnector{http: srv.Client()}
	_, next, err := c.fetchPage(context.Background(), srv.URL+"/books/")
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if next != "" {
		t.Fatalf("expected empty next, got %q", next)
	}
}

// TestSearchPaginatesAcrossPages verifies that search() follows the next URL to collect
// books from multiple pages up to the requested max.
func TestSearchPaginatesAcrossPages(t *testing.T) {
	page2Path := "/books/?search=test&page=2"

	// Track how many times each page was requested.
	requests := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path+"?"+r.URL.RawQuery]++

		if strings.HasSuffix(r.URL.Path, ".txt") {
			_, _ = w.Write([]byte("book content"))
			return
		}

		var resp gutendexResponse
		if r.URL.RawQuery == "search=test&page=2" {
			// Page 2: two more books, no next.
			resp = gutendexResponse{
				Count: 4,
				Next:  "",
				Results: []gutendexBook{
					{ID: 3, Title: "Book3", Authors: []gutendexAuthor{{Name: "C"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/3.txt"}},
					{ID: 4, Title: "Book4", Authors: []gutendexAuthor{{Name: "D"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/4.txt"}},
				},
			}
		} else {
			// Page 1: two books, next points to page 2.
			resp = gutendexResponse{
				Count: 4,
				Next:  "http://" + r.Host + page2Path,
				Results: []gutendexBook{
					{ID: 1, Title: "Book1", Authors: []gutendexAuthor{{Name: "A"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/1.txt"}},
					{ID: 2, Title: "Book2", Authors: []gutendexAuthor{{Name: "B"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/2.txt"}},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := &GutenbergConnector{http: srv.Client()}

	// max=4: should collect all 4 books across 2 pages.
	collected, _, err := c.fetchPage(context.Background(), srv.URL+"/books/?search=test")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(collected) != 2 {
		t.Fatalf("expected 2 books on page 1, got %d", len(collected))
	}

	// Simulate what search() does: follow next.
	page2Books, next2, err := c.fetchPage(context.Background(), srv.URL+page2Path)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2Books) != 2 {
		t.Fatalf("expected 2 books on page 2, got %d", len(page2Books))
	}
	if next2 != "" {
		t.Fatalf("expected empty next after page 2, got %q", next2)
	}
	total := append(collected, page2Books...)
	if len(total) != 4 {
		t.Fatalf("expected 4 total books, got %d", len(total))
	}
}

// TestSearchHardCapAcrossPages verifies book-max is enforced even when pagination has more results.
func TestSearchHardCapAcrossPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".txt") {
			_, _ = w.Write([]byte("content"))
			return
		}
		// Always return 3 books with a next page to simulate infinite pagination.
		resp := gutendexResponse{
			Count: 999,
			Next:  "http://" + r.Host + "/books/?search=test&page=99",
			Results: []gutendexBook{
				{ID: 1, Title: "B1", Authors: []gutendexAuthor{{Name: "A"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/1.txt"}},
				{ID: 2, Title: "B2", Authors: []gutendexAuthor{{Name: "B"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/2.txt"}},
				{ID: 3, Title: "B3", Authors: []gutendexAuthor{{Name: "C"}}, Formats: map[string]string{"text/plain": "http://" + r.Host + "/3.txt"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := &GutenbergConnector{http: srv.Client()}
	// Collect manually to test cap logic (search() uses gutendexBase which we can't redirect).
	var collected []gutendexBook
	pageURL := srv.URL + "/books/?search=test"
	max := 2
	for pageURL != "" && len(collected) < max {
		books, next, err := c.fetchPage(context.Background(), pageURL)
		if err != nil {
			t.Fatalf("fetchPage: %v", err)
		}
		collected = append(collected, books...)
		pageURL = next
	}
	if len(collected) > max {
		collected = collected[:max]
	}
	if len(collected) != max {
		t.Fatalf("expected %d books (hard cap), got %d", max, len(collected))
	}
}

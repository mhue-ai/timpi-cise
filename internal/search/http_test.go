package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

func TestHTTPAdapterParsesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "hello world" {
			t.Errorf("query param = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"results":[
			{"title":"First","url":"https://a.example","snippet":"one"},
			{"title":"Second","url":"https://b.example","snippet":"two"}
		]}}`))
	}))
	defer srv.Close()

	a := newHTTPAdapter("test", httpAdapterOpts{
		endpoint: srv.URL, method: "GET", queryParam: "q",
		itemsPath: "data.results", titleKey: "title", urlKey: "url", snippetKey: "snippet",
	})
	res, err := a.Search(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Status != 200 || res.Count != 2 {
		t.Fatalf("status=%d count=%d", res.Status, res.Count)
	}
	if res.Items[0].Title != "First" || res.Items[1].URL != "https://b.example" {
		t.Errorf("items: %+v", res.Items)
	}
	if res.Note != "" {
		t.Errorf("unexpected note: %q", res.Note)
	}
}

func TestHTTPAdapterHTMLHonesty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>app shell</body></html>"))
	}))
	defer srv.Close()

	a := newHTTPAdapter("test", httpAdapterOpts{endpoint: srv.URL + "?q={query}", method: "GET"})
	res, err := a.Search(context.Background(), "x")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Count != 0 || res.Note == "" {
		t.Errorf("expected HTML honesty note, got count=%d note=%q", res.Count, res.Note)
	}
}

func TestHTTPAdapterRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	a := newHTTPAdapter("test", httpAdapterOpts{endpoint: srv.URL, method: "GET", queryParam: "q"})
	res, err := a.Search(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if res.Status != 429 || res.RetryAfter != 42*time.Second {
		t.Errorf("status=%d retryAfter=%v", res.Status, res.RetryAfter)
	}
}

func TestWalkPathAndAsString(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": []any{1.0, 2.0}}}
	if v := walkPath(root, "a.b"); v == nil {
		t.Error("walkPath a.b should resolve")
	}
	if v := walkPath(root, "a.missing"); v != nil {
		t.Error("missing key should be nil")
	}
	if asString(3.0) != "3" || asString(nil) != "" || asString(true) != "true" {
		t.Error("asString conversions wrong")
	}
}

func TestParseRetryAfter(t *testing.T) {
	if parseRetryAfter("30") != 30*time.Second {
		t.Error("numeric retry-after")
	}
	if parseRetryAfter("") != 0 {
		t.Error("empty retry-after should be 0")
	}
}

func TestBuildAdapterModes(t *testing.T) {
	if newBrowserAdapter(config.Browser{}, "").Name() != "browser" {
		t.Error("browser name")
	}
	if (dryRun{}).Name() != "dry-run" {
		t.Error("dry-run name")
	}
}

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type httpAdapterOpts struct {
	endpoint   string
	method     string
	queryParam string
	userAgent  string
	apiKey     string
	keyHeader  string
	itemsPath  string
	titleKey   string
	urlKey     string
	snippetKey string
	browserish bool // add browser-like Accept headers (public-web mode)
}

// httpAdapter is a configuration-driven HTTP search adapter shared by the
// public-web and official-api modes.
type httpAdapter struct {
	name string
	opts httpAdapterOpts
	hc   *http.Client
}

func newHTTPAdapter(name string, o httpAdapterOpts) *httpAdapter {
	if strings.TrimSpace(o.method) == "" {
		o.method = http.MethodGet
	}
	return &httpAdapter{
		name: name,
		opts: o,
		hc:   &http.Client{Timeout: 25 * time.Second},
	}
}

func (a *httpAdapter) Name() string { return a.name }

func (a *httpAdapter) Search(ctx context.Context, query string) (Result, error) {
	target, body, err := a.buildURL(query)
	if err != nil {
		return Result{Query: query}, err
	}

	method := strings.ToUpper(a.opts.method)
	var reqBody io.Reader
	if method == http.MethodPost {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reqBody)
	if err != nil {
		return Result{Query: query}, err
	}
	a.setHeaders(req, method)

	start := time.Now()
	resp, err := a.hc.Do(req)
	if err != nil {
		return Result{Query: query, LatencyMS: time.Since(start).Milliseconds()}, err
	}
	defer resp.Body.Close()
	latency := time.Since(start).Milliseconds()

	// Cap the amount we read so a huge response cannot exhaust memory.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	res := Result{
		Query:     query,
		Status:    resp.StatusCode,
		LatencyMS: latency,
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		res.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return res, fmt.Errorf("%s: rate-limited (%d)", a.name, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res, fmt.Errorf("%s: status %d", a.name, resp.StatusCode)
	}

	items, count := a.parseItems(raw)
	res.Items = items
	res.Count = count

	// Honesty check: a 2xx that returns an HTML page (not JSON) almost always
	// means the URL is a web page, not a search API — e.g. a single-page-app
	// shell. Surface that rather than reporting a hollow "success".
	if count == 0 {
		ct := resp.Header.Get("Content-Type")
		if looksHTML(ct, raw) {
			res.Note = "endpoint returned an HTML page, not JSON results — check the endpoint URL (and results JSON path)"
		} else if a.opts.itemsPath != "" {
			res.Note = "no results parsed — check the results JSON path"
		}
	}
	return res, nil
}

// looksHTML reports whether the response is an HTML document rather than data.
func looksHTML(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	trimmed := strings.TrimSpace(string(body[:min(len(body), 256)]))
	low := strings.ToLower(trimmed)
	return strings.HasPrefix(low, "<!doctype html") || strings.HasPrefix(low, "<html")
}

func (a *httpAdapter) buildURL(query string) (target, body string, err error) {
	ep := a.opts.endpoint
	if strings.Contains(ep, "{query}") {
		return strings.ReplaceAll(ep, "{query}", url.QueryEscape(query)), "", nil
	}
	if a.opts.queryParam == "" {
		return "", "", fmt.Errorf("%s: endpoint has no {query} token and no query_param set", a.name)
	}
	if strings.ToUpper(a.opts.method) == http.MethodPost {
		// Send as an application/x-www-form-urlencoded body by default.
		vals := url.Values{}
		vals.Set(a.opts.queryParam, query)
		return ep, vals.Encode(), nil
	}
	u, err := url.Parse(ep)
	if err != nil {
		return "", "", err
	}
	q := u.Query()
	q.Set(a.opts.queryParam, query)
	u.RawQuery = q.Encode()
	return u.String(), "", nil
}

func (a *httpAdapter) setHeaders(req *http.Request, method string) {
	req.Header.Set("User-Agent", a.opts.userAgent)
	req.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.8")
	if a.opts.browserish {
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if a.opts.apiKey != "" {
		if a.opts.keyHeader != "" {
			req.Header.Set(a.opts.keyHeader, a.opts.apiKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+a.opts.apiKey)
		}
	}
}

// parseItems extracts result items if the response is JSON and itemsPath is
// configured. If parsing is not possible it returns no items but still reports a
// count of 0, leaving the caller to treat a 2xx as a successful hit.
func (a *httpAdapter) parseItems(raw []byte) ([]Item, int) {
	if a.opts.itemsPath == "" {
		return nil, 0
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, 0
	}
	node := walkPath(root, a.opts.itemsPath)
	arr, ok := node.([]any)
	if !ok {
		return nil, 0
	}
	items := make([]Item, 0, len(arr))
	for _, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, Item{
			Title:   asString(obj[a.opts.titleKey]),
			URL:     asString(obj[a.opts.urlKey]),
			Snippet: asString(obj[a.opts.snippetKey]),
		})
	}
	return items, len(arr)
}

// walkPath resolves a dotted path like "data.results" within decoded JSON.
func walkPath(root any, path string) any {
	cur := root
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

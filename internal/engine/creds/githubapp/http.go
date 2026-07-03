// Package githubapp mints ephemeral, repo-scoped GitHub App installation tokens without the `gh`
// CLI: it builds an RS256 App JWT from a private key held only in host memory (specs/0069 T3),
// then calls the REST API through the ForgeHTTP seam so tests run fully hermetic (httptest, no
// live network — AGENTS.md). No token or PEM byte ever appears in an error string.
package githubapp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// ForgeHTTP is the minimal transport seam for GitHub REST calls (sibling of creds.forgejoDo). The
// real impl is net/http with a timeout; tests substitute a fake pointed at httptest. Do returns
// (body, statusCode, error); a non-2xx status is NOT an error here — callers map status to meaning.
type ForgeHTTP interface {
	Do(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, int, error)
}

// httpClient is the production ForgeHTTP.
type httpClient struct{ c *http.Client }

// NewHTTP returns the net/http-backed ForgeHTTP with a bounded timeout.
func NewHTTP() ForgeHTTP {
	return &httpClient{c: &http.Client{Timeout: 30 * time.Second}}
}

func (h *httpClient) Do(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, int, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

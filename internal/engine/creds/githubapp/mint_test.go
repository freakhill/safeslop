package githubapp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// fakeHTTP records the last request and returns a canned response.
type fakeHTTP struct {
	method, url string
	headers     map[string]string
	body        []byte

	respBody   []byte
	respStatus int
	respErr    error
}

func (f *fakeHTTP) Do(_ context.Context, method, url string, headers map[string]string, body []byte) ([]byte, int, error) {
	f.method, f.url, f.headers, f.body = method, url, headers, body
	return f.respBody, f.respStatus, f.respErr
}

func TestMintTokenRequestBodyAndParse(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)
	f := &fakeHTTP{
		respStatus: 201,
		respBody:   []byte(`{"token":"ghs_abc123","expires_at":"2026-07-03T13:00:00Z"}`),
	}
	c := New(f, "http://example.test")

	tok, err := c.MintToken(context.Background(), 42, 99, keyPEM, MintRequest{
		Repositories: []string{"api", "web"},
		Permissions:  map[string]string{"contents": "write", "metadata": "read"},
	})
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if tok.Token != "ghs_abc123" {
		t.Fatalf("token = %q", tok.Token)
	}
	if tok.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z") != "2026-07-03T13:00:00Z" {
		t.Fatalf("expiresAt = %v", tok.ExpiresAt)
	}

	if f.method != "POST" || f.url != "http://example.test/app/installations/99/access_tokens" {
		t.Fatalf("wrong request line: %s %s", f.method, f.url)
	}
	var sent struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(f.body, &sent); err != nil {
		t.Fatalf("request body parse: %v", err)
	}
	if !reflect.DeepEqual(sent.Repositories, []string{"api", "web"}) {
		t.Fatalf("repositories = %v", sent.Repositories)
	}
	if !reflect.DeepEqual(sent.Permissions, map[string]string{"contents": "write", "metadata": "read"}) {
		t.Fatalf("permissions = %v", sent.Permissions)
	}
	if !strings.HasPrefix(f.headers["Authorization"], "Bearer ") {
		t.Fatalf("missing bearer auth: %v", f.headers)
	}
}

func TestMintEmptyPermissionsDenied(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)
	f := &fakeHTTP{respStatus: 201}
	c := New(f, "http://example.test")
	_, err := c.MintToken(context.Background(), 42, 99, keyPEM, MintRequest{Repositories: []string{"api"}})
	if err == nil || !strings.Contains(err.Error(), "no permissions") {
		t.Fatalf("empty permissions must be denied, got %v", err)
	}
	if f.method != "" {
		t.Fatal("must deny before any HTTP call")
	}
}

func TestMintTooManyReposDenied(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)
	repos := make([]string, 501)
	for i := range repos {
		repos[i] = "r"
	}
	c := New(&fakeHTTP{respStatus: 201}, "http://example.test")
	_, err := c.MintToken(context.Background(), 42, 99, keyPEM, MintRequest{
		Repositories: repos,
		Permissions:  map[string]string{"contents": "read"},
	})
	if err == nil || !strings.Contains(err.Error(), "too many repositories") {
		t.Fatalf("501 repos must be denied, got %v", err)
	}
}

func TestMint422MapsToInstallGuidance(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)
	f := &fakeHTTP{respStatus: 422, respBody: []byte(`{"message":"Unprocessable"}`)}
	c := New(f, "http://example.test")
	_, err := c.MintToken(context.Background(), 42, 99, keyPEM, MintRequest{
		Repositories: []string{"api"},
		Permissions:  map[string]string{"contents": "read"},
	})
	if err == nil || !strings.Contains(err.Error(), "install the GitHub App") {
		t.Fatalf("422 must map to install guidance, got %v", err)
	}
	if !strings.Contains(err.Error(), "api") {
		t.Fatalf("guidance should name the repo, got %v", err)
	}
}

func TestMintErrorNeverLeaksTokenBytes(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)
	// A 500 whose body happens to carry a token-shaped string must not surface in the error.
	f := &fakeHTTP{respStatus: 500, respBody: []byte(`{"token":"ghs_SUPERSECRET"}`)}
	c := New(f, "http://example.test")
	_, err := c.MintToken(context.Background(), 42, 99, keyPEM, MintRequest{
		Repositories: []string{"api"},
		Permissions:  map[string]string{"contents": "read"},
	})
	if err == nil {
		t.Fatal("500 must error")
	}
	if strings.Contains(err.Error(), "ghs_SUPERSECRET") {
		t.Fatalf("error leaked token bytes: %v", err)
	}
}

func TestInstallationInfo(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)
	f := &fakeHTTP{
		respStatus: 200,
		respBody:   []byte(`{"id":99,"app_id":42,"account":{"login":"acme","type":"Organization"}}`),
	}
	c := New(f, "http://example.test")
	inst, err := c.InstallationInfo(context.Background(), 42, 99, keyPEM)
	if err != nil {
		t.Fatalf("InstallationInfo: %v", err)
	}
	if inst.AccountLogin() != "acme" {
		t.Fatalf("account login = %q", inst.AccountLogin())
	}
	if f.method != "GET" || f.url != "http://example.test/app/installations/99" {
		t.Fatalf("wrong request: %s %s", f.method, f.url)
	}
}

func TestInstallationInfoNotFound(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)
	c := New(&fakeHTTP{respStatus: 404}, "http://example.test")
	_, err := c.InstallationInfo(context.Background(), 42, 99, keyPEM)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("404 must map to not-found guidance, got %v", err)
	}
}

func TestRevoke404And401AreSuccess(t *testing.T) {
	for _, status := range []int{204, 401, 404} {
		c := New(&fakeHTTP{respStatus: status}, "http://example.test")
		if err := c.Revoke(context.Background(), "ghs_dead"); err != nil {
			t.Fatalf("status %d must be success, got %v", status, err)
		}
	}
}

func TestRevokeOtherErrorReturned(t *testing.T) {
	c := New(&fakeHTTP{respStatus: 500}, "http://example.test")
	if err := c.Revoke(context.Background(), "ghs_x"); err == nil {
		t.Fatal("500 revoke must return an error for logging")
	}
}

func TestRevokeUsesTokenAsBearer(t *testing.T) {
	f := &fakeHTTP{respStatus: 204}
	c := New(f, "http://example.test")
	if err := c.Revoke(context.Background(), "ghs_live"); err != nil {
		t.Fatal(err)
	}
	if f.method != "DELETE" || f.url != "http://example.test/installation/token" {
		t.Fatalf("wrong request: %s %s", f.method, f.url)
	}
	if f.headers["Authorization"] != "Bearer ghs_live" {
		t.Fatalf("revoke must auth with the token itself, got %v", f.headers["Authorization"])
	}
}

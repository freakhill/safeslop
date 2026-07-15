package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

func writeForgejoGCAccounts(t *testing.T) string {
	t.Helper()
	t.Setenv("TEST_FORGEJO_GC_TOKEN", "test-token-not-output")
	path := filepath.Join(t.TempDir(), "accounts.cue")
	accounts := &userconfig.Accounts{Accounts: map[string]userconfig.Account{}}
	accounts.Upsert(userconfig.Account{Forge: "forgejo", Host: "forge.example", Owner: "acme", Forgejo: &userconfig.ForgejoAccount{TokenRef: "env:TEST_FORGEJO_GC_TOKEN"}})
	if err := userconfig.SaveAccounts(path, accounts); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCredsGCCommandDefaultsToDryRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	accountsPath, err := accountsPathOrErr()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_FORGEJO_GC_TOKEN", "test-token-not-output")
	accounts := &userconfig.Accounts{Accounts: map[string]userconfig.Account{}}
	accounts.Upsert(userconfig.Account{Forge: "forgejo", Host: "forge.example", Owner: "acme", Forgejo: &userconfig.ForgejoAccount{TokenRef: "env:TEST_FORGEJO_GC_TOKEN"}})
	if err := userconfig.SaveAccounts(accountsPath, accounts); err != nil {
		t.Fatal(err)
	}
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodGet && r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`[{"id":1,"title":"safeslop-acme-web"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	previousBase := forgejoGCBaseForHost
	forgejoGCBaseForHost = func(string) string { return srv.URL }
	t.Cleanup(func() { forgejoGCBaseForHost = previousBase })

	out, err := runRootForTest(t, t.TempDir(), "creds", "gc", "--host", "forge.example", "--repo", "acme/web", "--output", "json")
	if err != nil {
		t.Fatalf("creds gc default dry run: %v\nout=%s", err, out)
	}
	if len(methods) != 2 || methods[0] != http.MethodGet || methods[1] != http.MethodGet {
		t.Fatalf("default dry run issued non-GET requests: %v", methods)
	}
	env := parseEnvelopeForTest(t, out)
	rows := env.Data["results"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["action"] != "dry-run" {
		t.Fatalf("dry-run output = %#v", env.Data)
	}
}

func TestRunCredsGCDeletesOnlyInitiallyDiscoveredExactTitles(t *testing.T) {
	accountsPath := writeForgejoGCAccounts(t)
	var requests []string
	var handlerErr string
	initialDiscoveries := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "token test-token-not-output" {
			handlerErr = "missing Forgejo authorization"
		}
		if r.Method == http.MethodDelete && (!initialDiscoveries["/api/v1/repos/acme/web/keys"] || !initialDiscoveries["/api/v1/repos/acme/api/keys"]) {
			handlerErr = "deletion preceded complete discovery"
		}
		if r.Method == http.MethodGet && r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/acme/web/keys":
			initialDiscoveries[r.URL.Path] = true
			_, _ = w.Write([]byte(`[{"id":1,"title":"safeslop-acme-web"},{"id":2,"title":"safeslop-acme-web-old"}]`))
		case "GET /api/v1/repos/acme/api/keys":
			initialDiscoveries[r.URL.Path] = true
			_, _ = w.Write([]byte(`[{"id":3,"title":"safeslop-acme-api"}]`))
		case "DELETE /api/v1/repos/acme/web/keys/1":
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /api/v1/repos/acme/api/keys/3":
			w.WriteHeader(http.StatusNotFound) // idempotent absence is not a GC failure.
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	report, err := runCredsGC(context.Background(), accountsPath, "forge.example", []string{"acme/web", "acme/api"}, true, false, func(string) string { return srv.URL })
	if err != nil {
		t.Fatalf("runCredsGC: %v", err)
	}
	if handlerErr != "" {
		t.Fatal(handlerErr)
	}
	if len(report.Results) != 2 {
		t.Fatalf("results = %#v", report.Results)
	}
	if report.Results[0].Action != "deleted" || report.Results[0].Title != "safeslop-acme-web" || report.Results[0].Count != 1 {
		t.Fatalf("web result = %#v", report.Results[0])
	}
	if report.Results[1].Action != "absent" || report.Results[1].Title != "safeslop-acme-api" || report.Results[1].Count != 0 {
		t.Fatalf("api result = %#v", report.Results[1])
	}
	for _, request := range requests {
		if strings.Contains(request, "keys/2") || strings.Contains(request, "old") {
			t.Fatalf("GC broadened its exact-title scope: %q", request)
		}
	}
	if encoded := renderCredsGCJSON(t, report); strings.Contains(encoded, "test-token-not-output") || strings.Contains(encoded, "env:TEST_FORGEJO_GC_TOKEN") || strings.Contains(encoded, "safeslop-acme-web-old") {
		t.Fatalf("GC output leaked a value, ref, or nonmatching title: %s", encoded)
	}
}

func TestRunCredsGCFollowsPaginationBeforeDeleting(t *testing.T) {
	accountsPath := writeForgejoGCAccounts(t)
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`[{"id":1,"title":"unrelated"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id":2,"title":"safeslop-acme-web"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	report, err := runCredsGC(context.Background(), accountsPath, "forge.example", []string{"acme/web"}, true, false, func(string) string { return srv.URL })
	if err != nil {
		t.Fatalf("runCredsGC: %v", err)
	}
	if !deleted || len(report.Results) != 1 || report.Results[0].Action != "deleted" {
		t.Fatalf("pagination report = %#v deleted=%v", report.Results, deleted)
	}
}

func TestRunCredsGCRecheckPreventsDeletingChangedKey(t *testing.T) {
	accountsPath := writeForgejoGCAccounts(t)
	getCount, deleted := 0, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		switch r.Method {
		case http.MethodGet:
			getCount++
			if getCount == 1 {
				_, _ = w.Write([]byte(`[{"id":1,"title":"safeslop-acme-web"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`)) // The candidate disappeared after discovery.
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	report, err := runCredsGC(context.Background(), accountsPath, "forge.example", []string{"acme/web"}, true, false, func(string) string { return srv.URL })
	if err != nil {
		t.Fatalf("runCredsGC: %v", err)
	}
	if getCount != 2 || deleted || report.Results[0].Action != "absent" {
		t.Fatalf("recheck getCount=%d deleted=%v report=%#v", getCount, deleted, report.Results)
	}
}

func TestRunCredsGCDiscoveryFailurePreventsAllDeletion(t *testing.T) {
	accountsPath := writeForgejoGCAccounts(t)
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = true
		}
		if r.Method == http.MethodGet && r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/repos/acme/web/keys":
			_, _ = w.Write([]byte(`[{"id":1,"title":"safeslop-acme-web"}]`))
		case "/api/v1/repos/acme/api/keys":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	report, err := runCredsGC(context.Background(), accountsPath, "forge.example", []string{"acme/web", "acme/api"}, true, false, func(string) string { return srv.URL })
	if err == nil {
		t.Fatal("a discovery failure must be nonzero")
	}
	if deleted {
		t.Fatal("a discovery failure must prevent every deletion")
	}
	if len(report.Results) != 2 || report.Results[1].Action != "error" || report.Results[1].ErrorClass != "http" {
		t.Fatalf("failure report = %#v", report.Results)
	}
}

func TestRunCredsGCDryRunNeverDeletes(t *testing.T) {
	accountsPath := writeForgejoGCAccounts(t)
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"title":"safeslop-acme-web"}]`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	report, err := runCredsGC(context.Background(), accountsPath, "forge.example", []string{"acme/web"}, false, true, func(string) string { return srv.URL })
	if err != nil {
		t.Fatalf("runCredsGC dry-run: %v", err)
	}
	if len(methods) != 2 || methods[0] != http.MethodGet || methods[1] != http.MethodGet || report.Results[0].Action != "dry-run" {
		t.Fatalf("dry run methods=%v report=%#v", methods, report.Results)
	}
}

func renderCredsGCJSON(t *testing.T, report forgejoGCReport) string {
	t.Helper()
	out, err := formatCredsGCOutput(report, "json")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

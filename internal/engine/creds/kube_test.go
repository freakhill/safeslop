package creds

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEksArgv(t *testing.T) {
	tok := strings.Join(eksGetTokenArgv("prod", "eu-west-1", "dev-admin"), " ")
	if tok != "aws eks get-token --cluster-name prod --output json --region eu-west-1 --profile dev-admin" {
		t.Fatalf("get-token argv = %q", tok)
	}
	desc := strings.Join(eksDescribeArgv("prod", "", ""), " ")
	if desc != "aws eks describe-cluster --name prod --output json" {
		t.Fatalf("describe argv = %q", desc)
	}
}

func TestGkeArgv(t *testing.T) {
	if got := strings.Join(gkeTokenArgv(), " "); got != "gke-gcloud-auth-plugin" {
		t.Fatalf("gke token argv = %q", got)
	}
	desc := strings.Join(gkeDescribeArgv("prod", "europe-west1", "acme"), " ")
	if desc != "gcloud container clusters describe prod --location europe-west1 --format json --project acme" {
		t.Fatalf("gke describe argv = %q", desc)
	}
}

func TestParseExecToken(t *testing.T) {
	out := `{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1beta1","status":{"expirationTimestamp":"2026-06-18T12:00:00Z","token":"k8s-aws-v1.aHR0cHM"}}`
	tok, err := parseExecToken([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if tok != "k8s-aws-v1.aHR0cHM" {
		t.Fatalf("token = %q", tok)
	}
	if _, err := parseExecToken([]byte(`{"status":{}}`)); err == nil {
		t.Fatal("expected error on empty token")
	}
}

func TestParseEksDescribe(t *testing.T) {
	out := `{"cluster":{"name":"prod","endpoint":"https://ABC.gr7.eu-west-1.eks.amazonaws.com","certificateAuthority":{"data":"Q0FEQVRB"},"status":"ACTIVE"}}`
	server, ca, err := parseEksDescribe([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://ABC.gr7.eu-west-1.eks.amazonaws.com" || ca != "Q0FEQVRB" {
		t.Fatalf("server=%q ca=%q", server, ca)
	}
	if _, _, err := parseEksDescribe([]byte(`{"cluster":{"endpoint":""}}`)); err == nil {
		t.Fatal("expected error on missing endpoint/ca")
	}
}

func TestParseGkeDescribe(t *testing.T) {
	out := `{"endpoint":"34.79.12.34","masterAuth":{"clusterCaCertificate":"Q0FEQVRB"}}`
	server, ca, err := parseGkeDescribe([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://34.79.12.34" || ca != "Q0FEQVRB" {
		t.Fatalf("server=%q ca=%q", server, ca)
	}
	if _, _, err := parseGkeDescribe([]byte(`{"endpoint":"","masterAuth":{}}`)); err == nil {
		t.Fatal("expected error on missing endpoint/ca")
	}
}

func TestRenderKubeconfig(t *testing.T) {
	raw := renderKubeconfig("eks:prod", "https://srv", "Q0FEQVRB", "k8s-aws-v1.tok")
	var kc map[string]any
	if err := json.Unmarshal(raw, &kc); err != nil {
		t.Fatalf("rendered kubeconfig is not valid JSON/YAML: %v", err)
	}
	if kc["current-context"] != "eks:prod" {
		t.Fatalf("current-context = %v", kc["current-context"])
	}
	clusters := kc["clusters"].([]any)
	cl := clusters[0].(map[string]any)["cluster"].(map[string]any)
	if cl["server"] != "https://srv" || cl["certificate-authority-data"] != "Q0FEQVRB" {
		t.Fatalf("cluster = %v", cl)
	}
	users := kc["users"].([]any)
	usr := users[0].(map[string]any)["user"].(map[string]any)
	if usr["token"] != "k8s-aws-v1.tok" {
		t.Fatalf("user token = %v", usr["token"])
	}
}

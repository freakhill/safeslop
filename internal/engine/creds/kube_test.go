package creds

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
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

// fakeMultiBin writes an executable stub that dispatches on $2 (the subcommand) to one
// of several heredoc outputs. Used because `aws` is invoked twice (get-token vs
// describe-cluster) with different required stdout.
func fakeMultiBin(t *testing.T, dir, name string, bySubcmd map[string]string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\ncase \"$2\" in\n")
	for sub, out := range bySubcmd {
		sb.WriteString(sub + ") cat <<'EOF'\n" + out + "\nEOF\n;;\n")
	}
	sb.WriteString("esac\n")
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sb.String()), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStageKubeEks(t *testing.T) {
	binDir := t.TempDir()
	fakeMultiBin(t, binDir, "aws", map[string]string{
		"get-token":        `{"kind":"ExecCredential","status":{"token":"k8s-aws-v1.TOK"}}`,
		"describe-cluster": `{"cluster":{"endpoint":"https://EKS.example","certificateAuthority":{"data":"Q0FEQVRB"}}}`,
	})
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH")) // fake `aws` wins; stub's `cat` resolves from real PATH

	stage := t.TempDir()
	env, err := StageKube(context.Background(),
		&policy.Credentials{Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "prod", Region: "eu-west-1"}}}, stage)
	if err != nil {
		t.Fatalf("StageKube: %v", err)
	}
	kcPath := filepath.Join(stage, "kubeconfig")
	if got := strings.Join(env, " "); got != "KUBECONFIG="+kcPath {
		t.Fatalf("env = %v", env)
	}
	fi, err := os.Stat(kcPath)
	if err != nil {
		t.Fatalf("kubeconfig not staged: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("kubeconfig perm = %v, want 0600", fi.Mode().Perm())
	}
	body, _ := os.ReadFile(kcPath)
	for _, want := range []string{`"server": "https://EKS.example"`, `"token": "k8s-aws-v1.TOK"`, `"certificate-authority-data": "Q0FEQVRB"`, `"current-context": "eks:prod"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("kubeconfig missing %q:\n%s", want, body)
		}
	}
}

func TestStageKubeGke(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "gke-gcloud-auth-plugin", `{"kind":"ExecCredential","status":{"token":"ya29.TOK"}}`) // fakeBin from aws_test.go
	fakeBin(t, binDir, "gcloud", `{"endpoint":"34.79.12.34","masterAuth":{"clusterCaCertificate":"Q0FEQVRB"}}`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	stage := t.TempDir()
	env, err := StageKube(context.Background(),
		&policy.Credentials{Kube: &policy.KubeCluster{Gke: &policy.GkeCluster{Name: "prod", Location: "europe-west1"}}}, stage)
	if err != nil {
		t.Fatalf("StageKube: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(stage, "kubeconfig"))
	for _, want := range []string{`"server": "https://34.79.12.34"`, `"token": "ya29.TOK"`, `"current-context": "gke:prod"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("kubeconfig missing %q:\n%s", want, body)
		}
	}
	_ = env
}

func TestStageKubeNilIsNoop(t *testing.T) {
	env, err := StageKube(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil kube creds must be a no-op: env=%v err=%v", env, err)
	}
}

func TestStageKubeRequiresExactlyOne(t *testing.T) {
	if _, err := StageKube(context.Background(),
		&policy.Credentials{Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "a"}, Gke: &policy.GkeCluster{Name: "b"}}}, t.TempDir()); err == nil {
		t.Fatal("expected error when both eks and gke set")
	}
	if _, err := StageKube(context.Background(), &policy.Credentials{Kube: &policy.KubeCluster{}}, t.TempDir()); err == nil {
		t.Fatal("expected error when neither eks nor gke set")
	}
}

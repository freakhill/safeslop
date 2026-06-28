package creds

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// ---- argv builders ----

func eksGetTokenArgv(name, region, profile string) []string {
	a := []string{"aws", "eks", "get-token", "--cluster-name", name, "--output", "json"}
	if region != "" {
		a = append(a, "--region", region)
	}
	if profile != "" {
		a = append(a, "--profile", profile)
	}
	return a
}

func eksDescribeArgv(name, region, profile string) []string {
	a := []string{"aws", "eks", "describe-cluster", "--name", name, "--output", "json"}
	if region != "" {
		a = append(a, "--region", region)
	}
	if profile != "" {
		a = append(a, "--profile", profile)
	}
	return a
}

func gkeTokenArgv() []string { return []string{"gke-gcloud-auth-plugin"} }

func gkeDescribeArgv(name, location, project string) []string {
	a := []string{"gcloud", "container", "clusters", "describe", name, "--location", location, "--format", "json"}
	if project != "" {
		a = append(a, "--project", project)
	}
	return a
}

// ---- parsers ----

// parseExecToken extracts status.token from a client.authentication.k8s.io
// ExecCredential — the shape emitted by both `aws eks get-token` and
// `gke-gcloud-auth-plugin`.
func parseExecToken(out []byte) (string, error) {
	var ec struct {
		Status struct {
			Token string `json:"token"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &ec); err != nil {
		return "", fmt.Errorf("parse ExecCredential token: %w", err)
	}
	if ec.Status.Token == "" {
		return "", fmt.Errorf("no k8s bearer token returned (cloud session expired? run: aws sso login / gcloud auth application-default login)")
	}
	return ec.Status.Token, nil
}

func parseEksDescribe(out []byte) (server, caData string, err error) {
	var d struct {
		Cluster struct {
			Endpoint             string `json:"endpoint"`
			CertificateAuthority struct {
				Data string `json:"data"`
			} `json:"certificateAuthority"`
		} `json:"cluster"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return "", "", fmt.Errorf("parse aws eks describe-cluster: %w", err)
	}
	if d.Cluster.Endpoint == "" || d.Cluster.CertificateAuthority.Data == "" {
		return "", "", fmt.Errorf("aws eks describe-cluster returned no endpoint/CA")
	}
	return d.Cluster.Endpoint, d.Cluster.CertificateAuthority.Data, nil
}

func parseGkeDescribe(out []byte) (server, caData string, err error) {
	var d struct {
		Endpoint   string `json:"endpoint"`
		MasterAuth struct {
			ClusterCaCertificate string `json:"clusterCaCertificate"`
		} `json:"masterAuth"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return "", "", fmt.Errorf("parse gcloud container clusters describe: %w", err)
	}
	if d.Endpoint == "" || d.MasterAuth.ClusterCaCertificate == "" {
		return "", "", fmt.Errorf("gcloud container clusters describe returned no endpoint/CA")
	}
	return "https://" + d.Endpoint, d.MasterAuth.ClusterCaCertificate, nil
}

// ---- kubeconfig render ----

// kubeconfig is a minimal one-cluster kubeconfig. Rendered as JSON (valid YAML, so
// kubectl reads it directly); the bearer token is embedded, making this whole file the
// secret — staged 0600, wiped with the run on exit.
type kubeconfig struct {
	APIVersion     string      `json:"apiVersion"`
	Kind           string      `json:"kind"`
	Clusters       []kcCluster `json:"clusters"`
	Users          []kcUser    `json:"users"`
	Contexts       []kcContext `json:"contexts"`
	CurrentContext string      `json:"current-context"`
}

type kcCluster struct {
	Name    string `json:"name"`
	Cluster struct {
		Server                   string `json:"server"`
		CertificateAuthorityData string `json:"certificate-authority-data"`
	} `json:"cluster"`
}

type kcUser struct {
	Name string `json:"name"`
	User struct {
		Token string `json:"token"`
	} `json:"user"`
}

type kcContext struct {
	Name    string `json:"name"`
	Context struct {
		Cluster string `json:"cluster"`
		User    string `json:"user"`
	} `json:"context"`
}

func renderKubeconfig(ctxName, server, caData, token string) []byte {
	var cl kcCluster
	cl.Name = ctxName
	cl.Cluster.Server = server
	cl.Cluster.CertificateAuthorityData = caData

	var us kcUser
	us.Name = ctxName
	us.User.Token = token

	var cx kcContext
	cx.Name = ctxName
	cx.Context.Cluster = ctxName
	cx.Context.User = ctxName

	kc := kubeconfig{
		APIVersion:     "v1",
		Kind:           "Config",
		Clusters:       []kcCluster{cl},
		Users:          []kcUser{us},
		Contexts:       []kcContext{cx},
		CurrentContext: ctxName,
	}
	b, _ := json.MarshalIndent(kc, "", "  ")
	return b
}

// runKubeCmd executes argv and returns stdout, wrapping failures with a hint. The error
// label is argv[0] (+ argv[1] when present) so single-token commands like
// `gke-gcloud-auth-plugin` don't index out of range.
func runKubeCmd(ctx context.Context, argv []string, hint string) ([]byte, error) {
	out, err := osexec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		label := argv[0]
		if len(argv) > 1 {
			label += " " + argv[1]
		}
		return nil, fmt.Errorf("%s (%s): %w", label, hint, err)
	}
	return out, nil
}

// StageKube pre-mints a short-lived k8s bearer token on the host (aws eks get-token /
// gke-gcloud-auth-plugin, using the host's SSO/ADC), resolves the cluster endpoint+CA,
// and writes a scoped one-cluster kubeconfig (token inside, 0600) into stageDir. It
// returns KUBECONFIG pointing at that file — the host path, correct for host;
// the container path is set in the compose env (see container.Launch). No revoke: the
// token decays and the stageDir wipe removes the file (decay-first).
func StageKube(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Kube == nil {
		return nil, nil
	}
	k := creds.Kube
	if (k.Eks == nil) == (k.Gke == nil) {
		return nil, fmt.Errorf("kube credentials: set exactly one of eks/gke")
	}

	var server, caData, token, ctxName string
	switch {
	case k.Eks != nil:
		tOut, err := runKubeCmd(ctx, eksGetTokenArgv(k.Eks.Name, k.Eks.Region, k.Eks.Profile), "is `aws sso login` current?")
		if err != nil {
			return nil, err
		}
		if token, err = parseExecToken(tOut); err != nil {
			return nil, err
		}
		dOut, err := runKubeCmd(ctx, eksDescribeArgv(k.Eks.Name, k.Eks.Region, k.Eks.Profile), "can the SSO profile describe the cluster?")
		if err != nil {
			return nil, err
		}
		if server, caData, err = parseEksDescribe(dOut); err != nil {
			return nil, err
		}
		ctxName = "eks:" + k.Eks.Name
	case k.Gke != nil:
		tOut, err := runKubeCmd(ctx, gkeTokenArgv(), "is ADC set up? run: gcloud auth application-default login")
		if err != nil {
			return nil, err
		}
		if token, err = parseExecToken(tOut); err != nil {
			return nil, err
		}
		dOut, err := runKubeCmd(ctx, gkeDescribeArgv(k.Gke.Name, k.Gke.Location, k.Gke.Project), "can gcloud describe the cluster?")
		if err != nil {
			return nil, err
		}
		if server, caData, err = parseGkeDescribe(dOut); err != nil {
			return nil, err
		}
		ctxName = "gke:" + k.Gke.Name
	}

	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	kcPath := filepath.Join(stageDir, "kubeconfig")
	if err := os.WriteFile(kcPath, renderKubeconfig(ctxName, server, caData, token), 0o600); err != nil {
		return nil, err
	}
	return []string{"KUBECONFIG=" + kcPath}, nil
}

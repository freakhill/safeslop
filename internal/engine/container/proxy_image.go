package container

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	reviewedProxyImage = "docker.io/ubuntu/squid"
	reviewedProxyTag   = "5.2-22.04_beta"
)

type proxyImageLock struct {
	SchemaVersion int               `json:"schemaVersion"`
	Image         string            `json:"image"`
	Tag           string            `json:"tag"`
	IndexDigest   string            `json:"indexDigest"`
	IndexFile     string            `json:"indexFile"`
	Manifests     map[string]string `json:"manifests"`
}

type proxyOCIIndex struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Manifests     []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
		Platform  struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant,omitempty"`
		} `json:"platform"`
	} `json:"manifests"`
}

func loadProxyImageLock() (proxyImageLock, error) {
	body, err := readAsset("proxy-image.lock.json")
	if err != nil {
		return proxyImageLock{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var lock proxyImageLock
	if err := decoder.Decode(&lock); err != nil {
		return proxyImageLock{}, fmt.Errorf("decode proxy image lock: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return proxyImageLock{}, fmt.Errorf("decode proxy image lock: trailing data")
	}
	if lock.IndexFile != "proxy-image.index.json" {
		return proxyImageLock{}, fmt.Errorf("proxy image lock has an unreviewed index artifact")
	}
	indexBody, err := readAsset(lock.IndexFile)
	if err != nil {
		return proxyImageLock{}, err
	}
	if err := validateProxyImageLock(lock, indexBody); err != nil {
		return proxyImageLock{}, err
	}
	return lock, nil
}

func validateProxyImageLock(lock proxyImageLock, indexBody []byte) error {
	if lock.SchemaVersion != 1 || lock.Image != reviewedProxyImage || lock.Tag != reviewedProxyTag || lock.IndexFile != "proxy-image.index.json" {
		return fmt.Errorf("proxy image lock has an unreviewed identity")
	}
	if !validOCIDigest(lock.IndexDigest) || len(lock.Manifests) != 2 {
		return fmt.Errorf("proxy image lock has an invalid OCI index")
	}
	indexHash := sha256.Sum256(indexBody)
	if "sha256:"+hex.EncodeToString(indexHash[:]) != lock.IndexDigest {
		return fmt.Errorf("proxy image index bytes do not match the lock")
	}
	var index proxyOCIIndex
	if err := json.Unmarshal(indexBody, &index); err != nil || index.SchemaVersion != 2 || index.MediaType != "application/vnd.docker.distribution.manifest.list.v2+json" {
		return fmt.Errorf("proxy image index artifact is invalid")
	}
	found := map[string]int{}
	for _, descriptor := range index.Manifests {
		if descriptor.Size <= 0 || !validOCIDigest(descriptor.Digest) {
			return fmt.Errorf("proxy image index contains an invalid descriptor")
		}
		platform := descriptor.Platform.OS + "/" + descriptor.Platform.Architecture
		expected, required := lock.Manifests[platform]
		if !required {
			continue
		}
		if descriptor.MediaType != "application/vnd.docker.distribution.manifest.v2+json" || descriptor.Digest != expected {
			return fmt.Errorf("proxy image index has an unreviewed %s descriptor", platform)
		}
		if platform == "linux/arm64" && descriptor.Platform.Variant != "v8" {
			return fmt.Errorf("proxy image index has an unreviewed arm64 variant")
		}
		found[platform]++
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		digest, ok := lock.Manifests[platform]
		if !ok || !validOCIDigest(digest) || digest == lock.IndexDigest || found[platform] != 1 {
			return fmt.Errorf("proxy image lock has an invalid %s manifest", platform)
		}
	}
	return nil
}

func validOCIDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") || value == "sha256:"+strings.Repeat("0", 64) {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func proxyImageReference() (string, error) {
	lock, err := loadProxyImageLock()
	if err != nil {
		return "", err
	}
	return lock.Image + "@" + lock.IndexDigest, nil
}

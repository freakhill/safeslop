package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"aead.dev/minisign"
)

// VerifySHA256 fails closed unless sha256(data) hex-equals want (case-insensitive).
func VerifySHA256(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, want)
	}
	return nil
}

// sha256Hex is the lowercase hex sha256 of b — the form recorded in install receipts (receipt.File.SHA256).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sha256File hashes the file at path. Apply records this for each placed artifact so uninstall can
// verify the on-disk bytes still match what safeslop wrote before unlinking (specs/0041).
func sha256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Hex(b), nil
}

// VerifyMinisign verifies the upstream signature chain, fail-closed:
//  1. sig is a valid minisign signature over sums (the SHASUMS256.txt bytes), under pubKey;
//  2. a line of sums contains both artifactSHA and artifactName (the pin's artifact is covered).
//
// This is why a copied sha256 isn't enough: the maintainer's key signs the checksum file, and the
// artifact we fetched must appear inside that signed file (specs/0012 §10.2).
func VerifyMinisign(pubKey string, sums, sig []byte, artifactSHA, artifactName string) error {
	var pk minisign.PublicKey
	if err := pk.UnmarshalText([]byte(pubKey)); err != nil {
		return fmt.Errorf("bad minisign public key: %w", err)
	}
	if !minisign.Verify(pk, sums, sig) {
		return fmt.Errorf("minisign signature does not verify against the checksum file")
	}
	for _, line := range strings.Split(string(sums), "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(artifactSHA)) && strings.Contains(line, artifactName) {
			return nil
		}
	}
	return fmt.Errorf("artifact %q (%s) not found in the signed checksum file", artifactName, artifactSHA)
}

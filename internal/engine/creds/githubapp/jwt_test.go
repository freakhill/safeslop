package githubapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

// testKeyPEM returns a fresh PKCS#1 RSA key as PEM plus the parsed key for signature checks.
func testKeyPEM(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	return p, k
}

func TestAppJWTHeaderClaimsAndSignature(t *testing.T) {
	keyPEM, key := testKeyPEM(t)
	now := time.Unix(1_700_000_000, 0)

	tok, err := AppJWT(4242, keyPEM, now)
	if err != nil {
		t.Fatalf("AppJWT: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT must have 3 parts, got %d", len(parts))
	}

	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if string(hdr) != `{"alg":"RS256","typ":"JWT"}` {
		t.Fatalf("header = %s", hdr)
	}

	claimsRaw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
		Iss int   `json:"iss"`
	}
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		t.Fatalf("claims parse: %v", err)
	}
	if claims.Iss != 4242 {
		t.Fatalf("iss = %d, want 4242", claims.Iss)
	}
	if claims.Iat != now.Unix()-60 {
		t.Fatalf("iat = %d, want %d (now-60s)", claims.Iat, now.Unix()-60)
	}
	if claims.Exp != now.Unix()+540 {
		t.Fatalf("exp = %d, want %d (now+9m)", claims.Exp, now.Unix()+540)
	}

	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
}

func TestAppJWTAcceptsPKCS8(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := AppJWT(1, p, time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatalf("PKCS#8 key must be accepted: %v", err)
	}
}

func TestAppJWTRejectsGarbageKey(t *testing.T) {
	_, err := AppJWT(1, []byte("not a pem"), time.Now())
	if err == nil {
		t.Fatal("garbage key must error")
	}
	// Error must not echo the (would-be secret) key bytes.
	if strings.Contains(err.Error(), "not a pem") {
		t.Fatalf("error leaked key bytes: %v", err)
	}
}

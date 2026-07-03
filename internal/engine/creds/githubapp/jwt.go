package githubapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// AppJWT builds an RS256-signed GitHub App JWT valid around `now`. Per GitHub's clock-skew
// guidance (specs/0068 G2) iat is backdated 60s and exp is now+9m (under the 10m ceiling). The
// key is parsed from PEM held only in host memory; no key byte is ever surfaced in an error.
func AppJWT(appID int, keyPEM []byte, now time.Time) (string, error) {
	key, err := parseRSAKey(keyPEM)
	if err != nil {
		return "", err
	}
	header := b64(`{"alg":"RS256","typ":"JWT"}`)
	claims := b64(fmt.Sprintf(`{"iat":%d,"exp":%d,"iss":%d}`,
		now.Add(-60*time.Second).Unix(), now.Add(9*time.Minute).Unix(), appID))
	signingInput := header + "." + claims

	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", errors.New("github: signing App JWT failed")
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// parseRSAKey accepts a PKCS#1 ("RSA PRIVATE KEY") or PKCS#8 ("PRIVATE KEY") RSA PEM — the two
// shapes GitHub hands out. Error messages never echo key bytes (deepseek R1 gap, specs/0069 T3).
func parseRSAKey(keyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("github: invalid private key PEM (no key block found)")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	anyKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("github: unsupported private key (want PKCS#1 or PKCS#8 RSA)")
	}
	k, ok := anyKey.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github: private key is not RSA")
	}
	return k, nil
}

package install

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"aead.dev/minisign"
)

func TestVerifySHA256(t *testing.T) {
	data := []byte("hello safeslop")
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	if err := VerifySHA256(data, want); err != nil {
		t.Fatalf("matching digest must verify: %v", err)
	}
	if err := VerifySHA256(data, "00"+want[2:]); err == nil {
		t.Fatal("a wrong digest must fail closed")
	}
}

func TestVerifyMinisignChain(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const artSHA = "084c352a9c5d1a19bd31fef84ba9692952aa04e8d2e3fe666948db35dedfaf95"
	sums := []byte(artSHA + "  ./mise.tar.gz\n")
	sig := minisign.Sign(priv, sums)

	if err := VerifyMinisign(pub.String(), sums, sig, artSHA, "./mise.tar.gz"); err != nil {
		t.Fatalf("valid chain must verify: %v", err)
	}
	if err := VerifyMinisign(pub.String(), append([]byte{}, append(sums, 'x')...), sig, artSHA, "./mise.tar.gz"); err == nil {
		t.Fatal("tampered checksum file must fail the signature")
	}
	if err := VerifyMinisign(pub.String(), sums, sig,
		"deadbeef00000000000000000000000000000000000000000000000000000000", "./mise.tar.gz"); err == nil {
		t.Fatal("artifact sha absent from signed checksum file must fail closed")
	}
}

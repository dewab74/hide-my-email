package srp

import (
	"bytes"
	"math/big"
	"testing"
)

var (
	user    = []byte("someone@example.com")
	salt    = []byte("0123456789abcdef")
	passKey = bytes.Repeat([]byte{0x11}, 32)
)

func exchange(t *testing.T, clientKey []byte) bool {
	t.Helper()
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(user, salt, Verifier(salt, passKey), c.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	m1, m2, err := c.Proofs(user, clientKey, salt, s.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if len(m1) != 32 || len(m2) != 32 {
		t.Fatalf("proof lengths %d, %d", len(m1), len(m2))
	}
	return s.Check(m1)
}

func TestClientAndServerAgree(t *testing.T) {
	if !exchange(t, passKey) {
		t.Error("the server rejected a proof made with the right password")
	}
}

func TestWrongPasswordFailsProof(t *testing.T) {
	if exchange(t, bytes.Repeat([]byte{0x22}, 32)) {
		t.Error("a wrong password produced a valid proof")
	}
}

func TestRejectsMaliciousServerKey(t *testing.T) {
	c := newWithSecret(bytes.Repeat([]byte{0x07}, 32))
	for name, B := range map[string][]byte{
		"zero":       {0},
		"N":          bigN.Bytes(),
		"multiple N": new(big.Int).Mul(bigN, big.NewInt(2)).Bytes(),
	} {
		if _, _, err := c.Proofs(user, passKey, salt, B); err == nil {
			t.Errorf("B = %s was accepted", name)
		}
	}
}

func TestPublicKeyIsPadded(t *testing.T) {
	c := newWithSecret([]byte{1}) // A = g^1 = 2
	if got := c.PublicKey(); len(got) != nBytes || got[nBytes-1] != 2 {
		t.Errorf("PublicKey not padded to %d bytes", nBytes)
	}
}

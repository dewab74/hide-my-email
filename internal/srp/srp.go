// Package srp implements the client side of SRP-6a as Apple's idmsa
// sign-in uses it: RFC 5054 2048-bit group, SHA-256, and the user name
// left out of the private key x.
//
// With SRP the password never leaves the machine. The client proves it
// knows the password (M1) without sending it or anything it can be
// recovered from.
package srp

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

const nBytes = 256 // 2048-bit group

// RFC 5054 appendix A, 2048-bit group.
const nHex = "AC6BDB41324A9A9BF166DE5E1389582FAF72B6651987EE07FC3192943DB56050" +
	"A37329CBB4A099ED8193E0757767A13DD52312AB4B03310DCD7F48A9DA04FD50" +
	"E8083969EDB767B0CF6095179A163AB3661A05FBD5FAAAE82918A9962F0B93B8" +
	"55F97993EC975EEAA80D740ADBF4FF747359D041D5C33EA71D281E446B14773B" +
	"CA97B43A23FB801676BD207A436C6481F1D2B9078717461A5B9D32E688F87748" +
	"544523B524B0D57D5EA77A2775D2ECFA032CFBDBF52FB3786160279004E57AE6" +
	"AF874E7303CE53299CCC041C7BC308D82A5698F3A8D0C38271AE35F8E9DBFBB6" +
	"94B5C803D89F7AE435DE236D525F54759B65E372FCD68EF20FA7111F9E4AFF73"

var (
	bigN, _ = new(big.Int).SetString(nHex, 16)
	bigG    = big.NewInt(2)
)

// Client holds one sign-in attempt's ephemeral key pair. Use it once.
type Client struct {
	a *big.Int // private
	A *big.Int // public
}

// New creates a client with a fresh random ephemeral key.
func New() (*Client, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generating SRP secret: %w", err)
	}
	return newWithSecret(buf), nil
}

func newWithSecret(secret []byte) *Client {
	a := new(big.Int).SetBytes(secret)
	return &Client{a: a, A: new(big.Int).Exp(bigG, a, bigN)}
}

// PublicKey returns A, padded to the group size.
func (c *Client) PublicKey() []byte { return pad(c.A) }

// Proofs answers the server's challenge. It returns M1, the client's
// proof of the password, and M2, the proof the server is expected to be
// able to produce. passKey is the PBKDF2-derived password key.
func (c *Client) Proofs(username, passKey, salt, serverB []byte) (m1, m2 []byte, err error) {
	B := new(big.Int).SetBytes(serverB)
	// A server sending B = 0 mod N would fix the shared secret no
	// matter what the password is.
	if new(big.Int).Mod(B, bigN).Sign() == 0 || B.Cmp(bigN) >= 0 {
		return nil, nil, errors.New("invalid server public key")
	}
	u := new(big.Int).SetBytes(hash(pad(c.A), pad(B)))
	if u.Sign() == 0 {
		return nil, nil, errors.New("invalid SRP scrambling parameter")
	}

	x := computeX(salt, passKey)
	k := multiplier()

	// S = (B - k*g^x) ^ (a + u*x) mod N
	base := new(big.Int).Exp(bigG, x, bigN)
	base.Mul(base, k)
	base.Sub(B, base)
	base.Mod(base, bigN)
	exp := new(big.Int).Mul(u, x)
	exp.Add(exp, c.a)
	S := new(big.Int).Exp(base, exp, bigN)

	K := hash(pad(S))
	m1 = computeM1(username, salt, pad(c.A), pad(B), K)
	m2 = hash(pad(c.A), m1, K)
	return m1, m2, nil
}

func multiplier() *big.Int {
	return new(big.Int).SetBytes(hash(bigN.Bytes(), pad(bigG)))
}

// computeX is x = H(salt | H(":" | passKey)). Apple omits the user name.
func computeX(salt, passKey []byte) *big.Int {
	return new(big.Int).SetBytes(hash(salt, hash([]byte(":"), passKey)))
}

func computeM1(username, salt, A, B, K []byte) []byte {
	hN := hash(bigN.Bytes())
	hG := hash(pad(bigG))
	for i := range hN {
		hN[i] ^= hG[i]
	}
	return hash(hN, hash(username), salt, A, B, K)
}

func hash(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func pad(n *big.Int) []byte {
	return n.FillBytes(make([]byte, nBytes))
}

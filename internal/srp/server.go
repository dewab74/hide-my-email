package srp

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"math/big"
)

// The server side exists so tests can stand in for Apple and check the
// client's proof for real. Nothing in the hme binary uses it.

// Verifier computes the password verifier a server stores: g^x mod N.
func Verifier(salt, passKey []byte) []byte {
	return pad(new(big.Int).Exp(bigG, computeX(salt, passKey), bigN))
}

// Server is one sign-in attempt as the server sees it.
type Server struct {
	username, salt []byte
	v, b, B, A     *big.Int
}

// NewServer starts an exchange for a client that sent public key A.
func NewServer(username, salt, verifier, clientA []byte) (*Server, error) {
	A := new(big.Int).SetBytes(clientA)
	if new(big.Int).Mod(A, bigN).Sign() == 0 {
		return nil, errors.New("invalid client public key")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	v := new(big.Int).SetBytes(verifier)
	b := new(big.Int).SetBytes(secret)
	// B = k*v + g^b mod N
	B := new(big.Int).Mul(multiplier(), v)
	B.Add(B, new(big.Int).Exp(bigG, b, bigN))
	B.Mod(B, bigN)
	return &Server{username: username, salt: salt, v: v, b: b, B: B, A: A}, nil
}

// PublicKey returns B, padded to the group size.
func (s *Server) PublicKey() []byte { return pad(s.B) }

// Check reports whether m1 proves knowledge of the password.
func (s *Server) Check(m1 []byte) bool {
	u := new(big.Int).SetBytes(hash(pad(s.A), pad(s.B)))
	// S = (A * v^u) ^ b mod N
	S := new(big.Int).Exp(s.v, u, bigN)
	S.Mul(S, s.A)
	S.Exp(S, s.b, bigN)
	want := computeM1(s.username, s.salt, pad(s.A), pad(s.B), hash(pad(S)))
	return subtle.ConstantTimeCompare(want, m1) == 1
}

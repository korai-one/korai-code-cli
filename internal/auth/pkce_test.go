package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	p, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	// RFC 7636 requires the verifier to be 43–128 characters.
	if len(p.Verifier) < 43 || len(p.Verifier) > 128 {
		t.Errorf("verifier length %d out of [43,128]", len(p.Verifier))
	}
	// The challenge must be the unpadded base64url SHA-256 of the verifier.
	want := base64.RawURLEncoding.EncodeToString(sha256Sum(p.Verifier))
	if p.Challenge != want {
		t.Errorf("challenge = %q, want %q", p.Challenge, want)
	}
	// base64url must not contain padding or the standard-alphabet chars.
	for _, r := range p.Verifier + p.Challenge {
		if r == '=' || r == '+' || r == '/' {
			t.Errorf("non-base64url character %q present", r)
		}
	}
}

func TestGeneratePKCEUnique(t *testing.T) {
	a, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if a.Verifier == b.Verifier || a.Challenge == b.Challenge {
		t.Error("two PKCE pairs collided; entropy source is broken")
	}
}

func TestGenerateStateUnique(t *testing.T) {
	s1, err := GenerateState()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := GenerateState()
	if err != nil {
		t.Fatal(err)
	}
	if s1 == "" || s1 == s2 {
		t.Errorf("state not unique: %q vs %q", s1, s2)
	}
}

func sha256Sum(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

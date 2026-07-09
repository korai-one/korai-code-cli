// Package auth implements the CLI side of Korai's cross-device login: the
// OAuth device-authorization bridge (the `gh auth login` pattern). A browser
// that is already logged into the web app authorizes this CLI, which then holds
// its OWN rotating, revocable refresh session in ~/.korai/auth.json.
//
// The flow is Authorization Code + PKCE (S256 mandatory) against a loopback
// redirect, with an RFC 8628 device-grant fallback for headless/SSH shells.
// Two origins are involved: the orchestrator (the API, where /oauth/* live) and
// the web app (the human consent page opened in the browser). See the SSO_AUTH
// design doc in the sibling korai repo.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// ClientID is the first-party public OAuth client id for this CLI. It is not a
// secret (all Korai clients are public and use PKCE, no shippable secret).
const ClientID = "korai-cli"

// DefaultScope is the OAuth scope the CLI requests.
const DefaultScope = "openid email"

// PKCE is a generated code_verifier and its S256 code_challenge. The verifier
// stays on this device; only the challenge is sent to the authorization
// endpoint, and the verifier is revealed at the token endpoint to prove the
// same client that started the flow is finishing it.
type PKCE struct {
	Verifier  string
	Challenge string
}

// GeneratePKCE produces a fresh PKCE pair: a 43-char base64url verifier (32
// bytes of CSPRNG entropy, within RFC 7636's 43–128 range) and its
// base64url(sha256(verifier)) S256 challenge, both unpadded per the spec.
func GeneratePKCE() (PKCE, error) {
	verifier, err := randToken(32)
	if err != nil {
		return PKCE{}, fmt.Errorf("generating code verifier: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

// GenerateState returns an unguessable CSRF state value for the redirect leg.
func GenerateState() (string, error) {
	s, err := randToken(16)
	if err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	return s, nil
}

// randToken returns n bytes of CSPRNG entropy as an unpadded base64url string.
func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

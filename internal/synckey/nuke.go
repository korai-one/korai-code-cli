package synckey

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// nukePrefix tags the stored duress-wipe verifier. Only an Argon2id verifier is
// persisted — never the nuke code itself.
const nukePrefix = "korai-nuke:v1?"

// nukeSaltLen is the length of the random Argon2id salt in a nuke verifier.
const nukeSaltLen = 16

// nukeVerifierPerm keeps the verifier private to the user.
const nukeVerifierPerm os.FileMode = 0o600

// ErrVerifierFormat reports a malformed nuke verifier file.
var ErrVerifierFormat = errors.New("malformed nuke verifier")

// NukeVerifierPath returns where the duress-wipe verifier is stored under home.
func NukeVerifierPath(home string) string {
	return filepath.Join(home, ".korai", "nuke.verifier")
}

// SetNukeVerifier stores an Argon2id verifier for the duress-wipe code at
// NukeVerifierPath(home). The code itself is never written; only the salt,
// parameters, and the derived hash are. It is distinct from K_folder — entering
// it triggers a wipe, so it must not be a key the user would ever type normally.
func SetNukeVerifier(home, code string) error {
	if strings.TrimSpace(code) == "" {
		return errors.New("nuke code must not be empty")
	}
	salt := make([]byte, nukeSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("generating nuke salt: %w", err)
	}
	p := defaultArgon2
	hash := deriveArgon2(code, salt, p)

	q := url.Values{}
	q.Set("m", fmt.Sprintf("%d", p.Memory))
	q.Set("t", fmt.Sprintf("%d", p.Time))
	q.Set("p", fmt.Sprintf("%d", p.Threads))
	q.Set("s", base64.RawURLEncoding.EncodeToString(salt))
	q.Set("h", base64.RawURLEncoding.EncodeToString(hash))

	dir := filepath.Join(home, ".korai")
	if err := os.MkdirAll(dir, keyDirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	line := nukePrefix + q.Encode() + "\n"
	if err := os.WriteFile(NukeVerifierPath(home), []byte(line), nukeVerifierPerm); err != nil {
		return fmt.Errorf("writing nuke verifier: %w", err)
	}
	return nil
}

// VerifyNukeCode reports whether code matches the stored verifier. A missing
// verifier and a wrong code both return false with a nil error, so the caller
// cannot — and must not — distinguish "not configured" from "wrong code": the
// duress feature must not reveal whether it is even armed. Only a genuine I/O or
// parse error is returned.
func VerifyNukeCode(home, code string) (bool, error) {
	data, err := os.ReadFile(NukeVerifierPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading nuke verifier: %w", err)
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(data)), nukePrefix)
	if !ok {
		return false, ErrVerifierFormat
	}
	q, err := url.ParseQuery(rest)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrVerifierFormat, err)
	}
	p, err := parseArgon2Params(q)
	if err != nil {
		return false, err
	}
	salt, err := base64.RawURLEncoding.DecodeString(q.Get("s"))
	if err != nil || len(salt) == 0 {
		return false, fmt.Errorf("%w: bad salt", ErrVerifierFormat)
	}
	want, err := base64.RawURLEncoding.DecodeString(q.Get("h"))
	if err != nil || len(want) == 0 {
		return false, fmt.Errorf("%w: bad hash", ErrVerifierFormat)
	}
	got := deriveArgon2(code, salt, p)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

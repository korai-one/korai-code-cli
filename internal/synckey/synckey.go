// Package synckey owns the cross-device sync content key (K_folder) and
// everything derived from it: the sync_id namespace bearer, the human-friendly
// transports that move the key to another device (BIP39 mnemonic, terminal QR,
// passphrase-wrapped recovery), and the duress "nuke" wipe. It is the real key
// distribution layer that replaces step 2's placeholder key source (see
// docs/HISTORY_SYNC.md §10/§10b in the sibling korai repo).
//
// Invariants that must survive a public audit:
//   - K_folder is machine-generated 256-bit entropy; the user never chooses it,
//     so nothing here is grindable offline (the only opt-in grind surface is the
//     passphrase-wrapped recovery blob in recovery.go).
//   - sync_id is derived independently of any secret needed to decrypt, so the
//     hub — which necessarily learns sync_id — still cannot read anything.
//   - The key never leaves the user's devices; the transports move it
//     out-of-band, never through the hub.
package synckey

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	sdksession "github.com/korai-one/korai-sdk-go/session"
)

// KeyLen is the length in bytes of K_folder (a 256-bit key).
const KeyLen = 32

// syncIDInfo is the HMAC domain-separation label for the namespace-bearer
// derivation. Changing it re-namespaces every device, so it is frozen.
const syncIDInfo = "korai-sync-id"

// keyFilePerm keeps the persisted content key private to the user.
const keyFilePerm os.FileMode = 0o600

// keyDirPerm is the mode of the ~/.korai directory holding the key.
const keyDirPerm os.FileMode = 0o700

// ErrKeyLength reports a content key that is not exactly KeyLen bytes.
var ErrKeyLength = errors.New("sync content key must be 32 bytes")

// KeyPath returns the on-disk location of the content key file under home.
func KeyPath(home string) string {
	return filepath.Join(home, ".korai", "sync.key")
}

// Generate returns a fresh random 256-bit K_folder from the OS CSPRNG.
func Generate() ([]byte, error) {
	key := make([]byte, KeyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generating sync key: %w", err)
	}
	return key, nil
}

// Save persists key to KeyPath(home) as a hex line, 0600 in a 0700 dir. It is
// the single writer of the file that the SDK's session.LoadContentKey (and Load)
// read, so the encrypting Codec and the sync client pick the key up with no
// further wiring. key must be exactly KeyLen bytes.
func Save(home string, key []byte) error {
	if len(key) != KeyLen {
		return fmt.Errorf("%w: got %d", ErrKeyLength, len(key))
	}
	dir := filepath.Join(home, ".korai")
	if err := os.MkdirAll(dir, keyDirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	line := hex.EncodeToString(key) + "\n"
	if err := os.WriteFile(KeyPath(home), []byte(line), keyFilePerm); err != nil {
		return fmt.Errorf("writing sync key: %w", err)
	}
	return nil
}

// Load resolves K_folder using the same precedence as the encrypting codec
// (the KORAI_SYNC_KEY env var, then the key file), so a single reader backs the
// whole feature. ok is false with a nil error when no key is configured.
func Load(home string) (key []byte, ok bool, err error) {
	return sdksession.LoadContentKey(home)
}

// Exists reports whether a content key is currently configured (env or file).
func Exists(home string) bool {
	_, ok, err := Load(home)
	return ok && err == nil
}

// DeriveSyncID returns the opaque namespace bearer for key:
// base64url(HMAC-SHA256(K_folder, "korai-sync-id")). It is deterministic, so
// every device holding the same key targets the same hub namespace, and it
// reveals nothing about K_folder (learning it does not grant decryption).
func DeriveSyncID(key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(syncIDInfo))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

package synckey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// recoveryPrefix tags a passphrase-wrapped recovery blob. The blob is a single
// line so it is easy to copy into a password manager or print.
const recoveryPrefix = "korai-recovery:v1?"

// recoverySaltLen is the length of the random Argon2id salt in a recovery blob.
const recoverySaltLen = 16

// recoveryFilePerm keeps an exported recovery blob private to the user.
const recoveryFilePerm os.FileMode = 0o600

// ErrRecoveryFormat reports a malformed recovery blob.
var ErrRecoveryFormat = errors.New("malformed recovery blob")

// RecoveryPath returns where ExportRecovery writes the wrapped blob under home.
func RecoveryPath(home string) string {
	return filepath.Join(home, ".korai", "sync-recovery.txt")
}

// WrapRecovery encrypts K_folder under a key stretched from passphrase with
// Argon2id, returning a single-line, self-describing blob. This is the ONLY
// offline-grindable surface in the design, and it is opt-in: the passphrase
// guards just this blob, never the whole history. key must be KeyLen bytes.
func WrapRecovery(key []byte, passphrase string) (string, error) {
	if len(key) != KeyLen {
		return "", fmt.Errorf("%w: got %d", ErrKeyLength, len(key))
	}
	if strings.TrimSpace(passphrase) == "" {
		return "", errors.New("recovery passphrase must not be empty")
	}
	salt := make([]byte, recoverySaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("generating recovery salt: %w", err)
	}
	p := defaultArgon2
	kek := deriveArgon2(passphrase, salt, p)
	aead, err := newGCM(kek)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating recovery nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, key, nil)
	data := append(append([]byte{}, nonce...), sealed...)

	q := url.Values{}
	q.Set("m", strconv.FormatUint(uint64(p.Memory), 10))
	q.Set("t", strconv.FormatUint(uint64(p.Time), 10))
	q.Set("p", strconv.FormatUint(uint64(p.Threads), 10))
	q.Set("s", base64.RawURLEncoding.EncodeToString(salt))
	q.Set("d", base64.RawURLEncoding.EncodeToString(data))
	return recoveryPrefix + q.Encode(), nil
}

// UnwrapRecovery reverses WrapRecovery, returning K_folder. A wrong passphrase
// or a tampered blob fails AEAD authentication and returns an error.
func UnwrapRecovery(blob, passphrase string) ([]byte, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(blob), recoveryPrefix)
	if !ok {
		return nil, ErrRecoveryFormat
	}
	q, err := url.ParseQuery(rest)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecoveryFormat, err)
	}
	p, err := parseArgon2Params(q)
	if err != nil {
		return nil, err
	}
	salt, err := base64.RawURLEncoding.DecodeString(q.Get("s"))
	if err != nil || len(salt) == 0 {
		return nil, fmt.Errorf("%w: bad salt", ErrRecoveryFormat)
	}
	data, err := base64.RawURLEncoding.DecodeString(q.Get("d"))
	if err != nil {
		return nil, fmt.Errorf("%w: bad payload", ErrRecoveryFormat)
	}
	kek := deriveArgon2(passphrase, salt, p)
	aead, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize() {
		return nil, fmt.Errorf("%w: payload too short", ErrRecoveryFormat)
	}
	nonce, sealed := data[:aead.NonceSize()], data[aead.NonceSize():]
	key, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("recovery: wrong passphrase or corrupt blob: %w", err)
	}
	if len(key) != KeyLen {
		return nil, fmt.Errorf("%w: unwrapped key was %d bytes", ErrKeyLength, len(key))
	}
	return key, nil
}

// ExportRecovery loads the current key, wraps it under passphrase, and writes
// the blob to RecoveryPath(home) (0600). It returns the path for the caller to
// report.
//
// TODO(step 3 follow-up): the full recovery story also stores this blob at the
// hub (keyed by a passphrase-derived handle) so a user with no surviving device
// can bootstrap sync from the passphrase alone. That server round-trip — and the
// passphrase->sync_id derivation it needs — is deliberately NOT built here; this
// command only exports the blob locally.
func ExportRecovery(home, passphrase string) (string, error) {
	key, ok, err := Load(home)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("no sync key configured; run `korai sync setup` first")
	}
	blob, err := WrapRecovery(key, passphrase)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".korai")
	if err := os.MkdirAll(dir, keyDirPerm); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	path := RecoveryPath(home)
	if err := os.WriteFile(path, []byte(blob+"\n"), recoveryFilePerm); err != nil {
		return "", fmt.Errorf("writing recovery blob: %w", err)
	}
	return path, nil
}

// parseArgon2Params reads the m/t/p cost parameters from a blob's query values.
func parseArgon2Params(q url.Values) (argon2Params, error) {
	mem, err1 := strconv.ParseUint(q.Get("m"), 10, 32)
	tm, err2 := strconv.ParseUint(q.Get("t"), 10, 32)
	th, err3 := strconv.ParseUint(q.Get("p"), 10, 8)
	if err1 != nil || err2 != nil || err3 != nil || mem == 0 || tm == 0 || th == 0 {
		return argon2Params{}, fmt.Errorf("%w: bad cost parameters", ErrRecoveryFormat)
	}
	return argon2Params{Time: uint32(tm), Memory: uint32(mem), Threads: uint8(th), KeyLen: defaultArgon2.KeyLen}, nil
}

// newGCM builds an AES-256-GCM AEAD from a 32-byte key.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initializing cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initializing gcm: %w", err)
	}
	return aead, nil
}

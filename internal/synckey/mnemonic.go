package synckey

import (
	"fmt"
	"strings"

	bip39 "github.com/tyler-smith/go-bip39"
)

// Mnemonic encodes K_folder as a BIP39 mnemonic. A 32-byte key yields 24 words
// (256 bits of entropy plus an 8-bit checksum). This is the primary,
// camera-free transport for adding a device: the user reads the words on the
// first device and types them on the second. key must be KeyLen bytes.
func Mnemonic(key []byte) (string, error) {
	if len(key) != KeyLen {
		return "", fmt.Errorf("%w: got %d", ErrKeyLength, len(key))
	}
	m, err := bip39.NewMnemonic(key)
	if err != nil {
		return "", fmt.Errorf("encoding mnemonic: %w", err)
	}
	return m, nil
}

// KeyFromMnemonic decodes a BIP39 mnemonic back into K_folder, reversing
// Mnemonic exactly. Surrounding and repeated whitespace is tolerated; a wrong
// word or a bad checksum returns an error, and the decoded entropy must be
// exactly KeyLen bytes (24 words) to be a valid content key.
func KeyFromMnemonic(mnemonic string) ([]byte, error) {
	normalized := strings.Join(strings.Fields(mnemonic), " ")
	if normalized == "" {
		return nil, fmt.Errorf("%w: empty mnemonic", ErrKeyLength)
	}
	key, err := bip39.EntropyFromMnemonic(normalized)
	if err != nil {
		return nil, fmt.Errorf("decoding mnemonic: %w", err)
	}
	if len(key) != KeyLen {
		return nil, fmt.Errorf("%w: mnemonic carried %d", ErrKeyLength, len(key))
	}
	return key, nil
}

package synckey

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	qrterminal "github.com/mdp/qrterminal/v3"
)

// uriPrefix is the scheme of the pairing URI encoded in the QR. A scanning
// device recognizes it and extracts K_folder from the k= parameter.
const uriPrefix = "korai-sync:v1?k="

// SyncURI returns the pairing URI carrying K_folder for a QR code:
// "korai-sync:v1?k=<base64url(K_folder)>". key must be KeyLen bytes.
func SyncURI(key []byte) (string, error) {
	if len(key) != KeyLen {
		return "", fmt.Errorf("%w: got %d", ErrKeyLength, len(key))
	}
	return uriPrefix + base64.RawURLEncoding.EncodeToString(key), nil
}

// KeyFromURI parses a pairing URI produced by SyncURI back into K_folder,
// accepting either raw or padded base64url for robustness against scanners.
func KeyFromURI(uri string) ([]byte, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(uri), uriPrefix)
	if !ok {
		return nil, fmt.Errorf("not a korai-sync pairing URI")
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding} {
		if key, err := enc.DecodeString(rest); err == nil && len(key) == KeyLen {
			return key, nil
		}
	}
	return nil, fmt.Errorf("%w: pairing URI did not carry a 32-byte key", ErrKeyLength)
}

// RenderQR writes a scannable QR of the current key's pairing URI to w using
// Unicode half-blocks, so a phone camera can adopt the namespace when the
// devices are co-present. key must be KeyLen bytes.
func RenderQR(w io.Writer, key []byte) error {
	uri, err := SyncURI(key)
	if err != nil {
		return err
	}
	qrterminal.GenerateWithConfig(uri, qrterminal.Config{
		Level:      qrterminal.M,
		Writer:     w,
		HalfBlocks: true,
		QuietZone:  2,
	})
	return nil
}

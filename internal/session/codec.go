package session

// Codec transforms a session entry's bytes on the way to and from disk so
// session files can be encrypted at rest without changing the store's format
// or its callers. It is the seam for future at-rest encryption: the header
// line is always written in the clear (it records which codec produced the
// file via Name), and every message line is passed through the codec.
//
// PlainCodec is the pass-through used today. A future encrypting codec (for
// example AES-GCM keyed from the OS keychain or the Korai SDK identity)
// implements the same interface; Save records its Name in the header so Load
// can select the matching codec. Encode must not emit a newline byte, since
// entries are newline-framed (JSONL) — an encrypting codec should base64 or
// otherwise escape its ciphertext.
type Codec interface {
	// Name is recorded in the session header. "none" means plaintext.
	Name() string
	// Encode maps a plaintext entry to its stored (possibly encrypted) form.
	Encode(plaintext []byte) ([]byte, error)
	// Decode reverses Encode.
	Decode(stored []byte) ([]byte, error)
}

// PlainCodec stores entries verbatim (no encryption). Its name is "none".
type PlainCodec struct{}

// Name implements Codec.
func (PlainCodec) Name() string { return "none" }

// Encode implements Codec; it returns plaintext unchanged.
func (PlainCodec) Encode(plaintext []byte) ([]byte, error) { return plaintext, nil }

// Decode implements Codec; it returns stored bytes unchanged.
func (PlainCodec) Decode(stored []byte) ([]byte, error) { return stored, nil }

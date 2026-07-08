package session

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewID returns a sortable, unique session id (timestamp + random suffix). It is
// the id stamped into a fresh korai.Session before the first save.
func NewID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

package synckey

import "golang.org/x/crypto/argon2"

// argon2Params are the Argon2id cost parameters used for both the
// passphrase-wrapped recovery KEK and the nuke verifier. They are stored
// alongside each derived value so the parameters can be raised later without
// invalidating existing blobs. Defaults follow current OWASP guidance
// (64 MiB, 3 passes, 4-way parallel).
type argon2Params struct {
	Time    uint32 // number of passes
	Memory  uint32 // KiB
	Threads uint8  // parallelism
	KeyLen  uint32 // output length in bytes
}

// defaultArgon2 is the parameter set applied to new recovery blobs and nuke
// verifiers.
var defaultArgon2 = argon2Params{Time: 3, Memory: 64 * 1024, Threads: 4, KeyLen: 32}

// deriveArgon2 stretches a passphrase into KeyLen bytes under the given salt and
// parameters using Argon2id.
func deriveArgon2(passphrase string, salt []byte, p argon2Params) []byte {
	return argon2.IDKey([]byte(passphrase), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}

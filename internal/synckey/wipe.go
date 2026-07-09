package synckey

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// WipePaths enumerates every on-disk trace the duress wipe destroys. It is
// explicit (rather than derived inside Wipe) so tests can point it at temp
// directories and so the exact purge surface is auditable in one place.
type WipePaths struct {
	KeyFile      string // ~/.korai/sync.key (crypto-shred target)
	RecoveryFile string // ~/.korai/sync-recovery.txt (crypto-shred target)
	SessionsDB   string // ~/.korai/sessions.db
	SessionsDir  string // ~/.korai/sessions (holds *.jsonl)
	SnapshotsDir string // ~/.korai/snapshots
	CursorFile   string // ~/.korai/sync-cursor
	MemoryFile   string // <project>/.korai/MEMORY.md
}

// DefaultWipePaths returns the standard purge surface for a given home and
// project directory.
func DefaultWipePaths(home, projectDir string) WipePaths {
	korai := filepath.Join(home, ".korai")
	wp := WipePaths{
		KeyFile:      KeyPath(home),
		RecoveryFile: RecoveryPath(home),
		SessionsDB:   filepath.Join(korai, "sessions.db"),
		SessionsDir:  filepath.Join(korai, "sessions"),
		SnapshotsDir: filepath.Join(korai, "snapshots"),
		CursorFile:   filepath.Join(korai, "sync-cursor"),
	}
	if projectDir != "" {
		wp.MemoryFile = filepath.Join(projectDir, ".korai", "MEMORY.md")
	}
	return wp
}

// WipeReport records what the wipe did, for logging and tests. Errors are
// collected rather than aborting, so a failure on one target never blocks the
// rest — crypto-shred alone already makes remote ciphertext unreadable.
type WipeReport struct {
	KeyShredded bool     // the in-memory key was zeroized and the key file removed
	Removed     []string // paths that existed and were deleted
	RemoteWiped bool     // the hub DELETE succeeded
	Errs        []error  // best-effort failures (each purge step continues on error)
}

// Wipe performs the duress "nuke": crypto-shred, then local purge, then a
// best-effort remote purge. The ordering matters — shredding K_folder first
// makes all remote ciphertext permanently unreadable even if the machine loses
// power before the later steps run.
//
//  1. Crypto-shred: zeroize the in-memory key and delete the key + recovery
//     files. This alone renders remote ciphertext undecryptable.
//  2. Local purge: delete the session store, snapshots, sync cursor, and the
//     project MEMORY.md (best-effort; missing files are not errors).
//  3. Remote purge: call remotePurge (the hub DELETE /v1/sync) if provided.
//
// It is idempotent and safe to re-run: every step tolerates already-absent
// targets. key may be nil (already shredded); remotePurge may be nil (offline or
// no hub configured).
func Wipe(ctx context.Context, key []byte, paths WipePaths, remotePurge func(context.Context) error) WipeReport {
	var r WipeReport

	// 1. Crypto-shred: zeroize the live key material, then remove key files.
	for i := range key {
		key[i] = 0
	}
	r.KeyShredded = true
	r.removeIfPresent(paths.KeyFile)
	r.removeIfPresent(paths.RecoveryFile)

	// 2. Local purge of every remaining trace.
	r.removeIfPresent(paths.SessionsDB)
	r.removeIfPresent(paths.SessionsDir)
	r.removeIfPresent(paths.SnapshotsDir)
	r.removeIfPresent(paths.CursorFile)
	r.removeIfPresent(paths.MemoryFile)

	// 3. Remote purge (best-effort): the crypto-shred already protects the data,
	// so a network failure here is logged, not fatal.
	if remotePurge != nil {
		if err := remotePurge(ctx); err != nil {
			r.Errs = append(r.Errs, fmt.Errorf("remote purge: %w", err))
		} else {
			r.RemoteWiped = true
		}
	}
	return r
}

// removeIfPresent deletes a path (recursively) if it exists, recording the
// deletion or any non-not-exist error. An empty path is skipped.
func (r *WipeReport) removeIfPresent(path string) {
	if path == "" {
		return
	}
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return
		}
		r.Errs = append(r.Errs, fmt.Errorf("stat %s: %w", path, err))
		return
	}
	if err := os.RemoveAll(path); err != nil {
		r.Errs = append(r.Errs, fmt.Errorf("removing %s: %w", path, err))
		return
	}
	r.Removed = append(r.Removed, path)
}

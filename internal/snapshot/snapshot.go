// Package snapshot provides checkpoint/undo for the agent's file changes using
// a "shadow" git repository that never touches the user's real .git directory.
//
// # Mechanism
//
// A shadow git repository lives under <dataDir>/<projecthash>/, where
// projecthash is a hex SHA-256 of the absolute worktree path truncated to 16
// characters. Every git invocation is run with explicit
// "--git-dir <shadowDir> --work-tree <worktree>" flags, so all snapshot
// metadata is stored away from the user's project and the user's real
// repository is never read or modified by this package.
//
// The shadow repository honours the worktree's own .gitignore files (and
// "git add -A" already skips the real .git directory), so ignored paths such as
// node_modules are naturally excluded from snapshots.
//
// A snapshot id is a git tree hash: "git add -A" followed by "git write-tree"
// produces a tree SHA with no commit object required.
//
// # Restore strategy
//
// Restore brings the worktree's tracked files back to exactly the set recorded
// in a snapshot tree, covering all three cases:
//
//   - modified files are reverted to their snapshot content,
//   - files deleted since the snapshot are recreated,
//   - files added since the snapshot are removed.
//
// The implementation is:
//
//  1. "git add -A" so the index reflects the current worktree.
//  2. "git diff --cached --diff-filter=A --name-only -z <snapshot>" lists the
//     files present now but absent from the snapshot tree (the additions). This
//     must be computed before the index is overwritten.
//  3. "git read-tree <snapshot>" loads the snapshot tree into the index.
//  4. "git checkout-index -a -f" writes every index entry to the worktree,
//     which reverts modifications and recreates deletions.
//  5. the additions from step 2 are deleted from the worktree.
//
// This restores precisely the snapshot tree's tracked set: files the snapshot
// never knew about (and that were not added since) are left untouched.
package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitError reports a failed shadow-git invocation. Its message is the lowercased
// stderr of the command, and it unwraps to the underlying exec error.
type gitError struct {
	cmd string
	msg string
	err error
}

// Error returns the lowercased failure message.
func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %s", e.cmd, e.msg)
}

// Unwrap returns the underlying exec error for errors.Is/As.
func (e *gitError) Unwrap() error {
	return e.err
}

// Manager captures and restores worktree snapshots in an isolated shadow git
// repository. The zero value is not usable; construct one with [New].
type Manager struct {
	worktree string
	gitDir   string
	enabled  bool
}

// New returns a Manager for worktree, storing shadow git data under dataDir. If
// git is not on PATH, or the worktree path is unusable, or the shadow
// repository cannot be initialised, the returned Manager is disabled and every
// method is a safe no-op so callers never need to branch on availability.
func New(worktree, dataDir string) *Manager {
	m := &Manager{}

	abs, err := filepath.Abs(worktree)
	if err != nil {
		return m
	}
	m.worktree = abs

	if _, err := exec.LookPath("git"); err != nil {
		return m
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return m
	}

	sum := sha256.Sum256([]byte(abs))
	m.gitDir = filepath.Join(dataDir, hex.EncodeToString(sum[:])[:16])

	if err := m.init(context.Background()); err != nil {
		m.gitDir = ""
		return m
	}
	m.enabled = true
	return m
}

// Enabled reports whether snapshots are operational, meaning git is present and
// the shadow repository has been initialised.
func (m *Manager) Enabled() bool {
	return m != nil && m.enabled
}

// init creates and configures the shadow repository if it does not already
// exist. It configures a local git identity so write-tree and any future commit
// do not fail on a machine without a global git identity, and disables autocrlf
// so file content round-trips byte-for-byte across platforms.
func (m *Manager) init(ctx context.Context) error {
	if err := os.MkdirAll(m.gitDir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(m.gitDir, "HEAD")); err == nil {
		return nil // already initialised
	}
	if _, err := m.git(ctx, "init"); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"user.name", "korai"},
		{"user.email", "korai@localhost"},
		{"core.autocrlf", "false"},
		{"core.longpaths", "true"},
		{"commit.gpgsign", "false"},
	} {
		if _, err := m.git(ctx, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot captures the current worktree state and returns an opaque id, which
// is the git tree hash of the staged worktree. It returns ("", nil) when the
// Manager is disabled.
func (m *Manager) Snapshot(ctx context.Context) (string, error) {
	if !m.Enabled() {
		return "", nil
	}
	if _, err := m.git(ctx, "add", "-A"); err != nil {
		return "", err
	}
	out, err := m.git(ctx, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Restore resets the worktree's tracked files to the given snapshot id: modified
// files are reverted, files added since the snapshot are removed, and files
// deleted since the snapshot are restored. It is a no-op when the Manager is
// disabled or id is empty.
func (m *Manager) Restore(ctx context.Context, id string) error {
	if !m.Enabled() || id == "" {
		return nil
	}

	// Stage the current worktree so the index reflects reality, then determine
	// which files were added relative to the snapshot before the index is
	// overwritten by read-tree.
	if _, err := m.git(ctx, "add", "-A"); err != nil {
		return err
	}
	added, err := m.git(ctx, "diff", "--cached", "--diff-filter=A", "--name-only", "-z", id)
	if err != nil {
		return err
	}

	if _, err := m.git(ctx, "read-tree", id); err != nil {
		return err
	}
	if _, err := m.git(ctx, "checkout-index", "-a", "-f"); err != nil {
		return err
	}

	for _, rel := range strings.Split(added, "\x00") {
		if rel == "" {
			continue
		}
		// Best-effort removal of files that exist now but not in the snapshot.
		_ = os.Remove(filepath.Join(m.worktree, filepath.FromSlash(rel)))
	}
	return nil
}

// Diff returns a unified diff of the current worktree against snapshot id. It
// returns an empty string when the worktrees are identical or the Manager is
// disabled.
func (m *Manager) Diff(ctx context.Context, id string) (string, error) {
	if !m.Enabled() || id == "" {
		return "", nil
	}
	if _, err := m.git(ctx, "add", "-A"); err != nil {
		return "", err
	}
	out, err := m.git(ctx, "diff", "--cached", "--no-ext-diff", id)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// git runs a git command against the shadow repository, with the worktree as
// the working directory, and returns raw stdout. On failure it returns an error
// wrapping the lowercased stderr output.
func (m *Manager) git(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"--git-dir", m.gitDir, "--work-tree", m.worktree}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = m.worktree

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", &gitError{cmd: strings.Join(args, " "), msg: strings.ToLower(msg), err: err}
	}
	return stdout.String(), nil
}

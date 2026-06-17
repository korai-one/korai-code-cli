// Package context assembles the system context prepended to every conversation:
// working directory, git status, project instructions file, and current date.
package context

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Build returns the system context string for workDir.
// Errors from optional components (git, instructions file) are silently omitted.
func Build(_ context.Context, workDir string) string {
	var b bytes.Buffer

	fmt.Fprintf(&b, "Working directory: %s\n", workDir)
	fmt.Fprintf(&b, "Date: %s\n", time.Now().Format("2006-01-02"))

	if status := gitStatus(workDir); status != "" {
		fmt.Fprintf(&b, "\nGit status:\n%s\n", status)
	}

	if instructions := projectInstructions(workDir); instructions != "" {
		fmt.Fprintf(&b, "\nProject instructions (AGENTS.md):\n%s\n", instructions)
	}

	return b.String()
}

func gitStatus(dir string) string {
	cmd := exec.Command("git", "-C", dir, "status", "--short")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(out))
}

func projectInstructions(dir string) string {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return string(data)
		}
	}
	return ""
}

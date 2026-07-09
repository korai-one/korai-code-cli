package auth

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// OpenBrowser launches the system browser at rawURL. It returns an error when no
// opener is available or the launch fails, so callers can fall back to the
// device flow or to printing the URL.
func OpenBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 avoids cmd.exe's "start" mangling of & in the query string.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launching browser: %w", err)
	}
	// Reap the child so it does not linger as a zombie; the browser itself has
	// already forked away from this short-lived launcher process.
	go func() { _ = cmd.Wait() }()
	return nil
}

// BrowserLikelyAvailable reports whether a graphical browser can probably be
// opened. It is a heuristic used to auto-select the device flow on headless
// hosts (SSH, containers): on Linux/BSD a missing DISPLAY and WAYLAND_DISPLAY
// means no GUI. Windows and macOS always have a default handler.
func BrowserLikelyAvailable() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	default:
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
}

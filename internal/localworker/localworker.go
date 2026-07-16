// Package localworker discovers a Korai worker running locally so the CLI can
// route inference straight to it — bypassing the orchestrator and the network —
// when one is available. A worker started in local mode advertises itself by
// writing Info to a well-known file (see Path); this package reads it and
// confirms the worker is actually reachable before the CLI commits to it.
//
// The worker's local endpoint is OpenAI-compatible (/v1/chat/completions,
// /v1/models, /health), the same surface the orchestrator exposes, so the
// existing KoraiClient talks to it unchanged — only the base URL differs.
package localworker

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/localproto"
)

// Info is the advertisement a local worker writes to Path on startup. It is the
// cross-repo contract between the worker (which writes it) and the CLI (which
// reads it); keep the JSON tags stable.
type Info struct {
	// URL is the worker's loopback base URL, e.g. http://127.0.0.1:54321.
	URL string `json:"url"`
	// Socket is the worker's Unix-domain socket path for the direct binary
	// channel (the local fast path). Empty on workers that only expose the
	// loopback OpenAI-HTTP endpoint. When set and reachable it is preferred.
	Socket string `json:"socket,omitempty"`
	// PID is the worker process id, for diagnostics only.
	PID int `json:"pid,omitempty"`
	// Models lists the canonical model ids the worker hosts.
	Models []string `json:"models,omitempty"`
	// Started is when the worker began listening (RFC 3339).
	Started time.Time `json:"started,omitempty"`
}

// Path returns the well-known advertisement file: ~/.korai/local-worker.json.
// An empty home directory yields an empty path (discovery then no-ops).
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".korai", "local-worker.json")
}

// healthTimeout bounds the reachability probe so discovery never stalls CLI
// startup when the advertised worker is gone or wedged.
const healthTimeout = time.Second

// Read loads the advertisement file without probing the worker. It returns
// ok=false when the file is absent or unreadable.
func Read() (Info, bool) {
	p := Path()
	if p == "" {
		return Info{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Info{}, false
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil || strings.TrimSpace(info.URL) == "" {
		return Info{}, false
	}
	return info, true
}

// Discover returns a reachable local worker, if one is advertised. It reads the
// advertisement file and probes the worker's /health endpoint; a stale file
// (worker exited, or its port now belongs to something else) fails the probe
// and yields ok=false, so the caller falls back to the network.
func Discover(ctx context.Context, client *http.Client) (Info, bool) {
	info, ok := Read()
	if !ok {
		return Info{}, false
	}
	if !healthy(ctx, client, info.URL) {
		return Info{}, false
	}
	return info, true
}

// healthy reports whether baseURL/health answers 200 with an ok status, the
// worker's liveness signal. Any transport error or non-ok body means no.
func healthy(ctx context.Context, client *http.Client, baseURL string) bool {
	if client == nil {
		client = &http.Client{Timeout: healthTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.Contains(string(body), `"ok"`)
}

// Endpoint is a resolved local-worker address. When Network is set the caller
// should use the direct binary channel (the local fast path): Network is "unix"
// for a co-located worker socket or "tcp" for a home/LAN inference server, and
// Address is the socket path or host:port. Otherwise it uses the loopback
// OpenAI-HTTP URL. Token authenticates the tcp channel.
type Endpoint struct {
	Network string
	Address string
	Token   string
	URL     string
}

// IsDirect reports whether the endpoint is the direct binary channel (rather
// than the HTTP URL).
func (e Endpoint) IsDirect() bool { return e.Network != "" }

// Resolve picks the local-worker endpoint to use, honoring precedence: an
// explicit TCP address (a LAN inference server) wins, then an explicit HTTP URL
// override — both used as-is without a probe, since the operator asked for them.
// Otherwise an advertised same-machine worker is used only if a probe passes,
// preferring the direct Unix socket over the HTTP URL. It returns ok=false when
// none applies, meaning the caller should use the networked Korai backend.
func Resolve(ctx context.Context, explicitURL, explicitAddr, token string, client *http.Client) (Endpoint, bool) {
	if a := strings.TrimSpace(explicitAddr); a != "" {
		return Endpoint{Network: "tcp", Address: a, Token: token}, true
	}
	if e := strings.TrimSpace(explicitURL); e != "" {
		return Endpoint{URL: strings.TrimRight(e, "/")}, true
	}
	info, ok := Read()
	if !ok {
		return Endpoint{}, false
	}
	// Prefer the direct socket when advertised and its handshake succeeds.
	if info.Socket != "" && socketHealthy(ctx, "unix", info.Socket, "") {
		return Endpoint{Network: "unix", Address: info.Socket}, true
	}
	if info.URL != "" && healthy(ctx, client, info.URL) {
		return Endpoint{URL: strings.TrimRight(info.URL, "/")}, true
	}
	return Endpoint{}, false
}

// socketHealthy reports whether a localproto worker is live at network/address
// by dialing it and completing the Hello/Ready handshake with a matching
// protocol version. token is presented in the Hello (for the tcp transport).
// Any dial/transport error or version mismatch means no.
func socketHealthy(ctx context.Context, network, address, token string) bool {
	dctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(dctx, network, address)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(healthTimeout))
	if err := localproto.WriteJSON(conn, localproto.FrameHello, localproto.HelloPayload{Version: localproto.ProtocolVersion, Token: token}); err != nil {
		return false
	}
	ft, body, err := localproto.ReadFrame(conn)
	if err != nil || ft != localproto.FrameReady {
		return false
	}
	var r localproto.ReadyPayload
	if localproto.Decode(body, &r) != nil {
		return false
	}
	return r.Version == localproto.ProtocolVersion
}

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
	// APIToken is the bearer required on the worker's HTTP endpoint (/v1/*).
	// Empty means the endpoint is open, which the worker only permits while it
	// is bound to loopback; it gates itself the moment it binds anywhere a
	// container or another machine could reach.
	//
	// Distinct from the token that authorises the direct binary channel and
	// POST /control/shutdown: this one only buys inference, so something
	// allowed to spend the GPU cannot also stop the worker.
	APIToken string `json:"apiToken,omitempty"`
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
	if !healthy(ctx, client, info.URL, info.APIToken) {
		return Info{}, false
	}
	return info, true
}

// healthy reports whether baseURL/health answers 200 with an ok status, the
// worker's liveness signal. Any transport error or non-ok body means no.
//
// token is sent when non-empty. /health is ungated on today's workers — it
// carries only liveness — but a probe that cannot authenticate would report a
// perfectly good gated worker as absent, and the caller reads that as "fall
// back to the network". Sending the bearer costs nothing and removes the
// failure mode.
func healthy(ctx context.Context, client *http.Client, baseURL, token string) bool {
	if client == nil {
		client = &http.Client{Timeout: healthTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/health", nil)
	if err != nil {
		return false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
// OpenAI-HTTP URL. Token authenticates EITHER transport: presented in the
// Hello frame on tcp, and as `Authorization: Bearer` on the HTTP URL. Empty
// means the endpoint is ungated.
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
//
// token authenticates BOTH transports. For an explicit endpoint it is the
// caller's (KORAI_LOCAL_WORKER_TOKEN); for a discovered one the advert's
// apiToken is used instead, since the worker chose that secret itself and the
// operator has nothing to pass. Leaving the HTTP endpoint tokenless was a
// silent 401: the worker gates /v1/* as soon as it binds off loopback, which
// is exactly what a sandboxed session needs it to do.
func Resolve(ctx context.Context, explicitURL, explicitAddr, token string, client *http.Client) (Endpoint, bool) {
	if a := strings.TrimSpace(explicitAddr); a != "" {
		return Endpoint{Network: "tcp", Address: a, Token: token}, true
	}
	if e := strings.TrimSpace(explicitURL); e != "" {
		// An operator-supplied URL may well point at a gated worker (that is
		// how a container reaches one), so carry their token through.
		return Endpoint{URL: strings.TrimRight(e, "/"), Token: token}, true
	}
	info, ok := Read()
	if !ok {
		return Endpoint{}, false
	}
	// HTTP first. The direct binary channel is a latency optimisation, and it
	// was preferred automatically — which made it the default path on every
	// machine that advertised a socket, including ones where it does not
	// work. Its handshake probe passed and the turn then hung with no
	// deadline, so the CLI looked frozen rather than failing over. An
	// optimisation that can hang must be asked for, not assumed.
	if DirectOptIn() && info.Socket != "" && socketHealthy(ctx, "unix", info.Socket, "") {
		return Endpoint{Network: "unix", Address: info.Socket}, true
	}
	if info.URL != "" && healthy(ctx, client, info.URL, info.APIToken) {
		return Endpoint{URL: strings.TrimRight(info.URL, "/"), Token: info.APIToken}, true
	}
	// No socket fallback when the HTTP probe fails. Falling back would put the
	// turn back on the channel that can hang with no deadline, which is what
	// made this look frozen in the first place; declining here means the
	// caller uses the network and says so. Read() already guarantees a
	// non-empty URL, so "socket-only worker" is not a reachable state.
	return Endpoint{}, false
}

// DirectOptIn reports whether the caller asked for the direct binary channel
// (KORAI_LOCAL_WORKER_DIRECT=1). Env rather than a flag: the local-worker
// surface already carries three flags, and this is a tuning knob, not
// something a user picks per run.
func DirectOptIn() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KORAI_LOCAL_WORKER_DIRECT"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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

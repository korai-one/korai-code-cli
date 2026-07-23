package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/command"
	"github.com/Nevaero/korai-code-cli/internal/compact"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/proto"
	"github.com/Nevaero/korai-code-cli/internal/session"
	"github.com/Nevaero/korai-code-cli/internal/wsasker"
	"github.com/Nevaero/korai-code-cli/internal/wsevent"
)

// shutdownGrace bounds how long Serve waits for in-flight connections to drain
// after the context is cancelled.
const shutdownGrace = 5 * time.Second

// listenLinePrefix tags the stdout line that announces the bound WebSocket URL,
// e.g. "KORAI_KODE_LISTEN=ws://127.0.0.1:54321/ws". A parent process matches
// this prefix, strips it, and loopback-checks the URL before connecting.
const listenLinePrefix = "KORAI_KODE_LISTEN="

// serveOptions holds the resolved flags for `korai serve`.
type serveOptions struct {
	port            int
	host            string
	dir             string
	autoYes         bool
	resume          bool
	local           bool
	localWorker     string
	localWorkerAddr string
	authToken       string
	allowedOrigins  []string
}

// defaultOriginPatterns is the base allow-list for the WebSocket Origin header.
// Requests with no Origin (server-to-server, e.g. a proxy hop or a websocat
// probe) are always accepted by coder/websocket; these patterns gate
// browser-originated connections (the Tauri webview and local dev). Deployments
// extend this with --allowed-origin (e.g. the dashboard's domain when a browser
// connects straight to a sandbox).
var defaultOriginPatterns = []string{
	"localhost", "localhost:*",
	"127.0.0.1", "127.0.0.1:*",
	"tauri.localhost",
	"korai.one", "*.korai.one",
}

// serveCmd is the `korai serve` subcommand: it runs the engine behind a
// WebSocket endpoint so a thin client (desktop webview, browser, mobile) drives
// the same Go engine the CLI uses, with no client-side reimplementation.
func serveCmd() *cobra.Command {
	var (
		port            int
		host            string
		dir             string
		autoYes         bool
		resume          bool
		debug           bool
		local           bool
		localWorker     string
		localWorkerAddr string
		authToken       string
		allowedOrigins  []string
	)
	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "Serve the engine over WebSocket for web and desktop clients",
		Long:          "Run the Korai engine behind a WebSocket endpoint (GET /ws).\n\nEach connection is one session driven by the JSON wire protocol in internal/proto.\nThe bound address is printed to stdout on startup so a parent process (the Tauri\nsidecar) can read the chosen port.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogging(debug, os.Stderr)
			return runServe(cmd.Context(), serveOptions{
				port: port, host: host, dir: dir, autoYes: autoYes, resume: resume,
				local: local, localWorker: localWorker, localWorkerAddr: localWorkerAddr,
				authToken: authToken, allowedOrigins: allowedOrigins,
			})
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "port to listen on (0 picks a free port)")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1",
		"bind address; use 0.0.0.0 inside an isolated sandbox so an exposed port is reachable")
	cmd.Flags().StringVar(&dir, "dir", "", "working directory for the session (default: current directory)")
	cmd.Flags().BoolVar(&local, "local", false,
		"require a local/LAN worker for inference and run without any API key")
	cmd.Flags().BoolVar(&autoYes, "yes", false, "auto-approve every tool call instead of asking the client")
	cmd.Flags().BoolVar(&resume, "resume", false,
		"resume the most recent saved session for --dir (used to continue an expired sandbox)")
	cmd.Flags().BoolVar(&debug, "debug", false, "enable debug logging to stderr")
	cmd.Flags().StringVar(&localWorker, "local-worker-url", "",
		"route inference to a local Korai worker at this URL (default: auto-detect, else use the network)")
	cmd.Flags().StringVar(&localWorkerAddr, "local-worker-addr", "",
		"route inference to a home/LAN inference server over the direct binary channel (host:port; token via KORAI_LOCAL_WORKER_TOKEN)")
	cmd.Flags().StringVar(&authToken, "auth-token", "",
		"require this token as the ?token= query parameter on the WebSocket upgrade (gates browser→sandbox connections)")
	cmd.Flags().StringArrayVar(&allowedOrigins, "allowed-origin", nil,
		"additional allowed WebSocket Origin host pattern (e.g. korai.one, *.vercel.app); repeatable")
	return cmd
}

// runServe assembles one shared session and serves it over WebSocket until the
// context is cancelled (SIGINT/SIGTERM). The session's read-mostly parts
// (client, tool registry, system prompt, hooks, compactor) are shared across
// connections; each connection gets its own permission asker, engine, and
// history. serve targets one active session per process — the Tauri sidecar and
// the per-session VM both run a dedicated `korai serve` — so the shared
// ModeSelector is acceptable.
func runServe(ctx context.Context, opts serveOptions) error {
	if opts.dir != "" {
		if err := os.Chdir(opts.dir); err != nil {
			return fmt.Errorf("chdir %q: %w", opts.dir, err)
		}
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// opts.resume → continue the most recent saved session for this working dir.
	// The session store persists per turn (see worker's saver call) and lives on
	// the sandbox filesystem, which the Vercel snapshot captures, so restoring a
	// snapshot and starting with --resume continues the prior conversation.
	sess, err := assemble(ctx, runOptions{
		autoYes:         opts.autoYes,
		cont:            opts.resume,
		local:           opts.local,
		localWorkerURL:  opts.localWorker,
		localWorkerAddr: opts.localWorkerAddr,
	}, headlessPlanApprover{autoYes: opts.autoYes})
	if err != nil {
		return err
	}
	defer sess.close()

	host := opts.host
	if host == "" {
		host = "127.0.0.1"
	}
	srv := &server{
		sess:           sess,
		autoYes:        opts.autoYes,
		authToken:      opts.authToken,
		originPatterns: append(append([]string{}, defaultOriginPatterns...), opts.allowedOrigins...),
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, opts.port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	// Announce the bound WebSocket URL on stdout so a parent process (the Tauri
	// sidecar) can discover the port chosen by --port 0. The line is a stable,
	// machine-parseable, loopback-only prefix mirroring the worker's
	// KORAI_LOCAL_LISTEN convention: the parent strips the prefix and verifies
	// the 127.0.0.1 host before trusting it. Keep this the first stdout line.
	fmt.Printf("%s%s\n", listenLinePrefix, "ws://"+ln.Addr().String()+"/ws")
	slog.Info("serve listening", "addr", ln.Addr().String())

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.handleWS)
	httpSrv := &http.Server{
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		// Derive from ctx (so request-scoped values carry through) but strip its
		// cancellation — ctx is already done here, and Shutdown needs a live
		// context bounded only by the grace period.
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// server holds the shared session and serves WebSocket connections.
type server struct {
	sess    *assembled
	autoYes bool
	// authToken, when non-empty, must be presented as ?token= on the WebSocket
	// upgrade. It gates a browser connecting straight to a sandbox over a public
	// URL (the sandbox port is reachable by anyone who learns the URL; the token
	// is the credential). Empty disables the check (loopback/Tauri/local).
	authToken string
	// originPatterns is the Origin allow-list for the upgrade (defaults plus any
	// --allowed-origin values).
	originPatterns []string
}

// handleWS upgrades one connection and runs a session over it. The read loop and
// the turn worker run on separate goroutines on purpose: a tool that needs
// permission blocks the worker inside WSAsker.Ask until the client answers, and
// only the read loop — still reading — can deliver that perm_res. Mixing them
// would deadlock the first permission prompt.
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Gate the upgrade on the shared token before doing any protocol work, when
	// configured. Constant-time compare so a wrong token leaks no timing signal.
	if s.authToken != "" {
		got := r.URL.Query().Get("token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.authToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.originPatterns})
	if err != nil {
		slog.Warn("websocket accept failed", "error", err)
		return
	}
	defer func() { _ = c.CloseNow() }()

	connCtx, cancelConn := context.WithCancel(r.Context())
	defer cancelConn()

	// All writes go through send, serialized: coder/websocket allows only one
	// concurrent writer, and both the worker (via the bridge) and the read loop
	// (error replies) write.
	var writeMu sync.Mutex
	send := func(ev proto.ServerEvent) error {
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		return c.Write(connCtx, websocket.MessageText, data)
	}

	// Permission resolution: WSAsker round-trips to the client, unless --yes
	// auto-approves everything.
	var (
		askerWS *wsasker.Asker
		asker   perm.Asker = perm.AllowAsker{}
	)
	if !s.autoYes {
		askerWS = wsasker.New(send)
		asker = askerWS
	}
	permEngine := perm.NewEngine(s.sess.modes, s.sess.rules, asker)
	eng := engine.New(s.sess.client, s.sess.registry, permEngine, s.sess.deps,
		engine.WithHooks(s.sess.hooks), engine.WithModelSelector(s.sess.models),
		engine.WithUsageRecorder(s.sess.cost.Add), engine.WithSystemSuffix(planSuffix(s.sess.modes)),
		engine.WithSystemSection(s.sess.memSection),
		engine.WithToolResultFilter(s.sess.condense),
		engine.WithAutoCompact(compact.DefaultThreshold, compact.EstimateTokens, s.sess.compactor))

	// cancelTurn cancels the in-flight turn for abort; guarded because the read
	// loop (abort) and the worker (set/clear) both touch it.
	var turnMu sync.Mutex
	var cancelTurn context.CancelFunc
	setCancel := func(f context.CancelFunc) {
		turnMu.Lock()
		cancelTurn = f
		turnMu.Unlock()
	}
	abort := func() {
		turnMu.Lock()
		if cancelTurn != nil {
			cancelTurn()
		}
		turnMu.Unlock()
	}

	// actions carries user messages and slash commands to the worker so every
	// history mutation happens in one goroutine. Capacity 1 with a non-blocking
	// send means a message arriving mid-turn is rejected as busy rather than
	// blocking the read loop (which must keep delivering perm_res).
	actions := make(chan proto.ClientMsg, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.worker(connCtx, eng, send, setCancel, actions)
	}()

	for {
		_, data, err := c.Read(connCtx)
		if err != nil {
			break
		}
		var msg proto.ClientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = send(proto.Error("invalid message: " + err.Error()))
			continue
		}
		switch msg.Type {
		case proto.TypePermRes:
			if askerWS != nil {
				askerWS.Resolve(msg.ID, msg.Approved)
			}
		case proto.TypeAbort:
			abort()
		case proto.TypeMessage, proto.TypeSlash:
			select {
			case actions <- msg:
			default:
				_ = send(proto.Error("busy: a turn is already running"))
			}
		default:
			_ = send(proto.Error("unknown message type: " + msg.Type))
		}
	}

	cancelConn()   // unblock an in-flight turn and any pending permission ask
	close(actions) // let the worker drain and exit
	wg.Wait()
}

// worker owns the conversation history and runs turns one at a time. It reads
// actions until the channel closes, running each user message as a turn and
// each slash command through the command registry.
func (s *server) worker(
	ctx context.Context,
	eng *engine.Engine,
	send func(proto.ServerEvent) error,
	setCancel func(context.CancelFunc),
	actions <-chan proto.ClientMsg,
) {
	history := append([]apiclient.Message{}, s.sess.initialHistory...)
	// The active session id/start are mutable: /resume swaps to a saved session
	// and /clear starts a new one. The client binds its conversation to this id.
	sessionID := s.sess.sessionID
	sessionStart := s.sess.sessionStart

	// Announce the active session id so the client can reconcile its
	// conversation with the engine's session.
	_ = send(proto.Sess(sessionID))

	// runTurn appends text as a user message, runs the engine, bridges its
	// events to the client, and returns the post-turn history. ok is false when
	// a send failed (the connection is broken) so the worker can stop.
	runTurn := func(text string) (ok bool) {
		history = append(history, userMessage(text))
		turnCtx, cancel := context.WithCancel(ctx)
		setCancel(cancel)
		newHistory, err := wsevent.Bridge(eng.Run(turnCtx, history, s.sess.system), send)
		cancel()
		setCancel(nil)
		if newHistory != nil {
			history = newHistory
			s.sess.saver(sessionID, sessionStart, history)
		}
		return err == nil
	}

	// Periodic active-session checkpoint: re-persist the open conversation on a
	// cadence so the background syncer pushes it mid-session (a peer can pick it
	// up before the turn that ends it). The save runs in this single history-
	// owning goroutine, so it needs no lock; it swallows its own errors. A zero
	// interval (sync off) leaves tickC nil, and a nil channel never fires.
	var tickC <-chan time.Time
	if s.sess.activeSyncInterval > 0 {
		t := time.NewTicker(s.sess.activeSyncInterval)
		defer t.Stop()
		tickC = t.C
	}

	for {
		select {
		case act, ok := <-actions:
			if !ok {
				return
			}
			switch act.Type {
			case proto.TypeMessage:
				if !runTurn(act.Text) {
					return
				}
			case proto.TypeSlash:
				submit, ok := s.runSlash(ctx, act.Cmd, act.Text, send, &history, &sessionID, &sessionStart)
				if !ok {
					continue
				}
				if submit != "" && !runTurn(submit) {
					return
				}
			}
		case <-tickC:
			if len(history) > 0 {
				s.sess.saver(sessionID, sessionStart, history)
			}
		}
	}
}

// runSlash runs a slash command and acts on its Result. It reports the text to
// submit as a turn (non-empty only for SubmitPrompt) and whether to proceed.
// History-mutating actions (Clear, CompactHistory, ResumeSession) write through
// historyPtr — and the session pointers for Clear/ResumeSession — so the
// worker's single copy stays authoritative. Quit is informational in serve mode.
func (s *server) runSlash(
	ctx context.Context,
	name, args string,
	send func(proto.ServerEvent) error,
	historyPtr *[]apiclient.Message,
	sessionIDPtr *string,
	sessionStartPtr *time.Time,
) (submit string, ok bool) {
	cmd, found := s.sess.commands.Get(name)
	if !found {
		_ = send(proto.Error("unknown command: /" + name))
		_ = send(proto.Done())
		return "", false
	}
	res, err := cmd.Run(args)
	if err != nil {
		_ = send(proto.Error(err.Error()))
		_ = send(proto.Done())
		return "", false
	}

	switch res.Action {
	case command.SubmitPrompt:
		return res.Text, true
	case command.Clear:
		// Start a NEW engine session so "new conversation" on the client maps to
		// a distinct session id, and announce it.
		*historyPtr = []apiclient.Message{}
		*sessionIDPtr = session.NewID()
		*sessionStartPtr = time.Now()
		_ = send(proto.Sess(*sessionIDPtr))
		_ = send(proto.Text("(conversation cleared)"))
		_ = send(proto.Done())
		return "", false
	case command.ResumeSession:
		// Bind this connection to the session id the client asked for: load its
		// history if this sandbox's store has it, otherwise bind to the id with
		// empty history. Binding (rather than erroring) is what reconciles a
		// client-originated conversation id with the engine session — subsequent
		// saves write back under that id, so the two stay in lockstep from here.
		id := strings.TrimSpace(res.Text)
		if id == "" {
			_ = send(proto.Error("resume requires a session id"))
			_ = send(proto.Done())
			return "", false
		}
		msgs, created, lerr := s.sess.resumeLoad(id)
		if lerr != nil {
			msgs = nil
			created = time.Now()
		}
		*historyPtr = msgs
		*sessionIDPtr = id
		*sessionStartPtr = created
		_ = send(proto.Sess(id))
		_ = send(proto.Done())
		return "", false
	case command.CompactHistory:
		compacted, cerr := s.sess.compactor(ctx, *historyPtr)
		if cerr != nil {
			_ = send(proto.Error("compaction failed: " + cerr.Error()))
			_ = send(proto.Done())
			return "", false
		}
		before := len(*historyPtr)
		*historyPtr = compacted
		_ = send(proto.Compact(before, len(compacted)))
		_ = send(proto.Done())
		return "", false
	default: // ShowText, Quit — informational in serve mode
		if res.Text != "" {
			_ = send(proto.Text(res.Text))
		}
		_ = send(proto.Done())
		return "", false
	}
}

// userMessage wraps raw text as a user-role message.
func userMessage(text string) apiclient.Message {
	return apiclient.Message{
		Role:    apiclient.RoleUser,
		Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: text}},
	}
}

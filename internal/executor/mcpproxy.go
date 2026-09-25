package executor

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/mcp"
)

// MCP servers for a placed task, executed HERE.
//
// A local task gets the taskyou server as a stdio child of its Claude. A placed
// task cannot: a stdio server on the host would open the host's task store, and
// this task is not in it. The server has to run where the task store is — on
// this machine — and the only way to reach this machine from a host is the
// connection ty already holds open to it (hostchannel.go). Nothing on this side
// listens and no host can dial in; the coordinator usually is not reachable from
// the fleet anyway.
//
// So the host gets a stub. It is a small POSIX script registered with Claude via
// --mcp-config, and it:
//
//   - answers initialize and tools/list itself, from a copy staged at launch, so
//     Claude keeps the server (and its tools) even when this machine is asleep;
//   - spools every other request as a file, staged and renamed like .ty/signal;
//   - waits for the host agent to carry it up the ssh stdout, for ty to run it,
//     and for the answer to come back down the same connection's stdin;
//   - and fails FAST, with a plain MCP error, when the channel is known down —
//     the host agent records when it last heard from ty, and a stale stamp means
//     nobody is there. The agent reads the error, notes it, and keeps working.
//
// The wire format on the host channel, beyond what hostchannel.go already says:
//
//	host → ty   M <name> <base64 request line>
//	ty → host   P                              (I am here; sent every few seconds)
//	ty → host   A <name>                       (request received)
//	ty → host   R <name> <base64 response line>
//
// <name> is <task>.<run>.<server>.<pid>.<n>: the task and run come from the
// launch environment and are checked against this machine's record of the run
// before anything executes, so a stub can only act as the task it was launched
// for (the same scope `ty mcp-server --task-id N` has locally).

const (
	// mcpRelayPingInterval is how often ty tells a host it is still there.
	mcpRelayPingInterval = 3 * time.Second

	// mcpRelayStale is how old that word may be before a stub calls this machine
	// offline. Three missed pings: long enough to ride out a slow tick, short
	// enough that a call made while the coordinator sleeps fails in seconds, not
	// minutes.
	mcpRelayStale = 10 * time.Second

	// mcpRelayAckWait bounds how long a stub waits for its request to be received.
	// A live channel picks a request up within a fraction of a second.
	mcpRelayAckWait = 10 * time.Second

	// mcpRelayCallLimit bounds one relayed call once it is received. It is long
	// because taskyou_complete may run a verify gate; the liveness check, not this,
	// is what keeps a call from hanging when this machine goes away mid-call.
	mcpRelayCallLimit = 15 * time.Minute

	// mcpRelaySessionIdle is how long a Mac-side server started for a placed task
	// may sit unused before it is stopped. Nothing else reaps it.
	mcpRelaySessionIdle = 10 * time.Minute

	// taskyouServerName is the proxied taskyou server, named as it is locally so
	// the tools keep their usual names (mcp__taskyou__taskyou_complete).
	taskyouServerName = "taskyou"

	// mcpProxyScriptPath is the stub, inside the task's worktree like .ty/signal.
	mcpProxyScriptPath = ".ty/mcp-proxy"
	// mcpProxyCacheDir holds what the stub answers from on its own.
	mcpProxyCacheDir = ".ty/mcp"

	// SettingRemoteMCPProxy lists, comma-separated, the MCP servers that only work
	// on this machine (claude-in-chrome drives the Chrome running HERE) and that a
	// placed task should get anyway, executed here through the relay.
	SettingRemoteMCPProxy = "remote_mcp_proxy"
)

// mcpServerNameRe is what a relayed server may be called. No dots, so a relay
// request name splits unambiguously.
var mcpServerNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// relayRef is a parsed relay request name.
type relayRef struct {
	TaskID int64
	RunID  string
	Server string
}

func (r relayRef) key() string { return fmt.Sprintf("%d.%s.%s", r.TaskID, r.RunID, r.Server) }

// validRelayName is what may appear as a relay request's name on the wire and
// as a file name in the host's spool: the characters a name is built from, and
// nothing a shell or a path could read differently.
func validRelayName(name string) bool {
	if name == "" || len(name) > 200 || strings.HasPrefix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	for _, c := range name {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// parseRelayName reads <task>.<run>.<server>.<pid>.<n>.
func parseRelayName(name string) (relayRef, bool) {
	parts := strings.Split(name, ".")
	if len(parts) != 5 || !validRelayName(name) {
		return relayRef{}, false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return relayRef{}, false
	}
	if parts[1] == "" || strings.Trim(parts[1], "0123456789abcdef") != "" || !mcpServerNameRe.MatchString(parts[2]) {
		return relayRef{}, false
	}
	for _, p := range parts[3:] {
		if _, err := strconv.ParseUint(p, 10, 64); err != nil {
			return relayRef{}, false
		}
	}
	return relayRef{TaskID: id, RunID: parts[1], Server: parts[2]}, true
}

// rpcEnvelope is enough of a JSON-RPC message to route it.
type rpcEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// rpcIDOf returns a message's id as raw JSON, or "null" when it has none.
func rpcIDOf(line []byte) json.RawMessage {
	var env rpcEnvelope
	if json.Unmarshal(line, &env) != nil || len(env.ID) == 0 {
		return json.RawMessage("null")
	}
	return env.ID
}

// rpcErrorLine is a JSON-RPC error reply.
func rpcErrorLine(id json.RawMessage, code int, message string) []byte {
	msg, _ := json.Marshal(message)
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%s}}`, id, code, msg))
}

// mcpServerDef is how to start a Mac-only stdio MCP server on this machine.
type mcpServerDef struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// builtinMCPServers are servers ty knows how to start without being told.
// claude-in-chrome is built into the claude binary and is not listed in any
// config file, so a user naming it in remote_mcp_proxy would otherwise get
// nothing.
var builtinMCPServers = map[string]mcpServerDef{
	"claude-in-chrome": {Name: "claude-in-chrome", Command: "claude", Args: []string{"--claude-in-chrome-mcp"}},
}

// proxiedServerDefs resolves the remote_mcp_proxy setting into server
// definitions, reading stdio servers the user registered with Claude on this
// machine (~/.claude.json) for any name ty has no built-in for. A name it cannot
// resolve is reported, not guessed at.
func proxiedServerDefs(database *db.DB) ([]mcpServerDef, []string) {
	if database == nil {
		return nil, nil
	}
	raw, _ := database.GetSetting(SettingRemoteMCPProxy)
	var names []string
	for _, n := range strings.Split(raw, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	return resolveMCPServerDefs(names)
}

// resolveMCPServerDefs finds how to start each named server here.
func resolveMCPServerDefs(names []string) ([]mcpServerDef, []string) {

	var userServers map[string]struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if data, err := os.ReadFile(ClaudeConfigFilePath(DefaultClaudeConfigDir())); err == nil {
		var cfg struct {
			MCPServers json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(data, &cfg) == nil && len(cfg.MCPServers) > 0 {
			_ = json.Unmarshal(cfg.MCPServers, &userServers)
		}
	}

	var defs []mcpServerDef
	var problems []string
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		switch {
		case !mcpServerNameRe.MatchString(name) || name == taskyouServerName:
			problems = append(problems, fmt.Sprintf("%q is not a server name ty can relay", name))
		case builtinMCPServers[name].Command != "":
			defs = append(defs, builtinMCPServers[name])
		default:
			s, ok := userServers[name]
			if !ok || s.Command == "" || (s.Type != "" && s.Type != "stdio") {
				problems = append(problems, fmt.Sprintf("%s: not a stdio MCP server in %s", name, ClaudeConfigFilePath(DefaultClaudeConfigDir())))
				continue
			}
			defs = append(defs, mcpServerDef{Name: name, Command: s.Command, Args: s.Args, Env: s.Env})
		}
	}
	return defs, problems
}

// ValidateRemoteMCPProxy checks a remote_mcp_proxy value before it is saved:
// every name must be one ty can relay, and one it knows how to start here.
func ValidateRemoteMCPProxy(value string) error {
	var names []string
	for _, n := range strings.Split(value, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	for _, n := range names {
		if !mcpServerNameRe.MatchString(n) || n == taskyouServerName {
			return fmt.Errorf("%q is not a server name ty can relay", n)
		}
	}
	if len(names) == 0 {
		return nil
	}
	_, problems := resolveMCPServerDefs(names)
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// mcpRelay executes relayed MCP requests on this machine.
type mcpRelay struct {
	db *db.DB

	mu       sync.Mutex
	taskyou  map[string]*relayedTaskyou
	sessions map[string]*stdioSession
	listings map[string]serverListing
	closed   bool
	reaper   bool

	// defs resolves a server name to how to start it. Tests replace it.
	defs func() []mcpServerDef
}

type relayedTaskyou struct {
	srv      *mcp.Server
	lastUsed time.Time
}

// serverListing is what the stub answers from while this machine is away.
type serverListing struct {
	Init  json.RawMessage
	Tools json.RawMessage
	At    time.Time
}

func newMCPRelay(database *db.DB) *mcpRelay {
	r := &mcpRelay{
		db:       database,
		taskyou:  map[string]*relayedTaskyou{},
		sessions: map[string]*stdioSession{},
		listings: map[string]serverListing{},
	}
	r.defs = func() []mcpServerDef {
		defs, _ := proxiedServerDefs(r.db)
		return defs
	}
	return r
}

// handle answers one relayed request from host. It always returns a reply: the
// stub on the far side is waiting for one, and silence would hold it until its
// call limit.
func (r *mcpRelay) handle(host, name string, payload []byte) []byte {
	id := rpcIDOf(payload)
	ref, ok := parseRelayName(name)
	if !ok {
		return rpcErrorLine(id, -32600, "malformed relay request")
	}
	if r.db == nil {
		return rpcErrorLine(id, -32000, ref.Server+" unavailable: nothing is serving relayed calls on this machine")
	}
	current, err := r.db.IsCurrentRemoteRun(ref.TaskID, ref.RunID, host)
	if err != nil {
		return rpcErrorLine(id, -32000, fmt.Sprintf("%s unavailable: %v", ref.Server, err))
	}
	if !current {
		return rpcErrorLine(id, -32000, fmt.Sprintf(
			"%s refused: this session is not the current run of task #%d on %s (the task was retried or moved); stop and let the session end",
			ref.Server, ref.TaskID, host))
	}

	var out []byte
	if ref.Server == taskyouServerName {
		out = r.taskyouFor(ref).Handle(payload)
	} else {
		out = r.forward(ref, payload)
	}
	if len(out) == 0 {
		out = []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{}}`, id))
	}
	return out
}

// taskyouFor returns the in-process taskyou server for a placed run, scoped to
// that task exactly as `ty mcp-server --task-id N` is.
func (r *mcpRelay) taskyouFor(ref relayRef) *mcp.Server {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := ref.key()
	t, ok := r.taskyou[k]
	if !ok {
		t = &relayedTaskyou{srv: mcp.NewServer(r.db, ref.TaskID)}
		r.taskyou[k] = t
		r.startReaperLocked()
	}
	t.lastUsed = time.Now()
	return t.srv
}

// forward sends a request to the Mac-only server this run is using, starting
// one on first use.
func (r *mcpRelay) forward(ref relayRef, payload []byte) []byte {
	id := rpcIDOf(payload)
	def, ok := r.def(ref.Server)
	if !ok {
		return rpcErrorLine(id, -32000, ref.Server+" unavailable: it is no longer relayed from this machine (see ty settings remote_mcp_proxy)")
	}
	s, err := r.session(ref, def)
	if err != nil {
		return rpcErrorLine(id, -32000, fmt.Sprintf("%s unavailable: could not start it on this machine: %v", ref.Server, err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpRelayCallLimit-time.Minute)
	defer cancel()
	out, err := s.call(ctx, payload)
	if err != nil {
		r.drop(ref.key(), s)
		return rpcErrorLine(id, -32000, fmt.Sprintf("%s failed on this machine: %v", ref.Server, err))
	}
	return out
}

func (r *mcpRelay) def(name string) (mcpServerDef, bool) {
	for _, d := range r.defs() {
		if d.Name == name {
			return d, true
		}
	}
	return mcpServerDef{}, false
}

// session returns the running server for a run, starting it if needed.
func (r *mcpRelay) session(ref relayRef, def mcpServerDef) (*stdioSession, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("ty is shutting down")
	}
	if s, ok := r.sessions[ref.key()]; ok && s.alive() {
		s.touch()
		r.mu.Unlock()
		return s, nil
	}
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, _, err := startStdioSession(ctx, def)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		s.close()
		return nil, fmt.Errorf("ty is shutting down")
	}
	if prev, ok := r.sessions[ref.key()]; ok && prev.alive() {
		// Another call for the same run started one first; keep that one.
		s.close()
		prev.touch()
		return prev, nil
	}
	r.sessions[ref.key()] = s
	r.startReaperLocked()
	return s, nil
}

func (r *mcpRelay) drop(key string, s *stdioSession) {
	r.mu.Lock()
	if r.sessions[key] == s {
		delete(r.sessions, key)
	}
	r.mu.Unlock()
	s.close()
}

// startReaperLocked starts the idle sweep once. The caller holds r.mu.
func (r *mcpRelay) startReaperLocked() {
	if r.reaper {
		return
	}
	r.reaper = true
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if r.reap(time.Now()) {
				return
			}
		}
	}()
}

// reap stops Mac-side servers nobody has used for a while and forgets idle
// taskyou servers. It reports whether the relay has been closed.
func (r *mcpRelay) reap(now time.Time) bool {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return true
	}
	var idle []*stdioSession
	for k, s := range r.sessions {
		if !s.alive() || now.Sub(s.used()) > mcpRelaySessionIdle {
			idle = append(idle, s)
			delete(r.sessions, k)
		}
	}
	for k, t := range r.taskyou {
		if now.Sub(t.lastUsed) > time.Hour {
			delete(r.taskyou, k)
		}
	}
	r.mu.Unlock()
	for _, s := range idle {
		s.close()
	}
	return false
}

// Close stops every server the relay started.
func (r *mcpRelay) Close() {
	r.mu.Lock()
	r.closed = true
	sessions := r.sessions
	r.sessions = map[string]*stdioSession{}
	r.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
}

// listing returns a server's handshake and tool list for staging on a host,
// asking the server itself when there is no recent copy.
func (r *mcpRelay) listing(ctx context.Context, def mcpServerDef) (serverListing, error) {
	r.mu.Lock()
	cached, ok := r.listings[def.Name]
	r.mu.Unlock()
	if ok && time.Since(cached.At) < time.Hour {
		return cached, nil
	}

	s, initResult, err := startStdioSession(ctx, def)
	if err != nil {
		return serverListing{}, err
	}
	defer s.close()
	out, err := s.call(ctx, []byte(`{"jsonrpc":"2.0","id":"ty-list","method":"tools/list","params":{}}`))
	if err != nil {
		return serverListing{}, err
	}
	var env rpcEnvelope
	if err := json.Unmarshal(out, &env); err != nil || len(env.Result) == 0 {
		return serverListing{}, fmt.Errorf("tools/list gave no result: %s", strings.TrimSpace(string(out)))
	}
	l := serverListing{Init: relayInitResult(initResult), Tools: env.Result, At: time.Now()}
	r.mu.Lock()
	r.listings[def.Name] = l
	r.mu.Unlock()
	return l, nil
}

// relayInitResult is the handshake the stub replays. Only tools are relayed —
// notifications cannot travel up a one-way stub — so the capabilities say
// exactly that, whatever the real server offers.
func relayInitResult(real json.RawMessage) json.RawMessage {
	var in map[string]json.RawMessage
	if json.Unmarshal(real, &in) != nil {
		in = map[string]json.RawMessage{}
	}
	out := map[string]json.RawMessage{"capabilities": json.RawMessage(`{"tools":{}}`)}
	for _, k := range []string{"protocolVersion", "serverInfo", "instructions"} {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	if _, ok := out["protocolVersion"]; !ok {
		out["protocolVersion"] = json.RawMessage(`"2024-11-05"`)
	}
	data, _ := json.Marshal(out)
	return data
}

// stdioSession is one Mac-side MCP server process ty is the client of.
type stdioSession struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu       sync.Mutex
	pending  map[string]chan []byte
	lastUsed time.Time
	done     chan struct{}
	closeErr error
}

// startStdioSession starts a server and completes the MCP handshake with it,
// returning its initialize result.
func startStdioSession(ctx context.Context, def mcpServerDef) (*stdioSession, json.RawMessage, error) {
	// Not CommandContext: the session outlives the call that started it.
	cmd := exec.Command(def.Command, def.Args...)
	cmd.Env = os.Environ()
	for k, v := range def.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	s := &stdioSession{cmd: cmd, stdin: stdin, pending: map[string]chan []byte{}, lastUsed: time.Now(), done: make(chan struct{})}
	go s.read(stdout)
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.closeErr = err
		s.mu.Unlock()
		close(s.done)
	}()

	out, err := s.call(ctx, []byte(`{"jsonrpc":"2.0","id":"ty-init","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"ty-relay","version":"1"}}}`))
	if err != nil {
		s.close()
		return nil, nil, fmt.Errorf("initialize: %w", err)
	}
	var env rpcEnvelope
	if err := json.Unmarshal(out, &env); err != nil || len(env.Result) == 0 {
		s.close()
		return nil, nil, fmt.Errorf("initialize failed: %s", strings.TrimSpace(string(out)))
	}
	if err := s.write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); err != nil {
		s.close()
		return nil, nil, err
	}
	return s, env.Result, nil
}

// read routes the server's replies to their callers. A request FROM the server
// (roots, sampling, elicitation) cannot be passed on to the remote agent, so it
// is refused rather than left to hang the server.
func (s *stdioSession) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var env rpcEnvelope
		if json.Unmarshal(line, &env) != nil {
			continue
		}
		if env.Method != "" {
			if len(env.ID) > 0 {
				_ = s.write(rpcErrorLine(env.ID, -32601, "not supported by ty's relay"))
			}
			continue
		}
		if len(env.ID) == 0 {
			continue
		}
		s.mu.Lock()
		ch, ok := s.pending[string(compactID(env.ID))]
		delete(s.pending, string(compactID(env.ID)))
		s.mu.Unlock()
		if ok {
			ch <- line
		}
	}
}

func compactID(id json.RawMessage) []byte {
	var b bytes.Buffer
	if json.Compact(&b, id) != nil {
		return id
	}
	return b.Bytes()
}

func (s *stdioSession) write(line []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.stdin.Write(append(append([]byte(nil), line...), '\n'))
	return err
}

// call sends one request and waits for its reply.
func (s *stdioSession) call(ctx context.Context, payload []byte) ([]byte, error) {
	id := rpcIDOf(payload)
	if string(id) == "null" {
		return nil, fmt.Errorf("request has no id")
	}
	key := string(compactID(id))
	ch := make(chan []byte, 1)
	s.mu.Lock()
	s.pending[key] = ch
	s.lastUsed = time.Now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, key)
		s.lastUsed = time.Now()
		s.mu.Unlock()
	}()
	if err := s.write(bytes.TrimSpace(payload)); err != nil {
		return nil, err
	}
	select {
	case out := <-ch:
		return out, nil
	case <-s.done:
		return nil, fmt.Errorf("the server exited")
	case <-ctx.Done():
		return nil, fmt.Errorf("no answer: %w", ctx.Err())
	}
}

func (s *stdioSession) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *stdioSession) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

func (s *stdioSession) used() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

// close stops the server: its stdin is closed so a well-behaved one exits, and
// it is killed if it has not within a moment.
func (s *stdioSession) close() {
	_ = s.stdin.Close()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		<-s.done
	}
}

// mcpRelayFor returns the executor's relay, creating it on first use.
func (e *Executor) mcpRelayFor() *mcpRelay {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	if e.relay == nil {
		e.relay = newMCPRelay(e.db)
	}
	return e.relay
}

// closeMCPRelay stops every server the relay started, if it ever started.
func (e *Executor) closeMCPRelay() {
	e.relayMu.Lock()
	r := e.relay
	e.relayMu.Unlock()
	if r != nil {
		r.Close()
	}
}

var plainHostnameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// controllerName is how a placed agent is told which machine its tools live on.
func controllerName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "the machine that placed this task"
	}
	name, _, _ = strings.Cut(name, ".")
	if !plainHostnameRe.MatchString(name) {
		return "the machine that placed this task"
	}
	return name
}

// mcpProxyInstall is what installMCPProxy put on the host.
type mcpProxyInstall struct {
	// ConfigPath is the --mcp-config file, an absolute path on the host.
	ConfigPath string
	// Servers are the relayed servers, taskyou first.
	Servers []string
}

// installMCPProxy stages the stub, its cached answers and an --mcp-config file
// in a placed task's worktree, in one round trip.
//
// A Mac-only server whose tool list cannot be read here is left out (and said
// so): registering it with an empty list would give the agent a server with no
// tools, and guessing its tools would be worse.
func (e *Executor) installMCPProxy(ctx context.Context, task *db.Task, workDir string) (mcpProxyInstall, error) {
	if !strings.HasPrefix(workDir, "/") {
		return mcpProxyInstall{}, fmt.Errorf("the worktree path %q is not absolute", workDir)
	}
	controller := controllerName()

	type served struct {
		name    string
		listing serverListing
	}
	var servers []served

	// taskyou, answered by the same code a local task's server runs.
	probe := mcp.NewServer(e.db, task.ID)
	initOut := probe.Handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	toolsOut := probe.Handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	var initEnv, toolsEnv rpcEnvelope
	if json.Unmarshal(initOut, &initEnv) != nil || json.Unmarshal(toolsOut, &toolsEnv) != nil || len(toolsEnv.Result) == 0 {
		return mcpProxyInstall{}, fmt.Errorf("could not list the taskyou tools")
	}
	servers = append(servers, served{taskyouServerName, serverListing{Init: initEnv.Result, Tools: toolsEnv.Result}})

	relay := e.mcpRelayFor()
	defs := relay.defs()
	if _, problems := proxiedServerDefs(e.db); len(problems) > 0 {
		e.logLine(task.ID, "system", "Not relaying to the placed host: "+strings.Join(problems, "; "))
	}
	for _, def := range defs {
		listCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		l, err := relay.listing(listCtx, def)
		cancel()
		if err != nil {
			e.logLine(task.ID, "system", fmt.Sprintf("Not relaying %s to the placed host: could not read its tools here (%v)", def.Name, err))
			continue
		}
		servers = append(servers, served{def.Name, l})
	}

	root := strings.TrimRight(workDir, "/")
	stub := root + "/" + mcpProxyScriptPath
	config := map[string]map[string]any{}
	files := map[string][]byte{
		".ty/.gitignore":             []byte("# Written by ty: nothing in here belongs in the repository.\n*\n"),
		mcpProxyScriptPath:           []byte(mcpProxyScript()),
		mcpProxyCacheDir + "/.limit": []byte(strconv.Itoa(int(mcpRelayCallLimit.Seconds())) + "\n"),
		mcpProxyCacheDir + "/.stale": []byte(strconv.Itoa(int(mcpRelayStale.Seconds())) + "\n"),
		mcpProxyCacheDir + "/.wait":  []byte(strconv.Itoa(int(mcpRelayAckWait.Seconds())) + "\n"),
	}
	var names []string
	for _, s := range servers {
		names = append(names, s.name)
		entry := map[string]any{"type": "stdio", "command": stub, "args": []string{s.name}}
		if s.name == taskyouServerName {
			// Mirrors the local config, so the taskyou tools do not prompt in a
			// session that is not in dangerous mode.
			entry["autoApprove"] = toolNames(s.listing.Tools)
		}
		config[s.name] = entry
		base := mcpProxyCacheDir + "/" + s.name
		files[base+".init.json"] = append(compactRaw(s.listing.Init), '\n')
		files[base+".tools.json"] = append(compactRaw(s.listing.Tools), '\n')
		files[base+".offline"] = jsonString(mcpOfflineMessage(s.name, controller))
		files[base+".lost"] = jsonString(mcpLostMessage(s.name, controller))
	}
	cfg, err := json.MarshalIndent(map[string]any{"mcpServers": config}, "", "  ")
	if err != nil {
		return mcpProxyInstall{}, err
	}
	files[mcpProxyCacheDir+"/config.json"] = append(cfg, '\n')

	archive, err := tarFiles(files)
	if err != nil {
		return mcpProxyInstall{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := command(ctx, "", "sh", "-c",
		fmt.Sprintf("umask 077; rm -rf %s && mkdir -p %s && tar -xf - -C %s",
			shellQuote(root+"/"+mcpProxyCacheDir), shellQuote(root), shellQuote(root)))
	cmd.Stdin = bytes.NewReader(archive)
	if out, err := cmd.CombinedOutput(); err != nil {
		return mcpProxyInstall{}, fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return mcpProxyInstall{ConfigPath: root + "/" + mcpProxyCacheDir + "/config.json", Servers: names}, nil
}

// toolNames lists the names in a tools/list result.
func toolNames(result json.RawMessage) []string {
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(result, &list)
	var names []string
	for _, t := range list.Tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}

func compactRaw(raw json.RawMessage) []byte {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	return b.Bytes()
}

func jsonString(s string) []byte {
	b, _ := json.Marshal(s)
	return append(b, '\n')
}

// mcpOfflineMessage is the error a relayed call gets when this machine cannot
// be reached. It reads like any down MCP server, and says what to do instead,
// because an agent told only "unavailable" retries or stops.
func mcpOfflineMessage(server, controller string) string {
	msg := fmt.Sprintf("%s unavailable: the controlling machine (%s) is offline or asleep, so this call was not made. Do not wait for it or retry in a loop; carry on with your work.", server, controller)
	if server == taskyouServerName {
		msg += " To report that you have finished, run .ty/signal done \"<summary>\" from your worktree (or .ty/signal needs-input \"<question>\"); it is delivered when " + controller + " is back."
	}
	return msg
}

// mcpLostMessage is the error for a call that was sent but whose answer never
// came back: unlike the offline case, it may have taken effect.
func mcpLostMessage(server, controller string) string {
	msg := fmt.Sprintf("%s unavailable: lost contact with the controlling machine (%s) while this call was in flight, so it may or may not have taken effect. Carry on with your work.", server, controller)
	if server == taskyouServerName {
		msg += " If this was taskyou_complete or taskyou_needs_input, report it again with .ty/signal (done/needs-input) from your worktree; a repeat is harmless."
	}
	return msg
}

// tarFiles packs files (path → content) into a tar stream. The stub is the
// only executable.
func tarFiles(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	dirs := map[string]bool{}
	now := time.Now()
	for _, p := range paths {
		for d := filepath.Dir(p); d != "." && !dirs[d]; d = filepath.Dir(d) {
			dirs[d] = true
			if err := tw.WriteHeader(&tar.Header{Name: d + "/", Mode: 0o700, Typeflag: tar.TypeDir, ModTime: now}); err != nil {
				return nil, err
			}
		}
		mode := int64(0o600)
		if p == mcpProxyScriptPath {
			mode = 0o700
		}
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: mode, Size: int64(len(files[p])), Typeflag: tar.TypeReg, ModTime: now}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(files[p]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// mcpProxyScript is the stub with the spool path filled in.
func mcpProxyScript() string {
	return strings.ReplaceAll(mcpProxyScriptTemplate, spoolToken, remoteSpoolDir)
}

// mcpProxyScriptTemplate is the stub. POSIX sh and awk, for the reason the host
// agent is: it runs on any host that can run an agent, with nothing installed.
//
// Claude speaks newline-delimited JSON-RPC on its stdin. The stub needs only
// the top-level method and id of each message, which the awk below reads
// without being fooled by keys of the same name nested inside the params.
//
// Requests are handled one at a time, as they arrive. The replies it makes up
// itself (handshake, tool list, ping, errors) need no connection at all, which
// is what keeps the server registered when this machine is away.
const mcpProxyScriptTemplate = `#!/bin/sh
# Written by ty. An MCP server for the task placed in this worktree: it answers
# the handshake and tool list from .ty/mcp, and relays every other request to
# the machine that placed the task. If that machine is away it says so at once.
set -u
server=${1:?usage: mcp-proxy <server>}
case "$server" in ''|*[!A-Za-z0-9_-]*) exit 2 ;; esac
cache=$(cd "$(dirname "$0")" && pwd)/mcp
task=${WORKTREE_TASK_ID:?}
run=${WORKTREE_RUN_ID:?}
case "$task:$run" in *[!0-9a-f:]*|:*|*:) exit 2 ;; esac
spool=@@SPOOL@@/mcp
umask 077
mkdir -p "$spool" 2>/dev/null
limit=$(cat "$cache/.limit" 2>/dev/null) || limit=900
stale=$(cat "$cache/.stale" 2>/dev/null) || stale=10
ackwait=$(cat "$cache/.wait" 2>/dev/null) || ackwait=10
if sleep 0.1 2>/dev/null; then slice=0.1 per=10; else slice=1 per=1; fi
tab=$(printf '\t')
parse='
function strend(s, i, n,   c, e) {
  e = 0
  for (i = i + 1; i <= n; i++) {
    c = substr(s, i, 1)
    if (e) e = 0
    else if (c == "\\") e = 1
    else if (c == "\"") return i
  }
  return n
}
function skipws(s, k, n) {
  while (k <= n && index(" \t\r\n", substr(s, k, 1)) > 0) k++
  return k
}
{
  s = $0; n = length(s); depth = 0; method = ""; id = ""
  for (i = 1; i <= n; i++) {
    c = substr(s, i, 1)
    if (c == "\"") {
      j = strend(s, i, n)
      if (depth == 1) {
        k = skipws(s, j + 1, n)
        if (substr(s, k, 1) == ":") {
          key = substr(s, i + 1, j - i - 1)
          k = skipws(s, k + 1, n)
          if (key == "method" || key == "id") {
            if (substr(s, k, 1) == "\"") { e = strend(s, k, n); v = substr(s, k, e - k + 1); j = e }
            else { v = ""; while (k <= n && index(",}] \t\r\n", substr(s, k, 1)) == 0) { v = v substr(s, k, 1); k++ }; j = k - 1 }
            if (key == "method") method = v; else id = v
          } else j = k - 1
        }
      }
      i = j
      continue
    }
    if (c == "{" || c == "[") depth++
    else if (c == "}" || c == "]") depth--
  }
  if (substr(method, 1, 1) == "\"") method = substr(method, 2, length(method) - 2)
  printf "%s\t%s\n", method, id
}'
fresh() {
  a=$(cat "$spool/alive" 2>/dev/null) || return 1
  case "$a" in ''|*[!0-9]*) return 1 ;; esac
  [ $(( $(date +%s) - a )) -le "$stale" ]
}
reply() { printf '{"jsonrpc":"2.0","id":%s,"result":%s}\n' "$1" "$2"; }
fail() {
  m=$(cat "$cache/$server.$2" 2>/dev/null) || m='"unavailable"'
  printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":%s}}\n' "$1" "$m"
}
cached() {
  r=$(cat "$cache/$server.$2.json" 2>/dev/null) && [ -n "$r" ] && reply "$1" "$r" || fail "$1" offline
}
call() {
  rid=$2
  fresh || { fail "$rid" offline; return; }
  n=$((n + 1))
  name=$task.$run.$server.$$.$n
  if ! { printf '%s\n' "$1" >"$spool/.$name.tmp" && mv -f "$spool/.$name.tmp" "$spool/$name.req"; }; then
    rm -f "$spool/.$name.tmp"; fail "$rid" offline; return
  fi
  t=0
  while [ ! -f "$spool/$name.ack" ] && [ ! -f "$spool/$name.resp" ]; do
    if [ "$t" -ge $((ackwait * per)) ] || { [ $((t % per)) -eq 0 ] && [ "$t" -gt 0 ] && ! fresh; }; then
      if rm "$spool/$name.req" 2>/dev/null; then fail "$rid" offline; else fail "$rid" lost; fi
      rm -f "$spool/$name.ack"
      return
    fi
    sleep "$slice"; t=$((t + 1))
  done
  t=0
  while [ ! -f "$spool/$name.resp" ]; do
    if [ "$t" -ge $((limit * per)) ] || { [ $((t % per)) -eq 0 ] && [ "$t" -gt 0 ] && ! fresh; }; then
      rm -f "$spool/$name.ack"; fail "$rid" lost; return
    fi
    sleep "$slice"; t=$((t + 1))
  done
  cat "$spool/$name.resp"
  rm -f "$spool/$name.resp" "$spool/$name.ack"
}
n=0
while IFS= read -r line || [ -n "$line" ]; do
  [ -n "$line" ] || continue
  meta=$(printf '%s\n' "$line" | LC_ALL=C awk "$parse")
  method=${meta%%"$tab"*}
  rid=${meta#*"$tab"}
  [ -n "$rid" ] || continue
  case "$method" in
    '') ;;
    initialize) cached "$rid" init ;;
    tools/list) cached "$rid" tools ;;
    ping) reply "$rid" '{}' ;;
    *) call "$line" "$rid" ;;
  esac
done
`

// proxyInstructions is appended to a placed agent's prompt when its taskyou
// tools are relayed. It keeps .ty/signal as the path that works with this
// machine away, because that is exactly when the tools do not.
func proxyInstructions(controller string) string {
	return fmt.Sprintf(`

---
HOW TO FINISH THIS TASK (read this — it is different from usual)

Your taskyou_* tools (the "taskyou" MCP server) are relayed to %[1]s, the
machine that scheduled you, so they act on this task exactly as they would
there. Use them as usual: taskyou_get_project_context, taskyou_get_artifact /
taskyou_set_artifact, taskyou_needs_input, and taskyou_complete when you are
finished. Your harness may DEFER them behind tool search — load one before use
(e.g. ToolSearch "select:taskyou_complete") rather than concluding it is missing.

If a taskyou_* call fails with "unavailable", %[1]s is asleep or offline. That
is not your problem to fix: do not wait, and do not retry in a loop. Keep
working, and when you stop, report with the script in your worktree instead —
it is queued here and delivered when %[1]s is back:

    %[2]s done "<one line saying what you did>"
    %[2]s needs-input "<the question you need answered>"
    %[2]s failed "<what stopped you>"

Report exactly once: if taskyou_complete succeeded, you are finished; do not
also run %[2]s done.`, controller, signalScriptPath)
}

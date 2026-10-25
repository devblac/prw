package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/version"
)

// prw speaks the Model Context Protocol over stdio: newline-delimited JSON-RPC 2.0
// messages on stdin/stdout, logs on stderr. Only the parts an MCP tool server needs
// are implemented (initialize, ping, tools/list, tools/call, progress, cancellation);
// the official Go SDK would require raising the module's Go version.

// mcpProtocolVersions are the protocol revisions this server can speak, newest first.
var mcpProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602

	mcpDefaultWaitTimeout  = 10 * time.Minute
	mcpMaxWaitTimeout      = time.Hour
	mcpDefaultWaitInterval = 15 * time.Second
	mcpMinWaitInterval     = 5 * time.Second

	mcpInstructions = `prw reports the CI status of GitHub pull requests, aggregating GitHub Actions check runs and legacy commit statuses.
Use get_ci_status for a one-off check, and wait_for_ci after pushing to a PR to block until CI finishes (it fails fast and lists failed checks with links).
States: success, failure, error, pending, merged, closed, and no_ci (the PR has no checks or statuses at all; waiting longer will not help).`
)

func init() {
	rootCmd.AddCommand(mcpCmd)
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run an MCP server on stdio so AI agents can check and wait for PR CI",
	Long: `Run a Model Context Protocol (MCP) server over stdin/stdout.

AI coding agents (Claude Code, Claude Desktop, Cursor, ...) can then call these tools:
  get_ci_status   current CI state of a PR, with failed checks and links
  wait_for_ci     block until a PR's CI finishes (fails fast on the first failed check)
  list_watched    PRs on prw's watch list and their last known state
  watch_pr        add a PR to the watch list (picked up by a running 'prw run')
  unwatch_pr      remove a PR from the watch list

Authentication is the same as for every other command (gh auth login, GITHUB_TOKEN, ...).`,
	Example: `  # Claude Code
  claude mcp add prw -- prw mcp

  # Claude Desktop / Cursor (mcpServers in the client's JSON config)
  "prw": { "command": "prw", "args": ["mcp"] }`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return newMCPServer(os.Stdin, os.Stdout, os.Stderr, defaultMCPClient).Serve(ctx)
	},
}

// mcpClient is what the MCP tools need from GitHub.
type mcpClient interface {
	ciClient
}

func defaultMCPClient() (mcpClient, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	token := cfg.GetToken()
	if token == "" {
		return nil, errMissingToken
	}
	return newGitHubClient(token), nil
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type mcpServer struct {
	in     io.Reader
	out    io.Writer
	logOut io.Writer

	writeMu sync.Mutex

	clientFactory func() (mcpClient, error)
	clientMu      sync.Mutex
	client        mcpClient // created on first use and shared, so the ETag cache is too

	inflightMu sync.Mutex
	inflight   map[string]context.CancelFunc
	calls      sync.WaitGroup
}

func newMCPServer(in io.Reader, out, logOut io.Writer, clientFactory func() (mcpClient, error)) *mcpServer {
	return &mcpServer{
		in:            in,
		out:           out,
		logOut:        logOut,
		clientFactory: clientFactory,
		inflight:      make(map[string]context.CancelFunc),
	}
}

// Serve handles messages until stdin is closed or ctx is done. Tool calls run
// concurrently so that pings and cancellations are handled during a long wait.
func (s *mcpServer) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.calls.Wait()
	}()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(s.in)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		readErr <- scanner.Err()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			// stdin closed (err is nil on EOF): the client is shutting down. Let calls in
			// flight finish and answer; a client that can't wait sends SIGTERM, which
			// cancels ctx.
			finished := make(chan struct{})
			go func() {
				s.calls.Wait()
				close(finished)
			}()
			select {
			case <-finished:
			case <-ctx.Done():
			}
			return err
		case line := <-lines:
			s.handleLine(ctx, line)
		}
	}
}

func (s *mcpServer) handleLine(ctx context.Context, line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	if line[0] == '[' {
		s.respondError(json.RawMessage("null"), rpcInvalidRequest, "batch requests are not supported")
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.respondError(json.RawMessage("null"), rpcParseError, "parse error: "+err.Error())
		return
	}
	if req.Method == "" {
		return // a response to a request we never send; ignore
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		s.handleNotification(req)
		return
	}

	switch req.Method {
	case "initialize":
		s.respond(req.ID, s.initializeResult(req.Params))
	case "ping":
		s.respond(req.ID, struct{}{})
	case "tools/list":
		s.respond(req.ID, map[string]interface{}{"tools": mcpTools})
	case "tools/call":
		s.startToolCall(ctx, req)
	default:
		s.respondError(req.ID, rpcMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *mcpServer) handleNotification(req rpcRequest) {
	if req.Method != "notifications/cancelled" {
		return // notifications/initialized and others need no action
	}
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(req.Params, &params) != nil {
		return
	}
	s.inflightMu.Lock()
	cancel := s.inflight[string(params.RequestID)]
	s.inflightMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *mcpServer) initializeResult(params json.RawMessage) interface{} {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	negotiated := mcpProtocolVersions[0]
	for _, v := range mcpProtocolVersions {
		if v == p.ProtocolVersion {
			negotiated = v
		}
	}
	return map[string]interface{}{
		"protocolVersion": negotiated,
		"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		"serverInfo":      map[string]string{"name": "prw", "version": version.Version},
		"instructions":    mcpInstructions,
	}
}

func (s *mcpServer) startToolCall(ctx context.Context, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		s.respondError(req.ID, rpcInvalidParams, "tools/call needs a tool name")
		return
	}
	handler, ok := mcpToolHandlers[params.Name]
	if !ok {
		s.respondError(req.ID, rpcInvalidParams, "unknown tool: "+params.Name)
		return
	}

	callCtx, cancel := context.WithCancel(ctx)
	key := string(req.ID)
	s.inflightMu.Lock()
	s.inflight[key] = cancel
	s.inflightMu.Unlock()

	s.calls.Add(1)
	go func() {
		defer s.calls.Done()
		defer func() {
			s.inflightMu.Lock()
			delete(s.inflight, key)
			s.inflightMu.Unlock()
			cancel()
		}()

		var progress io.Writer = io.Discard
		if len(params.Meta.ProgressToken) > 0 && string(params.Meta.ProgressToken) != "null" {
			progress = &mcpProgressWriter{server: s, token: params.Meta.ProgressToken}
		}

		result := handler(callCtx, s, params.Arguments, progress)
		if callCtx.Err() != nil {
			// Cancelled by the client (or the server is shutting down): per the spec the
			// receiver should not answer a cancelled request.
			return
		}
		s.respond(req.ID, result)
	}()
}

// githubClient returns the shared GitHub client, creating it on first use so a missing
// token is reported as a tool error instead of preventing the server from starting.
func (s *mcpServer) githubClient() (mcpClient, error) {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	c, err := s.clientFactory()
	if err != nil {
		return nil, err
	}
	s.client = c
	return c, nil
}

func (s *mcpServer) respond(id json.RawMessage, result interface{}) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *mcpServer) respondError(id json.RawMessage, code int, message string) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (s *mcpServer) write(msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(s.logOut, "prw mcp: failed to encode message: %v\n", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.out.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(s.logOut, "prw mcp: failed to write message: %v\n", err)
	}
}

// mcpProgressWriter turns each line waitForCI prints into a notifications/progress
// message for the tool call that asked for progress.
type mcpProgressWriter struct {
	server *mcpServer
	token  json.RawMessage
	buf    []byte
	count  int
}

func (w *mcpProgressWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line == "" {
			continue
		}
		w.count++
		w.server.write(rpcNotification{
			JSONRPC: "2.0",
			Method:  "notifications/progress",
			Params: map[string]interface{}{
				"progressToken": w.token,
				"progress":      w.count,
				"message":       line,
			},
		})
	}
}

// --- tools ---

type mcpToolHandler func(ctx context.Context, s *mcpServer, args json.RawMessage, progress io.Writer) mcpToolResult

type mcpToolResult struct {
	Content           []mcpContent `json:"content"`
	StructuredContent interface{}  `json:"structuredContent,omitempty"`
	IsError           bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func toolError(format string, args ...interface{}) mcpToolResult {
	return mcpToolResult{Content: []mcpContent{{Type: "text", Text: fmt.Sprintf(format, args...)}}, IsError: true}
}

// toolOK returns a short human summary followed by the JSON data, plus the data as
// structuredContent for clients that support it.
func toolOK(summary string, data interface{}) mcpToolResult {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return toolError("failed to encode result: %v", err)
	}
	return mcpToolResult{
		Content:           []mcpContent{{Type: "text", Text: summary + "\n\n" + string(encoded)}},
		StructuredContent: data,
	}
}

var prURLSchema = map[string]interface{}{
	"type":        "string",
	"description": "GitHub pull request URL, e.g. https://github.com/owner/repo/pull/123",
}

var mcpTools = []map[string]interface{}{
	{
		"name":        "get_ci_status",
		"title":       "Get PR CI status",
		"description": "Get the current CI status of a GitHub pull request: overall state (success, failure, error, pending, merged, closed, or no_ci when the PR has no checks at all), check counts, and failed checks with links. Aggregates GitHub Actions check runs and legacy commit statuses. Returns immediately.",
		"inputSchema": map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{"pr_url": prURLSchema},
			"required":             []string{"pr_url"},
			"additionalProperties": false,
		},
		"annotations": map[string]interface{}{"readOnlyHint": true, "openWorldHint": true},
	},
	{
		"name":        "wait_for_ci",
		"title":       "Wait for PR CI",
		"description": "Block until a GitHub pull request's CI finishes, then return the result. Fails fast: returns as soon as any check fails, with the failed checks and links. Follows new pushes to the PR. If the timeout is reached first, returns state 'pending' with timed_out=true (call again to keep waiting). A merge while waiting returns state 'merged'; a PR with no checks or statuses at all returns 'no_ci' instead of waiting until the timeout.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pr_url": prURLSchema,
				"timeout_seconds": map[string]interface{}{
					"type":        "integer",
					"description": "Give up after this many seconds (default 600, max 3600).",
					"minimum":     1,
					"maximum":     int(mcpMaxWaitTimeout / time.Second),
				},
				"interval_seconds": map[string]interface{}{
					"type":        "integer",
					"description": "Seconds between polls (default 15, min 5).",
					"minimum":     int(mcpMinWaitInterval / time.Second),
				},
			},
			"required":             []string{"pr_url"},
			"additionalProperties": false,
		},
		"annotations": map[string]interface{}{"readOnlyHint": true, "openWorldHint": true},
	},
	{
		"name":        "list_watched",
		"title":       "List watched PRs",
		"description": "List the pull requests on prw's local watch list with their last known CI state. Reads local state only; use get_ci_status for fresh data.",
		"inputSchema": map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		"annotations": map[string]interface{}{"readOnlyHint": true, "openWorldHint": false},
	},
	{
		"name":        "watch_pr",
		"title":       "Watch a PR",
		"description": "Add a GitHub pull request to prw's watch list so a running 'prw run' sends desktop/Slack/Discord notifications when its CI status changes. Idempotent.",
		"inputSchema": map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{"pr_url": prURLSchema},
			"required":             []string{"pr_url"},
			"additionalProperties": false,
		},
		"annotations": map[string]interface{}{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
	},
	{
		"name":        "unwatch_pr",
		"title":       "Stop watching a PR",
		"description": "Remove a GitHub pull request from prw's watch list. Idempotent.",
		"inputSchema": map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{"pr_url": prURLSchema},
			"required":             []string{"pr_url"},
			"additionalProperties": false,
		},
		"annotations": map[string]interface{}{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
	},
}

var mcpToolHandlers map[string]mcpToolHandler

func init() {
	// Assigned in init to avoid an initialization cycle (handlers reference mcpServer).
	mcpToolHandlers = map[string]mcpToolHandler{
		"get_ci_status": toolGetCIStatus,
		"wait_for_ci":   toolWaitForCI,
		"list_watched":  toolListWatched,
		"watch_pr":      toolWatchPR,
		"unwatch_pr":    toolUnwatchPR,
	}
}

type prURLArgs struct {
	PRURL string `json:"pr_url"`
}

func parsePRArg(args json.RawMessage) (owner, repo string, number int, err error) {
	var a prURLArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", "", 0, fmt.Errorf("invalid arguments: %v", err)
		}
	}
	if strings.TrimSpace(a.PRURL) == "" {
		return "", "", 0, errors.New("pr_url is required")
	}
	owner, repo, number, err = github.ParsePRURL(a.PRURL)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid pr_url %q: %v", a.PRURL, err)
	}
	return owner, repo, number, nil
}

func ciSummary(r *waitResult) string {
	pr := fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
	switch {
	case r.TimedOut:
		return fmt.Sprintf("Timed out after %s: CI of %s is still pending (%d of %d check(s) running). Call wait_for_ci again to keep waiting.",
			formatElapsed(r.ElapsedSeconds), pr, r.PendingChecks, r.TotalChecks)
	case r.State == "merged" || r.State == "closed":
		return fmt.Sprintf("%s was %s.", pr, r.State)
	case r.State == "pending":
		return fmt.Sprintf("CI of %s is pending: %s.", pr, pendingSummary(r))
	case r.State == "success":
		return fmt.Sprintf("CI of %s passed (%d check(s)).", pr, r.TotalChecks)
	case r.State == github.StateNoCI:
		return fmt.Sprintf("%s has no CI: no checks or statuses were reported within %s of its last update, so nothing verified it.", pr, github.NoCIGracePeriod)
	}
	names := make([]string, len(r.FailedChecks))
	for i, c := range r.FailedChecks {
		names[i] = c.Name
	}
	summary := fmt.Sprintf("CI of %s finished with %s.", pr, r.State)
	if len(names) > 0 {
		summary += " Failed: " + strings.Join(names, ", ") + "."
	}
	return summary
}

func toolGetCIStatus(ctx context.Context, s *mcpServer, args json.RawMessage, _ io.Writer) mcpToolResult {
	owner, repo, number, err := parsePRArg(args)
	if err != nil {
		return toolError("%v", err)
	}
	client, err := s.githubClient()
	if err != nil {
		return toolError("%v", err)
	}
	result := &waitResult{Owner: owner, Repo: repo, Number: number, URL: github.FormatPRURL(owner, repo, number)}
	if _, err := pollOnce(ctx, client, result); err != nil {
		return toolError("failed to get CI status of %s/%s#%d: %v", owner, repo, number, err)
	}
	return toolOK(ciSummary(result), result)
}

func toolWaitForCI(ctx context.Context, s *mcpServer, args json.RawMessage, progress io.Writer) mcpToolResult {
	owner, repo, number, err := parsePRArg(args)
	if err != nil {
		return toolError("%v", err)
	}
	var opts struct {
		TimeoutSeconds  int `json:"timeout_seconds"`
		IntervalSeconds int `json:"interval_seconds"`
	}
	if err := json.Unmarshal(args, &opts); err != nil {
		return toolError("invalid arguments: %v", err)
	}
	timeout := mcpDefaultWaitTimeout
	if opts.TimeoutSeconds != 0 {
		if opts.TimeoutSeconds < 0 {
			return toolError("timeout_seconds must be positive")
		}
		timeout = time.Duration(opts.TimeoutSeconds) * time.Second
	}
	if timeout > mcpMaxWaitTimeout {
		timeout = mcpMaxWaitTimeout
	}
	interval := mcpDefaultWaitInterval
	if opts.IntervalSeconds != 0 {
		interval = time.Duration(opts.IntervalSeconds) * time.Second
	}
	if interval < mcpMinWaitInterval {
		interval = mcpMinWaitInterval
	}

	client, err := s.githubClient()
	if err != nil {
		return toolError("%v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := waitForCI(waitCtx, client, owner, repo, number, interval, progress)
	if err != nil {
		return toolError("failed waiting for CI of %s/%s#%d: %v", owner, repo, number, err)
	}
	return toolOK(ciSummary(result), result)
}

type watchedPROutput struct {
	URL         string     `json:"url"`
	Owner       string     `json:"owner"`
	Repo        string     `json:"repo"`
	Number      int        `json:"number"`
	Title       string     `json:"title,omitempty"`
	LastState   string     `json:"last_known_state"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
}

func toolListWatched(ctx context.Context, s *mcpServer, _ json.RawMessage, _ io.Writer) mcpToolResult {
	cfg, err := config.Load()
	if err != nil {
		return toolError("failed to load config: %v", err)
	}
	prs := make([]watchedPROutput, 0, len(cfg.WatchedPRs))
	for _, pr := range cfg.WatchedPRs {
		state := github.NormalizeState(pr.LastKnownState)
		if state == "" {
			state = "unknown"
		}
		out := watchedPROutput{
			URL: github.FormatPRURL(pr.Owner, pr.Repo, pr.Number), Owner: pr.Owner, Repo: pr.Repo,
			Number: pr.Number, Title: pr.Title, LastState: state,
		}
		if !pr.LastChecked.IsZero() {
			t := pr.LastChecked
			out.LastChecked = &t
		}
		prs = append(prs, out)
	}
	summary := fmt.Sprintf("%d PR(s) on the watch list.", len(prs))
	if len(prs) == 0 {
		summary = "No PRs on the watch list. Add one with watch_pr."
	}
	return toolOK(summary, map[string]interface{}{"watched_prs": prs})
}

func toolWatchPR(ctx context.Context, s *mcpServer, args json.RawMessage, _ io.Writer) mcpToolResult {
	owner, repo, number, err := parsePRArg(args)
	if err != nil {
		return toolError("%v", err)
	}
	client, err := s.githubClient()
	if err != nil {
		return toolError("%v", err)
	}
	pr, err := client.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return toolError("failed to fetch %s/%s#%d: %v", owner, repo, number, err)
	}

	added := false
	if err := config.Update(func(c *config.Config) error {
		added = c.AddPR(config.WatchedPR{Owner: owner, Repo: repo, Number: number, Title: pr.Title})
		if !added {
			return config.ErrNoChanges
		}
		return nil
	}); err != nil {
		return toolError("failed to save config: %v", err)
	}

	summary := fmt.Sprintf("Now watching %s/%s#%d - %s. A running 'prw run' picks it up on its next poll.", owner, repo, number, pr.Title)
	if !added {
		summary = fmt.Sprintf("%s/%s#%d was already being watched.", owner, repo, number)
	}
	return toolOK(summary, map[string]interface{}{"url": github.FormatPRURL(owner, repo, number), "title": pr.Title, "added": added})
}

func toolUnwatchPR(ctx context.Context, s *mcpServer, args json.RawMessage, _ io.Writer) mcpToolResult {
	owner, repo, number, err := parsePRArg(args)
	if err != nil {
		return toolError("%v", err)
	}
	removed := false
	if err := config.Update(func(c *config.Config) error {
		removed = c.RemovePR(owner, repo, number)
		if !removed {
			return config.ErrNoChanges
		}
		return nil
	}); err != nil {
		return toolError("failed to update config: %v", err)
	}

	summary := fmt.Sprintf("Stopped watching %s/%s#%d.", owner, repo, number)
	if !removed {
		summary = fmt.Sprintf("%s/%s#%d was not being watched.", owner, repo, number)
	}
	return toolOK(summary, map[string]interface{}{"url": github.FormatPRURL(owner, repo, number), "removed": removed})
}

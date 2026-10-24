package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

// mcpHarness runs an mcpServer over in-memory pipes and lets a test exchange messages.
type mcpHarness struct {
	t       *testing.T
	in      *io.PipeWriter
	out     *bufio.Scanner
	done    chan error
	cancel  context.CancelFunc
	pending []map[string]interface{} // messages read while waiting for something else
	mu      sync.Mutex
}

func startMCP(t *testing.T, factory func() (mcpClient, error)) *mcpHarness {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".prw", "config.json")
	oldPath := config.ConfigPath
	config.ConfigPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { config.ConfigPath = oldPath })

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	h := &mcpHarness{t: t, in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1), cancel: cancel}
	h.out.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	srv := newMCPServer(inR, outW, io.Discard, factory)
	go func() {
		h.done <- srv.Serve(ctx)
		outW.Close()
	}()
	t.Cleanup(func() {
		cancel()
		inW.Close()
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return h
}

func (h *mcpHarness) send(msg string) {
	h.t.Helper()
	if _, err := io.WriteString(h.in, msg+"\n"); err != nil {
		h.t.Fatalf("write: %v", err)
	}
}

func (h *mcpHarness) readMessage() map[string]interface{} {
	h.t.Helper()
	type res struct {
		m   map[string]interface{}
		err error
	}
	ch := make(chan res, 1)
	go func() {
		if !h.out.Scan() {
			ch <- res{err: fmt.Errorf("stream closed: %v", h.out.Err())}
			return
		}
		var m map[string]interface{}
		err := json.Unmarshal(h.out.Bytes(), &m)
		ch <- res{m, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			h.t.Fatalf("read: %v", r.err)
		}
		return r.m
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for a message")
	}
	return nil
}

// response reads messages until the response with the given id, keeping others.
func (h *mcpHarness) response(id int) map[string]interface{} {
	h.t.Helper()
	for i, m := range h.pending {
		if v, ok := m["id"].(float64); ok && int(v) == id {
			h.pending = append(h.pending[:i], h.pending[i+1:]...)
			return m
		}
	}
	for {
		m := h.readMessage()
		if v, ok := m["id"].(float64); ok && int(v) == id {
			return m
		}
		h.pending = append(h.pending, m)
	}
}

func (h *mcpHarness) call(id int, method string, params interface{}) map[string]interface{} {
	h.t.Helper()
	p, _ := json.Marshal(params)
	h.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, p))
	return h.response(id)
}

func (h *mcpHarness) initialize() {
	h.t.Helper()
	resp := h.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	if resp["error"] != nil {
		h.t.Fatalf("initialize failed: %v", resp["error"])
	}
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

// toolResult extracts the tool result and its text from a tools/call response.
func toolResultOf(t *testing.T, resp map[string]interface{}) (result map[string]interface{}, text string) {
	t.Helper()
	if resp["error"] != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp["error"])
	}
	result = resp["result"].(map[string]interface{})
	content := result["content"].([]interface{})
	if len(content) != 1 {
		t.Fatalf("expected 1 content item, got %v", content)
	}
	item := content[0].(map[string]interface{})
	if item["type"] != "text" {
		t.Fatalf("content type = %v", item["type"])
	}
	return result, item["text"].(string)
}

// setSleep replaces sleepFunc until the end of the test. Call it before startMCP:
// cleanups run in reverse order, so the original is restored only after the server
// (and any tool call still reading sleepFunc) has stopped.
func setSleep(t *testing.T, fn func(context.Context, time.Duration)) {
	t.Helper()
	old := sleepFunc
	sleepFunc = fn
	t.Cleanup(func() { sleepFunc = old })
}

func staticClient(c mcpClient) func() (mcpClient, error) {
	return func() (mcpClient, error) { return c, nil }
}

func successClient() *scriptedClient {
	return &scriptedClient{steps: []pollStep{{
		pr:   openPR("abc1234"),
		runs: []github.CheckRun{run("Test", "completed", "success"), run("Build", "completed", "success")},
	}}}
}

func TestMCP_Initialize(t *testing.T) {
	h := startMCP(t, staticClient(successClient()))

	resp := h.call(1, "initialize", map[string]interface{}{"protocolVersion": "2025-03-26", "capabilities": map[string]interface{}{}})
	result := resp["result"].(map[string]interface{})
	if result["protocolVersion"] != "2025-03-26" {
		t.Errorf("must echo a supported requested version, got %v", result["protocolVersion"])
	}
	if _, ok := result["capabilities"].(map[string]interface{})["tools"]; !ok {
		t.Errorf("tools capability missing: %v", result["capabilities"])
	}
	if info := result["serverInfo"].(map[string]interface{}); info["name"] != "prw" {
		t.Errorf("serverInfo = %v", info)
	}
	if result["instructions"] == "" {
		t.Error("instructions should be set")
	}

	unknown := h.call(2, "initialize", map[string]interface{}{"protocolVersion": "1999-01-01"})
	if v := unknown["result"].(map[string]interface{})["protocolVersion"]; v != mcpProtocolVersions[0] {
		t.Errorf("unsupported version should get the latest supported, got %v", v)
	}
}

func TestMCP_ToolsList(t *testing.T) {
	h := startMCP(t, staticClient(successClient()))
	h.initialize()

	resp := h.call(2, "tools/list", map[string]interface{}{})
	tools := resp["result"].(map[string]interface{})["tools"].([]interface{})
	names := map[string]bool{}
	for _, raw := range tools {
		tool := raw.(map[string]interface{})
		name := tool["name"].(string)
		names[name] = true
		if tool["description"] == "" {
			t.Errorf("%s has no description", name)
		}
		schema := tool["inputSchema"].(map[string]interface{})
		if schema["type"] != "object" {
			t.Errorf("%s inputSchema type = %v", name, schema["type"])
		}
		if _, ok := mcpToolHandlers[name]; !ok {
			t.Errorf("%s is listed but has no handler", name)
		}
	}
	for _, want := range []string{"get_ci_status", "wait_for_ci", "get_failed_logs", "list_watched", "watch_pr", "unwatch_pr"} {
		if !names[want] {
			t.Errorf("tool %s missing from tools/list", want)
		}
	}
	if len(names) != len(mcpToolHandlers) {
		t.Errorf("%d tools listed, %d handlers", len(names), len(mcpToolHandlers))
	}
}

func TestMCP_PingAndUnknownMethod(t *testing.T) {
	h := startMCP(t, staticClient(successClient()))

	if resp := h.call(1, "ping", nil); resp["error"] != nil || resp["result"] == nil {
		t.Errorf("ping response = %v", resp)
	}
	resp := h.call(2, "resources/list", map[string]interface{}{})
	if code := resp["error"].(map[string]interface{})["code"]; code != float64(rpcMethodNotFound) {
		t.Errorf("error code = %v, want %d", code, rpcMethodNotFound)
	}
}

func TestMCP_MalformedInput(t *testing.T) {
	h := startMCP(t, staticClient(successClient()))

	h.send(`{not json`)
	if code := h.readMessage()["error"].(map[string]interface{})["code"]; code != float64(rpcParseError) {
		t.Errorf("parse error code = %v", code)
	}
	h.send(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`)
	if code := h.readMessage()["error"].(map[string]interface{})["code"]; code != float64(rpcInvalidRequest) {
		t.Errorf("batch error code = %v", code)
	}
	h.send(``) // blank lines are ignored
	if resp := h.call(3, "tools/call", map[string]interface{}{"name": "no_such_tool"}); resp["error"].(map[string]interface{})["code"] != float64(rpcInvalidParams) {
		t.Errorf("unknown tool: %v", resp)
	}
	if resp := h.call(4, "tools/call", map[string]interface{}{}); resp["error"].(map[string]interface{})["code"] != float64(rpcInvalidParams) {
		t.Errorf("missing name: %v", resp)
	}
}

func TestMCP_GetCIStatus(t *testing.T) {
	h := startMCP(t, staticClient(&scriptedClient{steps: []pollStep{{
		pr:   openPR("abc1234"),
		runs: []github.CheckRun{run("Lint", "completed", "failure"), run("Test", "in_progress", "")},
	}}}))
	h.initialize()

	result, text := toolResultOf(t, h.call(2, "tools/call", map[string]interface{}{
		"name": "get_ci_status", "arguments": map[string]string{"pr_url": "https://github.com/owner/repo/pull/1"},
	}))
	if result["isError"] == true {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if !strings.HasPrefix(text, "CI of owner/repo#1 finished with failure. Failed: Lint.") {
		t.Errorf("summary = %q", strings.SplitN(text, "\n", 2)[0])
	}
	structured := result["structuredContent"].(map[string]interface{})
	if structured["state"] != "failure" || structured["total_checks"] != float64(2) || structured["pending_checks"] != float64(1) {
		t.Errorf("structuredContent = %v", structured)
	}
	failed := structured["failed_checks"].([]interface{})
	if len(failed) != 1 || failed[0].(map[string]interface{})["url"] != "https://github.com/o/r/runs/Lint" {
		t.Errorf("failed_checks = %v", failed)
	}
}

func TestMCP_ToolErrorsAreReportedAsIsError(t *testing.T) {
	tests := []struct {
		name    string
		factory func() (mcpClient, error)
		tool    string
		args    interface{}
		want    string
	}{
		{"missing pr_url", staticClient(successClient()), "get_ci_status", map[string]string{}, "pr_url is required"},
		{"invalid pr_url", staticClient(successClient()), "wait_for_ci", map[string]string{"pr_url": "nope"}, "invalid pr_url"},
		{"wrong argument type", staticClient(successClient()), "wait_for_ci", map[string]interface{}{"pr_url": "https://github.com/o/r/pull/1", "timeout_seconds": "soon"}, "invalid arguments"},
		{"missing token", func() (mcpClient, error) { return nil, errMissingToken }, "get_ci_status", map[string]string{"pr_url": "https://github.com/o/r/pull/1"}, "gh auth login"},
		{"API error", staticClient(&scriptedClient{steps: []pollStep{{prErr: &github.APIError{StatusCode: 404, Body: "Not Found"}}}}), "get_ci_status", map[string]string{"pr_url": "https://github.com/o/r/pull/1"}, "404"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := startMCP(t, tt.factory)
			h.initialize()
			result, text := toolResultOf(t, h.call(2, "tools/call", map[string]interface{}{"name": tt.tool, "arguments": tt.args}))
			if result["isError"] != true {
				t.Errorf("expected isError, got %v", result)
			}
			if !strings.Contains(text, tt.want) {
				t.Errorf("text %q should contain %q", text, tt.want)
			}
		})
	}
}

func TestMCP_WaitForCI_WithProgress(t *testing.T) {
	noSleep(t)
	h := startMCP(t, staticClient(&scriptedClient{steps: []pollStep{
		{pr: openPR("abc1234"), runs: []github.CheckRun{run("Test", "in_progress", "")}},
		{pr: openPR("abc1234"), runs: []github.CheckRun{run("Test", "completed", "success")}},
	}}))
	h.initialize()

	resp := h.call(2, "tools/call", map[string]interface{}{
		"name":      "wait_for_ci",
		"arguments": map[string]interface{}{"pr_url": "https://github.com/owner/repo/pull/1", "timeout_seconds": 60},
		"_meta":     map[string]interface{}{"progressToken": "tok-1"},
	})
	result, text := toolResultOf(t, resp)
	if result["isError"] == true || result["structuredContent"].(map[string]interface{})["state"] != "success" {
		t.Fatalf("unexpected result: %s", text)
	}

	var progress []map[string]interface{}
	for _, m := range h.pending {
		if m["method"] == "notifications/progress" {
			progress = append(progress, m["params"].(map[string]interface{}))
		}
	}
	if len(progress) != 1 {
		t.Fatalf("expected 1 progress notification, got %d: %v", len(progress), h.pending)
	}
	if progress[0]["progressToken"] != "tok-1" || progress[0]["progress"] != float64(1) ||
		!strings.Contains(progress[0]["message"].(string), "1 of 1 check(s) still running") {
		t.Errorf("progress = %v", progress[0])
	}
}

func TestMCP_WaitForCI_NoProgressWithoutToken(t *testing.T) {
	noSleep(t)
	h := startMCP(t, staticClient(&scriptedClient{steps: []pollStep{
		{pr: openPR("abc1234"), runs: []github.CheckRun{run("Test", "in_progress", "")}},
		{pr: openPR("abc1234"), runs: []github.CheckRun{run("Test", "completed", "success")}},
	}}))
	h.initialize()

	h.call(2, "tools/call", map[string]interface{}{"name": "wait_for_ci", "arguments": map[string]string{"pr_url": "https://github.com/o/r/pull/1"}})
	if len(h.pending) != 0 {
		t.Errorf("no notifications expected without a progressToken, got %v", h.pending)
	}
}

func TestMCP_WaitForCI_Timeout(t *testing.T) {
	setSleep(t, func(ctx context.Context, d time.Duration) { <-ctx.Done() })

	h := startMCP(t, staticClient(&scriptedClient{steps: []pollStep{{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "queued", "")}}}}))
	h.initialize()

	result, text := toolResultOf(t, h.call(2, "tools/call", map[string]interface{}{
		"name": "wait_for_ci", "arguments": map[string]interface{}{"pr_url": "https://github.com/o/r/pull/1", "timeout_seconds": 1},
	}))
	if result["isError"] == true {
		t.Fatalf("a timeout is a normal result, not a tool error: %s", text)
	}
	if s := result["structuredContent"].(map[string]interface{}); s["timed_out"] != true || s["state"] != "pending" {
		t.Errorf("structuredContent = %v", s)
	}
	if !strings.Contains(text, "Call wait_for_ci again") {
		t.Errorf("summary should tell the agent how to continue: %q", text)
	}
}

func TestMCP_PingWhileWaitingAndCancellation(t *testing.T) {
	setSleep(t, func(ctx context.Context, d time.Duration) { <-ctx.Done() }) // wait until cancelled

	h := startMCP(t, staticClient(&scriptedClient{steps: []pollStep{{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "queued", "")}}}}))
	h.initialize()

	h.send(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"wait_for_ci","arguments":{"pr_url":"https://github.com/o/r/pull/1"}}}`)

	// The long call must not block other requests.
	if resp := h.call(8, "ping", nil); resp["result"] == nil {
		t.Fatalf("ping during wait_for_ci failed: %v", resp)
	}

	h.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7,"reason":"user aborted"}}`)

	// No response must be sent for the cancelled request; the next ping proves the
	// server is alive and that nothing for id 7 arrived before it.
	if resp := h.call(9, "ping", nil); resp["result"] == nil {
		t.Fatalf("ping after cancel failed: %v", resp)
	}
	time.Sleep(100 * time.Millisecond)
	h.send(`{"jsonrpc":"2.0","id":10,"method":"ping"}`)
	h.response(10)
	for _, m := range h.pending {
		if m["id"] == float64(7) {
			t.Errorf("a cancelled request must not get a response: %v", m)
		}
	}
}

func TestMCP_FinishesInFlightCallsWhenStdinCloses(t *testing.T) {
	release := make(chan struct{})
	setSleep(t, func(ctx context.Context, d time.Duration) { <-release })

	h := startMCP(t, staticClient(&scriptedClient{steps: []pollStep{
		{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "queued", "")}},
		{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "completed", "success")}},
	}}))
	h.initialize()

	h.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"wait_for_ci","arguments":{"pr_url":"https://github.com/o/r/pull/1"}}}`)
	time.Sleep(50 * time.Millisecond)
	h.in.Close()
	close(release)

	result, _ := toolResultOf(t, h.response(5))
	if result["structuredContent"].(map[string]interface{})["state"] != "success" {
		t.Errorf("in-flight call should complete after stdin closes: %v", result)
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
		h.done <- nil // let the cleanup see it
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after stdin closed")
	}
}

func TestMCP_WatchListTools(t *testing.T) {
	pr := openPR("sha")
	pr.Number, pr.Title = 42, "Ship it"
	client := &scriptedClient{steps: []pollStep{{pr: pr}}}
	h := startMCP(t, staticClient(client))
	h.initialize()
	url := map[string]string{"pr_url": "https://github.com/owner/repo/pull/42"}

	_, text := toolResultOf(t, h.call(2, "tools/call", map[string]interface{}{"name": "list_watched", "arguments": map[string]string{}}))
	if !strings.HasPrefix(text, "No PRs on the watch list") {
		t.Errorf("empty list summary = %q", text)
	}

	result, text := toolResultOf(t, h.call(3, "tools/call", map[string]interface{}{"name": "watch_pr", "arguments": url}))
	if result["structuredContent"].(map[string]interface{})["added"] != true || !strings.Contains(text, "Ship it") {
		t.Errorf("watch_pr: %s", text)
	}
	result, _ = toolResultOf(t, h.call(4, "tools/call", map[string]interface{}{"name": "watch_pr", "arguments": url}))
	if result["structuredContent"].(map[string]interface{})["added"] != false {
		t.Error("watch_pr must be idempotent")
	}

	result, _ = toolResultOf(t, h.call(5, "tools/call", map[string]interface{}{"name": "list_watched"}))
	prs := result["structuredContent"].(map[string]interface{})["watched_prs"].([]interface{})
	if len(prs) != 1 {
		t.Fatalf("watched_prs = %v", prs)
	}
	if p := prs[0].(map[string]interface{}); p["url"] != "https://github.com/owner/repo/pull/42" || p["title"] != "Ship it" || p["last_known_state"] != "unknown" {
		t.Errorf("watched PR = %v", p)
	}

	loaded, _ := config.Load()
	if len(loaded.WatchedPRs) != 1 || loaded.WatchedPRs[0].Number != 42 {
		t.Errorf("watch_pr must persist to the config file: %+v", loaded.WatchedPRs)
	}

	result, _ = toolResultOf(t, h.call(6, "tools/call", map[string]interface{}{"name": "unwatch_pr", "arguments": url}))
	if result["structuredContent"].(map[string]interface{})["removed"] != true {
		t.Error("unwatch_pr should remove the PR")
	}
	result, _ = toolResultOf(t, h.call(7, "tools/call", map[string]interface{}{"name": "unwatch_pr", "arguments": url}))
	if result["structuredContent"].(map[string]interface{})["removed"] != false {
		t.Error("unwatch_pr must be idempotent")
	}
}

func TestMCP_SharesOneGitHubClient(t *testing.T) {
	created := 0
	h := startMCP(t, func() (mcpClient, error) {
		created++
		return successClient(), nil
	})
	h.initialize()
	for id := 2; id <= 4; id++ {
		h.call(id, "tools/call", map[string]interface{}{"name": "get_ci_status", "arguments": map[string]string{"pr_url": "https://github.com/o/r/pull/1"}})
	}
	if created != 1 {
		t.Errorf("client created %d times; it must be shared so the ETag cache is reused", created)
	}
}

func TestCISummary(t *testing.T) {
	tests := []struct {
		r    waitResult
		want string
	}{
		{waitResult{Owner: "o", Repo: "r", Number: 1, State: "success", TotalChecks: 3}, "CI of o/r#1 passed (3 check(s))."},
		{waitResult{Owner: "o", Repo: "r", Number: 1, State: "error"}, "CI of o/r#1 finished with error."},
		{waitResult{Owner: "o", Repo: "r", Number: 1, State: "merged"}, "o/r#1 was merged."},
		{waitResult{Owner: "o", Repo: "r", Number: 1, State: "pending", SHA: "abcdef123", TotalChecks: 2, PendingChecks: 1}, "CI of o/r#1 is pending: 1 of 2 check(s) still running on abcdef1."},
	}
	for _, tt := range tests {
		if got := ciSummary(&tt.r); got != tt.want {
			t.Errorf("ciSummary(%s) = %q, want %q", tt.r.State, got, tt.want)
		}
	}
}

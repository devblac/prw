package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/devblac/prw/internal/github"
)

func failingEvent() *StatusChangeEvent {
	return &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        42,
		Title:         "Fix <parser> & friends",
		PreviousState: "pending",
		CurrentState:  "failure",
		SHA:           "abc123",
		FailedChecks: []github.FailedCheck{
			{Name: "Unit | Tests", URL: "https://github.com/owner/repo/runs/1"},
			{Name: "Lint"},
		},
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestDetectWebhookFormat(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://hooks.slack.com/services/T000/B000/XXXX", WebhookFormatSlack},
		{"https://HOOKS.SLACK.COM/services/T000/B000/XXXX", WebhookFormatSlack},
		{"https://discord.com/api/webhooks/123/token", WebhookFormatDiscord},
		{"https://discordapp.com/api/webhooks/123/token", WebhookFormatDiscord},
		{"https://canary.discord.com/api/webhooks/123/token", WebhookFormatDiscord},
		{"https://discord.com/channels/123/456", WebhookFormatGeneric},
		{"https://example.com/hook", WebhookFormatGeneric},
		{"https://evil.com/hooks.slack.com/services/x", WebhookFormatGeneric},
		{"http://127.0.0.1:8080/webhook", WebhookFormatGeneric},
		{"not a url ::", WebhookFormatGeneric},
		{"", WebhookFormatGeneric},
	}
	for _, tt := range tests {
		if got := DetectWebhookFormat(tt.url); got != tt.want {
			t.Errorf("DetectWebhookFormat(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestWebhookFormatValidation(t *testing.T) {
	for _, valid := range []string{"", "auto", "Slack", " discord ", "GENERIC"} {
		if !IsValidWebhookFormat(valid) {
			t.Errorf("expected %q to be valid", valid)
		}
	}
	for _, invalid := range []string{"teams", "json", "slackk"} {
		if IsValidWebhookFormat(invalid) {
			t.Errorf("expected %q to be invalid", invalid)
		}
	}
	if got := NormalizeWebhookFormat("  Slack "); got != WebhookFormatSlack {
		t.Errorf("NormalizeWebhookFormat = %q, want slack", got)
	}
	if got := NormalizeWebhookFormat(""); got != WebhookFormatAuto {
		t.Errorf("empty format should normalize to auto, got %q", got)
	}
}

func TestResolveFormat(t *testing.T) {
	if got := resolveFormat("auto", "https://hooks.slack.com/services/a/b/c"); got != WebhookFormatSlack {
		t.Errorf("auto should detect slack, got %q", got)
	}
	if got := resolveFormat("", "https://discord.com/api/webhooks/1/x"); got != WebhookFormatDiscord {
		t.Errorf("empty should detect discord, got %q", got)
	}
	// An explicit format overrides detection (e.g. Slack-compatible Mattermost).
	if got := resolveFormat("slack", "https://mattermost.example.com/hooks/abc"); got != WebhookFormatSlack {
		t.Errorf("explicit slack should win, got %q", got)
	}
	// Unknown values fall back to URL detection rather than sending a broken payload.
	if got := resolveFormat("bogus", "https://example.com/hook"); got != WebhookFormatGeneric {
		t.Errorf("unknown format should fall back to detection, got %q", got)
	}
}

func TestSlackPayload(t *testing.T) {
	body, err := buildWebhookBody(WebhookFormatSlack, failingEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(payload) != 1 {
		t.Errorf("slack payload should only contain text, got keys: %v", payload)
	}
	text, _ := payload["text"].(string)

	for _, want := range []string{
		"❌",
		"<https://github.com/owner/repo/pull/42|owner/repo#42>",
		"Fix &lt;parser&gt; &amp; friends", // Slack-escaped title
		"`pending → failure`",
		"<https://github.com/owner/repo/runs/1|Unit ¦ Tests>", // pipe cannot appear in a link label
		"• Lint",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("slack text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<parser>") {
		t.Errorf("title must be escaped, got:\n%s", text)
	}
}

func TestSlackPayload_LimitsFailedChecks(t *testing.T) {
	event := failingEvent()
	event.FailedChecks = nil
	for i := 0; i < maxChecksListed+3; i++ {
		event.FailedChecks = append(event.FailedChecks, github.FailedCheck{Name: "check"})
	}
	text := slackPayload(event)["text"]
	if !strings.Contains(text, "… and 3 more") {
		t.Errorf("expected overflow line, got:\n%s", text)
	}
	if got := strings.Count(text, "• check"); got != maxChecksListed {
		t.Errorf("expected %d listed checks, got %d", maxChecksListed, got)
	}
}

func TestDiscordPayload(t *testing.T) {
	body, err := buildWebhookBody(WebhookFormatDiscord, failingEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload struct {
		Embeds []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Color       int    `json:"color"`
			Timestamp   string `json:"timestamp"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(payload.Embeds))
	}
	e := payload.Embeds[0]

	if e.Title != "❌ owner/repo#42 — Fix <parser> & friends" {
		t.Errorf("unexpected title: %q", e.Title)
	}
	if e.URL != "https://github.com/owner/repo/pull/42" {
		t.Errorf("unexpected url: %q", e.URL)
	}
	if e.Color != colorFailure {
		t.Errorf("expected failure color, got %#x", e.Color)
	}
	if e.Timestamp != "2026-01-02T03:04:05.000Z" {
		t.Errorf("unexpected timestamp: %q", e.Timestamp)
	}
	for _, want := range []string{"`pending → failure`", "• [Unit | Tests](https://github.com/owner/repo/runs/1)", "• Lint"} {
		if !strings.Contains(e.Description, want) {
			t.Errorf("description missing %q:\n%s", want, e.Description)
		}
	}
}

func TestDiscordPayload_TruncatesLongTitle(t *testing.T) {
	event := failingEvent()
	event.Title = strings.Repeat("é", 400)
	embeds := discordPayload(event)["embeds"].([]discordEmbed)
	if got := len([]rune(embeds[0].Title)); got != discordTitleLimit {
		t.Errorf("title should be truncated to %d runes, got %d", discordTitleLimit, got)
	}
	if !strings.HasSuffix(embeds[0].Title, "…") {
		t.Errorf("truncated title should end with an ellipsis")
	}
}

func TestStateColorsAndEmoji(t *testing.T) {
	tests := []struct {
		state string
		emoji string
		color int
	}{
		{"success", "✅", colorSuccess},
		{"failure", "❌", colorFailure},
		{"error", "❌", colorFailure},
		{"pending", "⏳", colorPending},
		{"", "⏳", colorPending},
	}
	for _, tt := range tests {
		if got := stateEmoji(tt.state); got != tt.emoji {
			t.Errorf("stateEmoji(%q) = %q, want %q", tt.state, got, tt.emoji)
		}
		if got := stateColor(tt.state); got != tt.color {
			t.Errorf("stateColor(%q) = %#x, want %#x", tt.state, got, tt.color)
		}
	}
}

func TestTransition_WithoutPreviousState(t *testing.T) {
	event := &StatusChangeEvent{CurrentState: "failure"}
	if got := transition(event); got != "failure" {
		t.Errorf("transition without previous state = %q, want %q", got, "failure")
	}
}

func TestGenericPayloadUnchanged(t *testing.T) {
	body, err := buildWebhookBody(WebhookFormatGeneric, failingEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload.Type != "pr_status_change" || payload.PRNumber != 42 || payload.CurrentState != "failure" || len(payload.FailedChecks) != 2 {
		t.Errorf("unexpected generic payload: %+v", payload)
	}
}

func TestWebhookNotifier_SendsRequestedFormat(t *testing.T) {
	var got []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// The test server URL is not Slack/Discord, so only an explicit format changes the shape.
	if err := NewWebhookNotifierWithFormat(server.URL, "slack").Notify(failingEvent()); err != nil {
		t.Fatalf("slack notify failed: %v", err)
	}
	if !strings.Contains(string(got), `"text"`) {
		t.Errorf("expected slack payload, got %s", got)
	}

	if err := NewWebhookNotifierWithFormat(server.URL, "discord").Notify(failingEvent()); err != nil {
		t.Fatalf("discord notify failed: %v", err)
	}
	if !strings.Contains(string(got), `"embeds"`) {
		t.Errorf("expected discord payload, got %s", got)
	}

	if err := NewWebhookNotifier(server.URL).Notify(failingEvent()); err != nil {
		t.Fatalf("auto notify failed: %v", err)
	}
	if !strings.Contains(string(got), `"pr_status_change"`) {
		t.Errorf("expected generic payload for unknown host, got %s", got)
	}
}

func TestWebhookNotifier_ErrorIncludesResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid_payload\n"))
	}))
	defer server.Close()

	err := NewWebhookNotifier(server.URL).Notify(failingEvent())
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "invalid_payload") {
		t.Errorf("error should include status and response body, got: %v", err)
	}
}

func TestNewWebhookNotifier_HasTimeout(t *testing.T) {
	n := NewWebhookNotifier("https://example.com")
	if n.HTTPClient.Timeout <= 0 {
		t.Error("webhook client must have a timeout so a hung endpoint cannot block the watcher")
	}
}

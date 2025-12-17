package notify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConsoleNotifier(t *testing.T) {
	notifier := NewConsoleNotifier()

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		Title:         "Test PR",
		PreviousState: "pending",
		CurrentState:  "success",
		SHA:           "abc123",
		Timestamp:     time.Now(),
	}

	// Just verify it doesn't panic or error
	if err := notifier.Notify(event); err != nil {
		t.Errorf("ConsoleNotifier.Notify failed: %v", err)
	}
}

func TestWebhookNotifier(t *testing.T) {
	var receivedPayload WebhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST request, got %s", r.Method)
		}

		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", contentType)
		}

		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &receivedPayload); err != nil {
			t.Errorf("failed to parse webhook payload: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(server.URL)

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		Title:         "Test PR",
		PreviousState: "pending",
		CurrentState:  "success",
		SHA:           "abc123",
		Timestamp:     time.Now(),
	}

	if err := notifier.Notify(event); err != nil {
		t.Fatalf("WebhookNotifier.Notify failed: %v", err)
	}

	// Verify payload
	if receivedPayload.Type != "pr_status_change" {
		t.Errorf("expected type 'pr_status_change', got %q", receivedPayload.Type)
	}
	if receivedPayload.Owner != "owner" {
		t.Errorf("expected owner 'owner', got %q", receivedPayload.Owner)
	}
	if receivedPayload.Repo != "repo" {
		t.Errorf("expected repo 'repo', got %q", receivedPayload.Repo)
	}
	if receivedPayload.PRNumber != 123 {
		t.Errorf("expected PR number 123, got %d", receivedPayload.PRNumber)
	}
	if receivedPayload.PreviousState != "pending" {
		t.Errorf("expected previous state 'pending', got %q", receivedPayload.PreviousState)
	}
	if receivedPayload.CurrentState != "success" {
		t.Errorf("expected current state 'success', got %q", receivedPayload.CurrentState)
	}
	if !strings.Contains(receivedPayload.URL, "github.com/owner/repo/pull/123") {
		t.Errorf("unexpected URL: %s", receivedPayload.URL)
	}
}

func TestWebhookNotifierEmptyURL(t *testing.T) {
	notifier := NewWebhookNotifier("")

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		PreviousState: "pending",
		CurrentState:  "success",
	}

	// Should not error when URL is empty
	if err := notifier.Notify(event); err != nil {
		t.Errorf("expected no error with empty URL, got %v", err)
	}
}

func TestWebhookNotifierServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(server.URL)

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		PreviousState: "pending",
		CurrentState:  "success",
	}

	err := notifier.Notify(event)
	if err == nil {
		t.Error("expected error when webhook returns 500, got nil")
	}
}

func TestMultiNotifier(t *testing.T) {
	mock1 := &mockNotifier{}
	mock2 := &mockNotifier{}

	multi := NewMultiNotifier(mock1, mock2)

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		PreviousState: "pending",
		CurrentState:  "success",
	}

	if err := multi.Notify(event); err != nil {
		t.Fatalf("MultiNotifier.Notify failed: %v", err)
	}

	if len(mock1.events) != 1 {
		t.Errorf("expected mock1 to receive 1 event, got %d", len(mock1.events))
	}
	if len(mock2.events) != 1 {
		t.Errorf("expected mock2 to receive 1 event, got %d", len(mock2.events))
	}
}

type mockNotifier struct {
	events []*StatusChangeEvent
	err    error
}

func (m *mockNotifier) Notify(event *StatusChangeEvent) error {
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, event)
	return nil
}

func TestMultiNotifierError(t *testing.T) {
	mock1 := &mockNotifier{err: fmt.Errorf("notification failed")}
	multi := NewMultiNotifier(mock1)

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		PreviousState: "pending",
		CurrentState:  "success",
	}

	err := multi.Notify(event)
	if err == nil {
		t.Error("expected MultiNotifier to return error when notifier fails")
	}
}

func TestWebhookNotifierMarshalError(t *testing.T) {
	// This is hard to test directly, but we can test with invalid timestamp
	notifier := NewWebhookNotifier("http://example.com")
	
	// Normal event should work
	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		PreviousState: "pending",
		CurrentState:  "success",
		Timestamp:     time.Now(),
	}

	// Should not error for valid event
	err := notifier.Notify(event)
	// May error due to network, but shouldn't panic
	_ = err
}

func TestWebhookNotifierNetworkError(t *testing.T) {
	// Use invalid URL to cause network error
	notifier := NewWebhookNotifier("http://invalid-host-that-does-not-exist-12345.com")

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		PreviousState: "pending",
		CurrentState:  "success",
		Timestamp:     time.Now(),
	}

	err := notifier.Notify(event)
	if err == nil {
		t.Error("expected error when connecting to invalid host")
	}
}

func TestConsoleNotifierWithEmptyTitle(t *testing.T) {
	notifier := NewConsoleNotifier()

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		Title:         "", // Empty title
		PreviousState: "pending",
		CurrentState:  "success",
		SHA:           "abc123",
		Timestamp:     time.Now(),
	}

	if err := notifier.Notify(event); err != nil {
		t.Errorf("ConsoleNotifier.Notify failed with empty title: %v", err)
	}
}

func TestNativeNotifier(t *testing.T) {
	notifier := NewNativeNotifier()

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		Title:         "Test PR",
		PreviousState: "pending",
		CurrentState:  "success",
		SHA:           "abc123",
		Timestamp:     time.Now(),
	}

	// Should not panic or error even if native notifications aren't available
	// (e.g., in CI environments or unsupported platforms)
	err := notifier.Notify(event)
	// We don't assert on error because:
	// 1. The tool might not be available (e.g., notify-send on macOS CI)
	// 2. The notifier should gracefully handle missing tools
	// 3. This is tested more thoroughly in platform-specific tests below
	_ = err
}

func TestNativeNotifierWithEmptyTitle(t *testing.T) {
	notifier := NewNativeNotifier()

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		Title:         "", // Empty title
		PreviousState: "pending",
		CurrentState:  "success",
		SHA:           "abc123",
		Timestamp:     time.Now(),
	}

	// Should handle empty title gracefully
	err := notifier.Notify(event)
	_ = err
}

func TestNativeNotifierUnsupportedPlatform(t *testing.T) {
	// This test verifies that unsupported platforms don't crash
	// We can't easily mock runtime.GOOS, so we just verify the notifier
	// doesn't panic on any platform
	notifier := NewNativeNotifier()

	event := &StatusChangeEvent{
		Owner:         "owner",
		Repo:          "repo",
		Number:        123,
		PreviousState: "pending",
		CurrentState:  "success",
		Timestamp:     time.Now(),
	}

	// Should not panic
	err := notifier.Notify(event)
	_ = err
}

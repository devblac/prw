package notify

import (
	"encoding/json"
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
}

func (m *mockNotifier) Notify(event *StatusChangeEvent) error {
	m.events = append(m.events, event)
	return nil
}

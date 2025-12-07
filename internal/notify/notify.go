package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/devblac/prw/internal/github"
)

// StatusChangeEvent represents a CI status change for a PR.
type StatusChangeEvent struct {
	Owner         string
	Repo          string
	Number        int
	Title         string
	PreviousState string
	CurrentState  string
	SHA           string
	Timestamp     time.Time
}

// Notifier sends notifications about status changes.
type Notifier interface {
	Notify(event *StatusChangeEvent) error
}

// MultiNotifier combines multiple notifiers.
type MultiNotifier struct {
	notifiers []Notifier
}

// NewMultiNotifier creates a notifier that sends to all provided notifiers.
func NewMultiNotifier(notifiers ...Notifier) *MultiNotifier {
	return &MultiNotifier{notifiers: notifiers}
}

// Notify sends the event to all notifiers.
func (m *MultiNotifier) Notify(event *StatusChangeEvent) error {
	for _, n := range m.notifiers {
		if err := n.Notify(event); err != nil {
			return err
		}
	}
	return nil
}

// ConsoleNotifier prints notifications to stdout.
type ConsoleNotifier struct{}

// NewConsoleNotifier creates a console notifier.
func NewConsoleNotifier() *ConsoleNotifier {
	return &ConsoleNotifier{}
}

// Notify prints the status change to console.
func (c *ConsoleNotifier) Notify(event *StatusChangeEvent) error {
	prURL := github.FormatPRURL(event.Owner, event.Repo, event.Number)

	fmt.Printf("\n🔔 Status Change Detected!\n")
	fmt.Printf("   PR: %s/%s#%d\n", event.Owner, event.Repo, event.Number)
	if event.Title != "" {
		fmt.Printf("   Title: %s\n", event.Title)
	}
	fmt.Printf("   Status: %s → %s\n", event.PreviousState, event.CurrentState)
	fmt.Printf("   Link: %s\n", prURL)
	fmt.Printf("   Time: %s\n\n", event.Timestamp.Format(time.RFC3339))

	return nil
}

// WebhookNotifier sends notifications to a webhook URL.
type WebhookNotifier struct {
	URL        string
	HTTPClient *http.Client
}

// NewWebhookNotifier creates a webhook notifier.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		URL:        url,
		HTTPClient: http.DefaultClient,
	}
}

// WebhookPayload is the JSON structure sent to the webhook.
type WebhookPayload struct {
	Type          string    `json:"type"`
	Owner         string    `json:"owner"`
	Repo          string    `json:"repo"`
	PRNumber      int       `json:"pr_number"`
	Title         string    `json:"title,omitempty"`
	PreviousState string    `json:"previous_state"`
	CurrentState  string    `json:"current_state"`
	SHA           string    `json:"sha"`
	URL           string    `json:"url"`
	Timestamp     time.Time `json:"timestamp"`
}

// Notify sends the status change to the webhook.
func (w *WebhookNotifier) Notify(event *StatusChangeEvent) error {
	if w.URL == "" {
		return nil
	}

	payload := WebhookPayload{
		Type:          "pr_status_change",
		Owner:         event.Owner,
		Repo:          event.Repo,
		PRNumber:      event.Number,
		Title:         event.Title,
		PreviousState: event.PreviousState,
		CurrentState:  event.CurrentState,
		SHA:           event.SHA,
		URL:           github.FormatPRURL(event.Owner, event.Repo, event.Number),
		Timestamp:     event.Timestamp,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	req, err := http.NewRequest("POST", w.URL, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := w.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	return nil
}

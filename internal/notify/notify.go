package notify

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	FailedChecks  []github.FailedCheck
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

// Notify sends the event to every notifier, even if an earlier one fails - a broken
// webhook must not stop the console or native notifications from firing. Errors from
// every notifier that failed are combined into one (nil if all succeeded).
func (m *MultiNotifier) Notify(event *StatusChangeEvent) error {
	var errs []error
	for _, n := range m.notifiers {
		if err := n.Notify(event); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
	if len(event.FailedChecks) > 0 {
		fmt.Printf("   Failed checks:\n")
		for _, check := range event.FailedChecks {
			if check.URL != "" {
				fmt.Printf("     ❌ %s (%s)\n", check.Name, check.URL)
			} else {
				fmt.Printf("     ❌ %s\n", check.Name)
			}
		}
	}
	fmt.Printf("   Link: %s\n", prURL)
	fmt.Printf("   Time: %s\n\n", event.Timestamp.Format(time.RFC3339))

	return nil
}

// WebhookNotifier sends notifications to a webhook URL.
type WebhookNotifier struct {
	URL string
	// Format is one of the WebhookFormat* constants; empty or "auto" detects it from URL.
	Format     string
	HTTPClient *http.Client
}

// NewWebhookNotifier creates a webhook notifier that auto-detects the payload format
// (Slack, Discord, or generic JSON) from the URL.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return NewWebhookNotifierWithFormat(url, WebhookFormatAuto)
}

// NewWebhookNotifierWithFormat creates a webhook notifier with an explicit payload format.
func NewWebhookNotifierWithFormat(url, format string) *WebhookNotifier {
	return &WebhookNotifier{
		URL:        url,
		Format:     NormalizeWebhookFormat(format),
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// WebhookPayload is the JSON structure sent to the webhook.
type WebhookPayload struct {
	Type          string               `json:"type"`
	Owner         string               `json:"owner"`
	Repo          string               `json:"repo"`
	PRNumber      int                  `json:"pr_number"`
	Title         string               `json:"title,omitempty"`
	PreviousState string               `json:"previous_state"`
	CurrentState  string               `json:"current_state"`
	SHA           string               `json:"sha"`
	FailedChecks  []github.FailedCheck `json:"failed_checks,omitempty"`
	URL           string               `json:"url"`
	Timestamp     time.Time            `json:"timestamp"`
}

// Notify sends the status change to the webhook.
func (w *WebhookNotifier) Notify(event *StatusChangeEvent) error {
	if w.URL == "" {
		return nil
	}

	data, err := buildWebhookBody(resolveFormat(w.Format, w.URL), event)
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return fmt.Errorf("webhook returned non-2xx status: %d (%s)", resp.StatusCode, msg)
		}
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	return nil
}

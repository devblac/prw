package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
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

// NativeNotifier sends notifications using OS-native notification systems.
type NativeNotifier struct {
	enabled bool
}

// NewNativeNotifier creates a native notifier.
// It checks if the platform supports native notifications and if the required tools are available.
func NewNativeNotifier() *NativeNotifier {
	return &NativeNotifier{
		enabled: isNativeNotificationSupported(),
	}
}

// Notify sends a native system notification.
func (n *NativeNotifier) Notify(event *StatusChangeEvent) error {
	if !n.enabled {
		// Silently skip if not supported - this is expected on unsupported platforms
		return nil
	}

	title := fmt.Sprintf("PR Status Change: %s/%s#%d", event.Owner, event.Repo, event.Number)
	message := fmt.Sprintf("%s → %s", event.PreviousState, event.CurrentState)
	if event.Title != "" {
		message = fmt.Sprintf("%s\n%s", event.Title, message)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// macOS: use osascript to display notification
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, escapeAppleScriptString(message), escapeAppleScriptString(title))
		cmd = exec.Command("osascript", "-e", script)
	case "linux":
		// Linux: use notify-send (requires libnotify-bin)
		cmd = exec.Command("notify-send", title, message)
	case "windows":
		// Windows: use PowerShell to show toast notification
		// Escape XML entities and PowerShell special characters
		titleEscaped := escapePowerShellXMLString(title)
		messageEscaped := escapePowerShellXMLString(message)
		psScript := fmt.Sprintf(`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = Windows.UI.Notifications]::CreateToastNotifier("prw").Show([Windows.UI.Notifications.ToastNotification]::new([Windows.Data.Xml.Dom.XmlDocument]::new().LoadXml("<toast><visual><binding template=\"ToastText02\"><text id=\"1">%s</text><text id=\"2">%s</text></binding></visual></toast>")))`, titleEscaped, messageEscaped)
		cmd = exec.Command("powershell", "-NoProfile", "-Command", psScript)
	default:
		// Unsupported platform - silently skip
		return nil
	}

	if err := cmd.Run(); err != nil {
		// Log but don't fail - native notifications are optional
		return fmt.Errorf("native notification failed (tool may be missing): %w", err)
	}

	return nil
}

// isNativeNotificationSupported checks if native notifications are supported on this platform.
func isNativeNotificationSupported() bool {
	switch runtime.GOOS {
	case "darwin":
		// Check if osascript is available
		_, err := exec.LookPath("osascript")
		return err == nil
	case "linux":
		// Check if notify-send is available
		_, err := exec.LookPath("notify-send")
		return err == nil
	case "windows":
		// PowerShell should be available on Windows
		_, err := exec.LookPath("powershell")
		return err == nil
	default:
		return false
	}
}

// escapeAppleScriptString escapes special characters for AppleScript strings.
func escapeAppleScriptString(s string) string {
	// Replace quotes and backslashes
	result := ""
	for _, r := range s {
		switch r {
		case '"':
			result += `\"`
		case '\\':
			result += `\\`
		case '\n':
			result += `\n`
		default:
			result += string(r)
		}
	}
	return result
}

// escapePowerShellXMLString escapes special characters for XML embedded in PowerShell strings.
func escapePowerShellXMLString(s string) string {
	// Escape XML entities for safe embedding in XML
	result := ""
	for _, r := range s {
		switch r {
		case '<':
			result += "&lt;"
		case '>':
			result += "&gt;"
		case '&':
			result += "&amp;"
		case '"':
			result += "&quot;"
		case '\'':
			result += "&apos;"
		default:
			result += string(r)
		}
	}
	return result
}

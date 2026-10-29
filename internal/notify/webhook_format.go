package notify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

// Webhook payload formats.
const (
	// WebhookFormatAuto picks a format based on the webhook URL host.
	WebhookFormatAuto = "auto"
	// WebhookFormatSlack sends a Slack incoming-webhook message ({"text": ...}).
	WebhookFormatSlack = "slack"
	// WebhookFormatDiscord sends a Discord webhook embed ({"embeds": [...]}).
	WebhookFormatDiscord = "discord"
	// WebhookFormatGeneric sends prw's raw JSON payload (see WebhookPayload).
	WebhookFormatGeneric = "generic"
)

const (
	maxChecksListed       = 10
	discordTitleLimit     = 256
	discordDescriptionMax = 4000

	colorSuccess = 0x2EA043
	colorFailure = 0xD1242F
	colorPending = 0xDBAB09
	colorNoCI    = 0x8C959F
	colorChanges = 0xBC4C00
)

// IsValidWebhookFormat reports whether value is a supported webhook format.
func IsValidWebhookFormat(value string) bool {
	switch NormalizeWebhookFormat(value) {
	case WebhookFormatAuto, WebhookFormatSlack, WebhookFormatDiscord, WebhookFormatGeneric:
		return true
	}
	return false
}

// NormalizeWebhookFormat lowercases and trims a user-supplied format; empty means auto.
func NormalizeWebhookFormat(value string) string {
	format := strings.ToLower(strings.TrimSpace(value))
	if format == "" {
		return WebhookFormatAuto
	}
	return format
}

// DetectWebhookFormat picks a payload format from the webhook URL.
// Slack and Discord reject prw's raw JSON payload, so they need their own shapes.
func DetectWebhookFormat(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return WebhookFormatGeneric
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "hooks.slack.com" || host == "hooks.slack-gov.com": // GovSlack uses its own domain
		return WebhookFormatSlack
	case (host == "discord.com" || host == "discordapp.com" || strings.HasSuffix(host, ".discord.com") || strings.HasSuffix(host, ".discordapp.com")) &&
		strings.HasPrefix(u.Path, "/api/webhooks/"):
		return WebhookFormatDiscord
	}
	return WebhookFormatGeneric
}

// resolveFormat turns "auto" (or an unknown value) into a concrete format.
func resolveFormat(format, rawURL string) string {
	switch f := NormalizeWebhookFormat(format); f {
	case WebhookFormatSlack, WebhookFormatDiscord, WebhookFormatGeneric:
		return f
	}
	return DetectWebhookFormat(rawURL)
}

// buildWebhookBody renders the event in the requested webhook format.
func buildWebhookBody(format string, event *StatusChangeEvent) ([]byte, error) {
	switch format {
	case WebhookFormatSlack:
		return json.Marshal(slackPayload(event))
	case WebhookFormatDiscord:
		return json.Marshal(discordPayload(event))
	default:
		return json.Marshal(genericPayload(event))
	}
}

func genericPayload(event *StatusChangeEvent) WebhookPayload {
	return WebhookPayload{
		Type:          payloadType(event.Kind),
		Host:          forge.StorageHost(event.Host),
		Owner:         event.Owner,
		Repo:          event.Repo,
		PRNumber:      event.Number,
		Title:         event.Title,
		PreviousState: event.PreviousState,
		CurrentState:  event.CurrentState,
		SHA:           event.SHA,
		FailedChecks:  event.FailedChecks,
		URL:           forge.PRURL(event.Host, event.Owner, event.Repo, event.Number),
		Timestamp:     event.Timestamp,
	}
}

// payloadType is the generic payload's "type" for each event kind.
func payloadType(kind string) string {
	switch kind {
	case EventKindReview:
		return "pr_review_change"
	case EventKindMerge:
		return "pr_merge_state_change"
	default:
		return "pr_status_change"
	}
}

// stateLabel names what changed, e.g. "Status: pending → success".
func stateLabel(kind string) string {
	switch kind {
	case EventKindReview:
		return "Review"
	case EventKindMerge:
		return "Merge"
	default:
		return "Status"
	}
}

func stateEmoji(state string) string {
	switch state {
	case "success", github.ReviewApproved:
		return "✅"
	case "failure", "error":
		return "❌"
	case github.StateNoCI:
		return "⚪"
	case github.ReviewChangesRequested:
		return "📝"
	case github.MergeConflict:
		return "⚠️"
	case github.MergeReady:
		return "🚀"
	default:
		return "⏳"
	}
}

func stateColor(state string) int {
	switch state {
	case "success", github.ReviewApproved, github.MergeReady:
		return colorSuccess
	case "failure", "error", github.MergeConflict:
		return colorFailure
	case github.ReviewChangesRequested:
		return colorChanges
	case github.StateNoCI:
		return colorNoCI
	default:
		return colorPending
	}
}

func transition(event *StatusChangeEvent) string {
	if event.PreviousState == "" {
		return event.CurrentState
	}
	return fmt.Sprintf("%s → %s", event.PreviousState, event.CurrentState)
}

// slackEscape escapes the three characters Slack treats specially in mrkdwn text.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// slackLinkText makes s safe to use as the label of a Slack <url|label> link.
func slackLinkText(s string) string {
	return strings.NewReplacer("|", "¦", "\n", " ").Replace(slackEscape(s))
}

func slackPayload(event *StatusChangeEvent) map[string]string {
	prURL := forge.PRURL(event.Host, event.Owner, event.Repo, event.Number)

	var b strings.Builder
	fmt.Fprintf(&b, "%s *<%s|%s/%s#%d>*", stateEmoji(event.CurrentState), prURL, slackLinkText(event.Owner), slackLinkText(event.Repo), event.Number)
	if event.Title != "" {
		fmt.Fprintf(&b, " — %s", slackEscape(event.Title))
	}
	fmt.Fprintf(&b, "\n%s: `%s`", stateLabel(event.Kind), transition(event))

	for i, check := range event.FailedChecks {
		if i == maxChecksListed {
			fmt.Fprintf(&b, "\n… and %d more", len(event.FailedChecks)-maxChecksListed)
			break
		}
		if check.URL != "" {
			fmt.Fprintf(&b, "\n• <%s|%s>", check.URL, slackLinkText(check.Name))
		} else {
			fmt.Fprintf(&b, "\n• %s", slackEscape(check.Name))
		}
	}

	return map[string]string{"text": b.String()}
}

type discordEmbed struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Color       int    `json:"color"`
	Timestamp   string `json:"timestamp,omitempty"`
}

func discordPayload(event *StatusChangeEvent) map[string]interface{} {
	title := fmt.Sprintf("%s %s/%s#%d", stateEmoji(event.CurrentState), event.Owner, event.Repo, event.Number)
	if event.Title != "" {
		title += " — " + event.Title
	}

	var desc strings.Builder
	fmt.Fprintf(&desc, "%s: `%s`", stateLabel(event.Kind), transition(event))
	for i, check := range event.FailedChecks {
		if i == maxChecksListed {
			fmt.Fprintf(&desc, "\n… and %d more", len(event.FailedChecks)-maxChecksListed)
			break
		}
		name := strings.NewReplacer("[", "(", "]", ")").Replace(check.Name)
		if check.URL != "" {
			fmt.Fprintf(&desc, "\n• [%s](%s)", name, check.URL)
		} else {
			fmt.Fprintf(&desc, "\n• %s", check.Name)
		}
	}

	embed := discordEmbed{
		Title:       truncate(title, discordTitleLimit),
		URL:         forge.PRURL(event.Host, event.Owner, event.Repo, event.Number),
		Description: truncate(desc.String(), discordDescriptionMax),
		Color:       stateColor(event.CurrentState),
	}
	if !event.Timestamp.IsZero() {
		embed.Timestamp = event.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
	}

	return map[string]interface{}{"embeds": []discordEmbed{embed}}
}

// truncate shortens s to at most limit runes, ending with an ellipsis when cut.
func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit-1]) + "…"
}

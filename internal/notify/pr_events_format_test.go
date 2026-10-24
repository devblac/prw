package notify

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/devblac/prw/internal/github"
)

func prEvent(kind, prev, current string) *StatusChangeEvent {
	return &StatusChangeEvent{Kind: kind, Owner: "o", Repo: "r", Number: 1, Title: "Add feature", PreviousState: prev, CurrentState: current}
}

func TestPayloads_ReviewEvent(t *testing.T) {
	event := prEvent(EventKindReview, github.ReviewNone, github.ReviewApproved)

	if text := slackPayload(event)["text"]; !strings.HasPrefix(text, "✅ ") || !strings.Contains(text, "Review: `none → approved`") {
		t.Errorf("slack text = %q", text)
	}
	embed := discordPayload(event)["embeds"].([]discordEmbed)[0]
	if !strings.Contains(embed.Description, "Review: `none → approved`") || embed.Color != colorSuccess {
		t.Errorf("discord embed = %+v", embed)
	}
	data, _ := json.Marshal(genericPayload(event))
	if !strings.Contains(string(data), `"type":"pr_review_change"`) || !strings.Contains(string(data), `"current_state":"approved"`) {
		t.Errorf("generic payload = %s", data)
	}
}

func TestPayloads_MergeEvents(t *testing.T) {
	conflict := prEvent(EventKindMerge, github.MergeReady, github.MergeConflict)
	if text := slackPayload(conflict)["text"]; !strings.HasPrefix(text, "⚠️ ") || !strings.Contains(text, "Merge: `ready → conflict`") {
		t.Errorf("slack text = %q", text)
	}
	if c := discordPayload(conflict)["embeds"].([]discordEmbed)[0].Color; c != colorFailure {
		t.Errorf("conflict color = %#x", c)
	}
	ready := prEvent(EventKindMerge, github.MergeBlocked, github.MergeReady)
	if text := slackPayload(ready)["text"]; !strings.HasPrefix(text, "🚀 ") {
		t.Errorf("slack text = %q", text)
	}
	if data, _ := json.Marshal(genericPayload(ready)); !strings.Contains(string(data), `"type":"pr_merge_state_change"`) {
		t.Errorf("generic payload = %s", data)
	}
}

func TestPayloads_CIEventsKeepTheirType(t *testing.T) {
	event := prEvent(EventKindCI, "pending", "success")
	if data, _ := json.Marshal(genericPayload(event)); !strings.Contains(string(data), `"type":"pr_status_change"`) {
		t.Errorf("generic payload = %s", data)
	}
	if text := slackPayload(event)["text"]; !strings.Contains(text, "Status: `pending → success`") {
		t.Errorf("slack text = %q", text)
	}
}

func TestNativeTitle_NamesTheKind(t *testing.T) {
	var seen string
	old := runNativeCommand
	runNativeCommand = func(cmd *exec.Cmd) error {
		seen = strings.Join(append(cmd.Args, cmd.Env...), "\n") // title is an arg or env var
		return nil
	}
	t.Cleanup(func() { runNativeCommand = old })

	n := &NativeNotifier{enabled: true}
	if err := n.Notify(prEvent(EventKindReview, github.ReviewNone, github.ReviewChangesRequested)); err != nil {
		t.Fatal(err)
	}
	if seen != "" && !strings.Contains(seen, "PR Review Change: o/r#1") {
		t.Errorf("native notification doesn't name the review:\n%s", seen)
	}
}

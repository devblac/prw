package notify

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain keeps the test suite from popping real desktop notifications: every
// NativeNotifier.Notify call in this package runs a no-op instead of the OS command.
func TestMain(m *testing.M) {
	runNativeCommand = func(cmd *exec.Cmd) error { return nil }
	os.Exit(m.Run())
}

const hostileText = `x" ' <b> & $(Remove-Item -Recurse C:\) ` + "`$(calc)" + ` ; "; do shell script "rm -rf ~" --`

func envValue(cmd *exec.Cmd, key string) (string, bool) {
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, key+"=") {
			return strings.TrimPrefix(kv, key+"="), true
		}
	}
	return "", false
}

func TestNativeCommand_WindowsKeepsTextOutOfScript(t *testing.T) {
	cmd := nativeCommand("windows", "title "+hostileText, "message\n"+hostileText)
	if cmd == nil {
		t.Fatal("expected a command for windows")
	}

	script := cmd.Args[len(cmd.Args)-1]
	if strings.Contains(script, "Remove-Item") || strings.Contains(script, "calc") || strings.Contains(script, "shell script") {
		t.Errorf("notification text must never be interpolated into the PowerShell script:\n%s", script)
	}
	for _, want := range []string{"$env:" + envNotifyTitle, "$env:" + envNotifyMessage, "SecurityElement]::Escape", "LoadXml"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}

	if got, ok := envValue(cmd, envNotifyTitle); !ok || got != "title "+hostileText {
		t.Errorf("title env var = %q (present=%v)", got, ok)
	}
	if got, ok := envValue(cmd, envNotifyMessage); !ok || got != "message\n"+hostileText {
		t.Errorf("message env var = %q (present=%v)", got, ok)
	}

	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "-NoProfile") || !strings.Contains(joined, "-NonInteractive") {
		t.Errorf("expected -NoProfile -NonInteractive, got %v", cmd.Args[:len(cmd.Args)-1])
	}
}

func TestNativeCommand_WindowsStripsNUL(t *testing.T) {
	cmd := nativeCommand("windows", "a\x00b", "c\x00d")
	if got, _ := envValue(cmd, envNotifyTitle); got != "ab" {
		t.Errorf("title = %q, want NUL stripped", got)
	}
	if got, _ := envValue(cmd, envNotifyMessage); got != "cd" {
		t.Errorf("message = %q, want NUL stripped", got)
	}
}

func TestNativeCommand_LinuxUsesArgumentsAfterDoubleDash(t *testing.T) {
	cmd := nativeCommand("linux", "-t evil", "--hint=x")
	want := []string{"notify-send", "--", "-t evil", "--hint=x"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}
}

func TestNativeCommand_DarwinEscapesAppleScript(t *testing.T) {
	cmd := nativeCommand("darwin", `say "hi"`, "a\\b\nc")
	if len(cmd.Args) != 3 || cmd.Args[1] != "-e" {
		t.Fatalf("unexpected args: %q", cmd.Args)
	}
	want := `display notification "a\\b\nc" with title "say \"hi\""`
	if cmd.Args[2] != want {
		t.Errorf("script = %s, want %s", cmd.Args[2], want)
	}
}

func TestNativeCommand_UnsupportedPlatform(t *testing.T) {
	if cmd := nativeCommand("plan9", "t", "m"); cmd != nil {
		t.Errorf("expected nil command for unsupported platform, got %v", cmd.Args)
	}
}

func TestNativeNotifier_RunsCommandAndReportsFailure(t *testing.T) {
	old := runNativeCommand
	defer func() { runNativeCommand = old }()

	var ran *exec.Cmd
	runNativeCommand = func(cmd *exec.Cmd) error {
		ran = cmd
		return nil
	}

	event := &StatusChangeEvent{Owner: "o", Repo: "r", Number: 7, Title: "T", PreviousState: "pending", CurrentState: "success", Timestamp: time.Now()}
	n := &NativeNotifier{enabled: true}
	if err := n.Notify(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran == nil {
		t.Skip("native notifications are not supported on this platform")
	}

	runNativeCommand = func(cmd *exec.Cmd) error { return os.ErrNotExist }
	if err := n.Notify(event); err == nil || !strings.Contains(err.Error(), "native notification failed") {
		t.Errorf("expected wrapped failure, got %v", err)
	}

	if err := (&NativeNotifier{enabled: false}).Notify(event); err != nil {
		t.Errorf("disabled notifier must be a silent no-op, got %v", err)
	}
}

package notify

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	envNotifyTitle   = "PRW_NOTIFY_TITLE"
	envNotifyMessage = "PRW_NOTIFY_MESSAGE"

	// windowsToastScript reads the notification text from environment variables
	// instead of having it interpolated into the script, so PR titles (which anyone
	// with write access to a repo controls) can never be executed as PowerShell.
	// It borrows Windows PowerShell's AppUserModelID, as unregistered ids do not
	// show toasts on Windows 10/11.
	windowsToastScript = `$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$title = [System.Security.SecurityElement]::Escape($env:` + envNotifyTitle + `)
$message = [System.Security.SecurityElement]::Escape($env:` + envNotifyMessage + `)
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml("<toast><visual><binding template='ToastText02'><text id='1'>$title</text><text id='2'>$message</text></binding></visual></toast>")
$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
$appId = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)`
)

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

// runNativeCommand executes the platform notification command. Tests replace it so
// they do not pop real notifications.
var runNativeCommand = func(cmd *exec.Cmd) error {
	return cmd.Run()
}

// Notify sends a native system notification.
func (n *NativeNotifier) Notify(event *StatusChangeEvent) error {
	if !n.enabled {
		// Silently skip if not supported - this is expected on unsupported platforms
		return nil
	}

	title := fmt.Sprintf("PR %s Change: %s/%s#%d", stateLabel(event.Kind), event.Owner, event.Repo, event.Number)
	message := fmt.Sprintf("%s → %s", event.PreviousState, event.CurrentState)
	if event.Title != "" {
		message = fmt.Sprintf("%s\n%s", event.Title, message)
	}

	cmd := nativeCommand(runtime.GOOS, title, message)
	if cmd == nil {
		// Unsupported platform - silently skip
		return nil
	}

	if err := runNativeCommand(cmd); err != nil {
		// Log but don't fail - native notifications are optional
		return fmt.Errorf("native notification failed (tool may be missing): %w", err)
	}

	return nil
}

// nativeCommand builds the command that shows a notification on the given OS, or nil
// when the OS is unsupported. The text is passed as an argument or environment
// variable, never spliced into a script, except for AppleScript which is escaped.
func nativeCommand(goos, title, message string) *exec.Cmd {
	switch goos {
	case "darwin":
		// macOS: use osascript to display notification
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, escapeAppleScriptString(message), escapeAppleScriptString(title))
		return exec.Command("osascript", "-e", script)
	case "linux":
		// Linux: use notify-send (requires libnotify-bin)
		return exec.Command("notify-send", "--", title, message)
	case "windows":
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", windowsToastScript)
		cmd.Env = append(os.Environ(),
			envNotifyTitle+"="+stripNUL(title),
			envNotifyMessage+"="+stripNUL(message),
		)
		return cmd
	default:
		return nil
	}
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

// stripNUL removes NUL bytes, which cannot be represented in an environment block.
func stripNUL(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}

// escapeAppleScriptString escapes special characters for AppleScript strings.
func escapeAppleScriptString(s string) string {
	// Replace quotes and backslashes
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

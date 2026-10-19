package delivery

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const alertTitle = "dispatch"

// Alerts shows the user a notification.
type Alerts interface {
	Show(ctx context.Context, text string) error
}

// NotificationCenter is the Alerts that shows macOS notifications through
// osascript.
type NotificationCenter struct{ bin string }

// NewNotificationCenter returns Alerts that run bin.
func NewNotificationCenter(bin string) *NotificationCenter { return &NotificationCenter{bin: bin} }

// Show passes the text as a script argument, so a ticket title with quotes
// can't break the script. The "--" keeps text such as "-e" from being read as
// an osascript option.
func (n *NotificationCenter) Show(ctx context.Context, text string) error {
	out, err := exec.CommandContext(ctx, n.bin,
		"-e", "on run argv",
		"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
		"-e", "end run",
		"--", text, alertTitle).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// HerdrNotifications is the Alerts that shows herdr notifications, which reach
// the user over SSH where a desktop notifier can't.
type HerdrNotifications struct{ bin string }

// NewHerdrNotifications returns Alerts that run bin.
func NewHerdrNotifications(bin string) *HerdrNotifications { return &HerdrNotifications{bin: bin} }

// Show passes the text as the separate argument after --body. herdr rejects
// the "--body=text" form but takes a value that starts with a dash.
func (h *HerdrNotifications) Show(ctx context.Context, text string) error {
	out, err := exec.CommandContext(ctx, h.bin,
		"notification", "show", alertTitle, "--body", text, "--sound", "request").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// NewAlerts returns the alerts for the operating system named by goos:
// macOS notifications on darwin and herdr notifications everywhere else.
func NewAlerts(goos string) Alerts {
	if goos == "darwin" {
		return NewNotificationCenter("osascript")
	}
	return NewHerdrNotifications("herdr")
}

// alert shows text, naming the failure for the caller to report.
func alert(ctx context.Context, alerts Alerts, text string) error {
	if err := alerts.Show(ctx, text); err != nil {
		return fmt.Errorf("while notifying: %w", err)
	}
	return nil
}

var (
	_ Alerts = (*NotificationCenter)(nil)
	_ Alerts = (*HerdrNotifications)(nil)
)

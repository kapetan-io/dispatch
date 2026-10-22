package notify

import (
	"context"
	"fmt"
	"strings"

	"github.com/kapetan-io/dispatch/internal/environment"
)

const alertTitle = "dispatch"

// Alerts shows the user a notification.
type Alerts interface {
	Show(ctx context.Context, text string) error
}

// NotificationCenter is the Alerts that shows macOS notifications through
// osascript.
type NotificationCenter struct {
	bin string
	env environment.Environment
}

// NewNotificationCenter returns Alerts that run bin.
func NewNotificationCenter(bin string) *NotificationCenter {
	return &NotificationCenter{bin: bin}
}

// WithEnv runs osascript, and finds it, with env instead of the process's environment.
func (n *NotificationCenter) WithEnv(env environment.Environment) *NotificationCenter {
	n.env = env
	return n
}

// Show passes the text as a script argument, so a ticket title with quotes
// can't break the script. The "--" keeps text such as "-e" from being read as
// an osascript option.
func (n *NotificationCenter) Show(ctx context.Context, text string) error {
	out, err := n.env.Command(ctx, n.bin,
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
type HerdrNotifications struct {
	bin string
	env environment.Environment
}

// NewHerdrNotifications returns Alerts that run bin.
func NewHerdrNotifications(bin string) *HerdrNotifications {
	return &HerdrNotifications{bin: bin}
}

// WithEnv runs herdr, and finds it, with env instead of the process's environment.
func (h *HerdrNotifications) WithEnv(env environment.Environment) *HerdrNotifications {
	h.env = env
	return h
}

// Show passes the text as the separate argument after --body. herdr rejects
// the "--body=text" form but takes a value that starts with a dash.
func (h *HerdrNotifications) Show(ctx context.Context, text string) error {
	out, err := h.env.Command(ctx, h.bin,
		"notification", "show", alertTitle, "--body", text, "--sound", "request").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// NewAlerts returns the alerts for the operating system named by goos:
// macOS notifications on darwin and herdr notifications everywhere else.
func NewAlerts(goos string, env environment.Environment) Alerts {
	if goos == "darwin" {
		return NewNotificationCenter("osascript").WithEnv(env)
	}
	return NewHerdrNotifications("herdr").WithEnv(env)
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

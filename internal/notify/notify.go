// Package notify raises macOS notifications, the dispatcher's only way to
// reach you when a worker needs an answer.
package notify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Send shows a notification through osascript. The text goes in as script
// arguments, so a ticket title with quotes can't break the script.
func Send(ctx context.Context, bin, title, message string) error {
	out, err := exec.CommandContext(ctx, bin,
		"-e", "on run argv",
		"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
		"-e", "end run",
		message, title).CombinedOutput()
	if err != nil {
		return fmt.Errorf("while notifying: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

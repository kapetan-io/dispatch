package delivery

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const alertTitle = "dispatch"

// alert shows a macOS notification through osascript. The text goes in as
// script arguments, so a ticket title with quotes can't break the script.
func alert(ctx context.Context, bin, message string) error {
	out, err := exec.CommandContext(ctx, bin,
		"-e", "on run argv",
		"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
		"-e", "end run",
		message, alertTitle).CombinedOutput()
	if err != nil {
		return fmt.Errorf("while notifying: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

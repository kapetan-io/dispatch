// Package launcher runs bin/dispatch-launch, which leases a tree, opens a herdr
// workspace and starts a Claude build worker for one ticket.
package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Launcher statuses that the dispatcher treats specially.
const (
	StatusOK             = "ok"
	StatusAlreadyRunning = "already_running"
	StatusPoolExhausted  = "pool_exhausted"
)

// Result is the one JSON object the launcher prints.
type Result struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Failed reports whether the launch counts toward the escalation limit. A
// full pool is waiting, not failing, and an existing worker is success.
func (r Result) Failed() bool {
	switch r.Status {
	case StatusOK, StatusAlreadyRunning, StatusPoolExhausted:
		return false
	}
	return true
}

// Launch runs `<bin> <ticket> <branch> --model <model> --no-focus` from repoDir.
func Launch(ctx context.Context, bin, repoDir, ticket, branch, model string) Result {
	cmd := exec.CommandContext(ctx, bin, ticket, branch, "--model", model, "--no-focus")
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	var r Result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil || r.Status == "" {
		detail := strings.TrimSpace(stderr.String())
		if runErr != nil {
			detail = fmt.Sprintf("%v: %s", runErr, detail)
		}
		return Result{Status: "launcher_error", Detail: detail}
	}
	return r
}

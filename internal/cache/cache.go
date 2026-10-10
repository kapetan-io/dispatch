// Package cache holds the per-ticket counters a tick carries to the next one.
// Deleting the cache loses little: counters reset, a last-seen status may
// repeat one notification, a comment posted meanwhile gets no nudge because
// the first tick only re-records the newest comment time, and an escalation
// comment Linear has not yet accepted is never posted.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one ticket's state between ticks.
type Entry struct {
	LastStatus     string `json:"last_status"`
	LaunchFailures int    `json:"launch_failures"`
	// Nudges counts nudges sent, the resume nudge included, since the ticket
	// last changed status or was escalated. Only In Review PR nudges read it;
	// stall nudges are counted in StallNudges.
	Nudges int `json:"nudges"`
	// StallNudges counts stall nudges since the ticket last changed status.
	StallNudges int `json:"stall_nudges,omitempty"`
	// IdleSince is when the dispatcher first saw the In Progress worker idle,
	// and stands in for the Stop time when the worker has no usable Stop record.
	IdleSince time.Time `json:"idle_since,omitzero"`
	// NudgedStop is the Stop the last stall or resume nudge answered, so each
	// Stop is nudged once.
	NudgedStop time.Time `json:"nudged_stop,omitzero"`
	// NudgedAt is when a stall nudge for NudgedStop went out. It is zero after
	// a resume nudge, which never raises the unresponsive-worker notification.
	NudgedAt time.Time `json:"nudged_at,omitzero"`
	// StallNotifyPending marks a stall-count notification that failed to show,
	// so the next tick sends it again.
	StallNotifyPending bool `json:"stall_notify_pending,omitempty"`
	// NudgeNotified marks that the user was told the worker ignored the nudge
	// for NudgedStop.
	NudgeNotified bool `json:"nudge_notified,omitempty"`
	// PRHead is the SHA of the In Review ticket's PR head when last read. A
	// different head means the worker pushed, which resets Nudges.
	PRHead string `json:"pr_head,omitempty"`
	// Relaunches counts workers relaunched for an In Review PR since the ticket
	// last changed status or the PR was pushed. It is separate from Nudges so a
	// relaunch does not use up one of the PR nudges.
	Relaunches int `json:"relaunches,omitempty"`
	// ResumePending marks a ticket moved from Needs Human back to In Progress
	// whose worker has not yet been sent the resume nudge.
	ResumePending bool `json:"resume_pending,omitempty"`
	// NotifyPending marks a Needs Human notification that failed to show and
	// is retried each tick until it does.
	NotifyPending bool `json:"notify_pending,omitempty"`
	// ReadyHead is the PR head the user was last told was ready for review. A
	// push moves the head, so a PR that is ready again notifies again.
	ReadyHead string `json:"ready_head,omitempty"`
	// MergeFailedHead is the PR head whose auto-merge failed. The user was told
	// once, and inReviewIdle does not retry that head. A push moves it.
	MergeFailedHead string `json:"merge_failed_head,omitempty"`
	// BlockedTicks counts consecutive ticks the ticket's worker sat blocked.
	BlockedTicks int `json:"blocked_ticks"`
	// PendingComment is an escalation comment Linear has not accepted yet.
	// The ticket is already in Needs Human, and each tick retries it by
	// TicketID, whatever state the ticket has moved to since.
	PendingComment string `json:"pending_comment,omitempty"`
	// TicketID is Linear's internal id for the ticket a pending comment goes on.
	TicketID string `json:"ticket_id,omitempty"`
	// CommentSeen is the creation time of the newest comment dispatch has
	// accounted for, "0" when the ticket had none. Empty means the ticket was
	// never seen, so its first sighting records a time without nudging. It
	// stays put while a nudge is owed or the worker can't take one yet.
	CommentSeen string `json:"comment_seen,omitempty"`
	// PRActivitySeen is the time of the newest PR review, conversation comment or open review thread comment already put
	// to the worker, or "0" for a ticket that entered In Review owing it all of
	// the PR's activity. Empty means no baseline yet, and ActivityBaseline says
	// what that means. It survives a trip out of In Review, so the worker is not
	// nudged again about activity it handled.
	PRActivitySeen string `json:"pr_activity_seen,omitempty"`
	// TeardownWarned marks a finished ticket whose dirty tree already got its
	// one comment. It is dropped when the ticket stops being a finished ticket
	// that holds a lease.
	TeardownWarned bool `json:"teardown_warned,omitempty"`
}

// NudgesAt is the nudge count that still stands for an In Review ticket now in
// status with its PR at head. A status change or a push starts the count over.
// Plan and the tick's settle step both use it, so they can't disagree.
func (e Entry) NudgesAt(status, head string) int {
	if e.LastStatus != status || e.PRHead != head {
		return 0
	}
	return e.Nudges
}

// ActivityBaseline is the PR activity time before which nothing is owed to the
// worker of a ticket whose status is now status, "" when the first PR read
// should record one. A ticket new to the cache owes its worker whatever the PR
// already has, and an In Review ticket without a time predates the field.
func (e Entry) ActivityBaseline(status, inReview string) string {
	if e.PRActivitySeen == "" && e.LastStatus != inReview && status == inReview {
		return "0"
	}
	return e.PRActivitySeen
}

// RelaunchesAt is NudgesAt for the relaunch count.
func (e Entry) RelaunchesAt(status, head string) int {
	if e.LastStatus != status || e.PRHead != head {
		return 0
	}
	return e.Relaunches
}

// Cache maps a ticket identifier such as ENG-123 to its entry.
type Cache map[string]Entry

// Load reads the cache at path. A missing file is an empty cache.
func Load(path string) (Cache, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Cache{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("while reading cache %s: %w", path, err)
	}
	c := Cache{}
	if err := json.Unmarshal(b, &c); err != nil {
		// A corrupt cache is treated as empty, which the design allows.
		return Cache{}, nil
	}
	return c, nil
}

// Save writes the cache atomically, so a crash never leaves half a file.
func (c Cache) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cache-*.json")
	if err != nil {
		return fmt.Errorf("while writing cache: %w", err)
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing cache: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}

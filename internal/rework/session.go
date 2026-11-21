package rework

import (
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/transcript"
)

const (
	// NotProven is the session of a PR no transcript on disk proves.
	NotProven = "not proven"
	// NotOnDisk is the session of a PR that merged before the oldest transcript record on disk.
	NotOnDisk = "not on disk"
)

// sessionOf names the session that made pr, as an 8-character session id with the subagent when
// one made it and how it was proven, or NotProven or NotOnDisk. The proof is a successful `gh pr
// create` whose result holds the PR's URL. Without one, a branch that only one session ran on is
// the fallback. A commit SHA that a session mentions proves nothing, since any session can read one.
func sessionOf(s transcript.Sessions, pr pullrequest.MergedChange) string {
	if !s.Oldest.IsZero() && pr.MergedAt.Before(s.Oldest) {
		return NotOnDisk
	}
	if creators := s.PRCreators[pr.URL]; len(creators) > 0 {
		var names []string
		for who := range creators {
			name := cut(who.Session, 8)
			if who.Agent != "" {
				name += "/agent-" + who.Agent
			}
			names = append(names, name)
		}
		slices.Sort(names)
		return strings.Join(names, ", ") + " (gh pr create)"
	}
	if sessions := s.BranchSessions[pr.Branch]; pr.Branch != "" && len(sessions) == 1 {
		for session := range sessions {
			return cut(session, 8) + " (branch)"
		}
	}
	return NotProven
}

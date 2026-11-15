package linear

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Filing is the part of the config that decides how a ticket is filed so dispatch manages it.
type Filing struct {
	Teams          []string
	AILabel        string
	RepoLabelGroup string
	// Paused are the statuses where a ticket waits until the user starts it.
	Paused []string
	// Todo is the status a new ticket waits in unless the request names another, which must be paused.
	Todo string
}

// Request is one ticket to file.
type Request struct {
	// Team is a team key. It may be empty when Teams has exactly one.
	Team        string
	Repo        string
	Title       string
	Description string
	// Status is a paused status. It defaults to Todo.
	Status     string
	Labels     []string
	Attributes map[string]string
}

// File files a ticket that dispatch manages: on one of the configured teams, with the AI label and
// the repo label, assigned to the key's owner, and waiting in a paused status.
func File(ctx context.Context, ws Workspace, f Filing, r Request) (string, error) {
	team := r.Team
	switch {
	case team == "" && len(f.Teams) == 1:
		team = f.Teams[0]
	case team == "":
		return "", fmt.Errorf("create linear needs --team, one of %s", strings.Join(f.Teams, ", "))
	case !slices.Contains(f.Teams, team):
		return "", fmt.Errorf("team %s is not one dispatch reads; the configured teams are %s", team, strings.Join(f.Teams, ", "))
	}
	status := r.Status
	if status == "" {
		status = f.Todo
	}
	i := slices.IndexFunc(f.Paused, func(s string) bool { return strings.EqualFold(s, status) })
	if i < 0 {
		return "", fmt.Errorf("status %q is not a paused status, and a new ticket must wait for you to start it; the paused statuses are %s", status, strings.Join(f.Paused, ", "))
	}
	status = f.Paused[i]
	description, err := WithAttributes(r.Description, r.Attributes, nil)
	if err != nil {
		return "", err
	}
	labels := []LabelRef{{Name: f.AILabel}, {Name: r.Repo, Group: f.RepoLabelGroup}}
	for _, name := range r.Labels {
		labels = append(labels, LabelRef{Name: name})
	}
	return ws.CreateTicket(ctx, NewTicket{Team: team, Title: r.Title, Description: description, Status: status, Labels: labels})
}

// EditAttributes sets and removes attributes in a managed ticket's description, leaving the rest of
// the description as it was. It re-reads the description right before writing, so a person's edit
// saved earlier is kept and only one saved during the write is lost.
func EditAttributes(ctx context.Context, ws Workspace, f Filing, identifier string, set map[string]string, unset []string) error {
	d, err := ws.ManagedDescription(ctx, f.AILabel, f.Teams, identifier)
	if err != nil {
		return err
	}
	next, err := WithAttributes(d.Description, set, unset)
	if err != nil {
		return err
	}
	if next == d.Description {
		return nil
	}
	return ws.SetDescription(ctx, d.ID, next)
}

// WithAttributes returns description with the pairs in set written to its "## Attributes" section
// and the keys in unset removed from it, as AttributesIn reads them back. A value replaces the
// value of its key where the key already is, a new key goes at the end of the section, and the
// section is added at the end of the description when it has none. Text outside the section is
// untouched.
func WithAttributes(description string, set map[string]string, unset []string) (string, error) {
	for k, v := range set {
		if k == "" || strings.ContainsAny(k, ":\n") || strings.TrimSpace(k) != k || strings.HasPrefix(k, "#") {
			return "", fmt.Errorf("attribute key %q must be a plain word with no colon or newline", k)
		}
		if strings.Contains(v, "\n") {
			return "", fmt.Errorf("attribute %s has a value with a newline", k)
		}
	}
	for _, k := range unset {
		if hasKey(set, k) {
			return "", fmt.Errorf("attribute %s is both set and unset", k)
		}
	}
	if len(set) == 0 && len(unset) == 0 {
		return description, nil
	}
	lines := strings.Split(description, "\n")
	if sections := slices.IndexFunc(lines, isAttributesHeading); sections >= 0 && slices.IndexFunc(lines[sections+1:], isAttributesHeading) >= 0 {
		return "", errors.New("the description has two ## Attributes sections, which dispatch won't guess between; merge them first")
	}
	start, end := -1, len(lines)
	for i, line := range lines {
		level, title := heading(line)
		switch {
		case start < 0 && level == 2 && strings.EqualFold(title, "attributes"):
			start = i
		case start >= 0 && level > 0:
			end = i
		}
		if start >= 0 && end != len(lines) {
			break
		}
	}
	var body []string
	if start >= 0 {
		body = slices.Clone(lines[start+1 : end])
		for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
			body = body[:len(body)-1]
		}
	}
	written := map[string]bool{}
	var kept []string
	for _, line := range body {
		key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		key = strings.TrimSpace(key)
		switch {
		case !ok || key == "":
			kept = append(kept, line)
		case slices.Contains(unset, key):
		case hasKey(set, key):
			written[key] = true
			kept = append(kept, key+": "+set[key])
		default:
			kept = append(kept, line)
		}
	}
	var fresh []string
	for k := range set {
		if !written[k] {
			fresh = append(fresh, k)
		}
	}
	sort.Strings(fresh)
	for _, k := range fresh {
		kept = append(kept, k+": "+set[k])
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	for len(kept) > 0 && strings.TrimSpace(kept[0]) == "" {
		kept = kept[1:]
	}
	switch {
	case start < 0 && len(kept) == 0:
		return description, nil
	case start < 0:
		out := strings.TrimRight(description, "\n")
		if out != "" {
			out += "\n\n"
		}
		return out + "## Attributes\n" + strings.Join(kept, "\n") + "\n", nil
	case len(kept) == 0:
		out := append(slices.Clone(lines[:start]), lines[end:]...)
		return strings.TrimRight(strings.Join(out, "\n"), "\n") + trailer(description), nil
	}
	out := append(slices.Clone(lines[:start+1]), kept...)
	if end < len(lines) || strings.HasSuffix(description, "\n") {
		out = append(out, "")
	}
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

func isAttributesHeading(line string) bool {
	level, title := heading(line)
	return level == 2 && strings.EqualFold(title, "attributes")
}

func trailer(description string) string {
	if strings.HasSuffix(description, "\n") {
		return "\n"
	}
	return ""
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

// heading is the level and title of a markdown heading line, and 0 for any other line. A heading
// is hashes and a space, as AttributesIn reads it.
func heading(line string) (int, string) {
	line = strings.TrimSpace(line)
	hashes := len(line) - len(strings.TrimLeft(line, "#"))
	if hashes == 0 || hashes >= len(line) || line[hashes] != ' ' {
		return 0, ""
	}
	return hashes, strings.TrimSpace(line[hashes:])
}

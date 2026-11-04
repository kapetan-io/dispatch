package herdr

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxRexLabel is the most characters Rex keeps in a session, window or block label.
const maxRexLabel = 128

// workerLabel is what Rex keeps of a worker, in the label of its block. Rex has no key-value
// labels and never returns a program's arguments, and a block's label is the one place that
// lives as long as the worker and survives a Rex restart.
type workerLabel struct {
	// Session is the harness session the worker was launched on, "" when the harness can't choose one.
	Session string
	// Instance is the start of the id of the Rex server process that created the block.
	Instance string
	// Tree is the path as the launcher spelled it.
	Tree string
}

const (
	workerLabelMark = "dispatch"
	noSession       = "-"
)

func (l workerLabel) String() string {
	session := l.Session
	if session == "" {
		session = noSession
	}
	return workerLabelMark + " " + session + " " + l.Instance + " " + l.Tree
}

// parseWorkerLabel reads a block label. A label that is not a worker's belongs to the user.
func parseWorkerLabel(label string) (workerLabel, bool) {
	parts := strings.SplitN(label, " ", 4)
	if len(parts) != 4 || parts[0] != workerLabelMark || parts[1] == "" || parts[2] == "" || !strings.HasPrefix(parts[3], "/") {
		return workerLabel{}, false
	}
	l := workerLabel{Session: parts[1], Instance: parts[2], Tree: parts[3]}
	if l.Session == noSession {
		l.Session = ""
	}
	return l, true
}

// checkLabel fails when the worker label would be longer than Rex keeps, naming the longest tree it holds.
func checkLabel(l workerLabel) error {
	if len(l.String()) <= maxRexLabel {
		return nil
	}
	room := maxRexLabel - (len(l.String()) - len(l.Tree))
	return fmt.Errorf("the tree %s is %d characters, and the Rex worker label holds trees of at most %d", l.Tree, len(l.Tree), room)
}

// SplitWindowLabel splits a Rex window label into the emoji and the tab label. The first word is
// the emoji when it holds no letter or digit. Rex has one label per window, so the workspace's
// emoji and the tab's `<ticket> <title>` share it.
func SplitWindowLabel(label string) (emoji, tab string) {
	first, rest, _ := strings.Cut(label, " ")
	if first == "" || strings.ContainsFunc(first, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
		return "", label
	}
	return first, strings.TrimLeft(rest, " ")
}

// JoinWindowLabel is the window label for an emoji and a tab label. It is the tab label alone when there is no emoji.
func JoinWindowLabel(emoji, tab string) string {
	if emoji == "" {
		return tab
	}
	return WorkspaceLabel(emoji, tab)
}

// clipLabel cuts a window label to the 128 bytes Rex accepts, never inside a character.
func clipLabel(label string) string {
	if len(label) <= maxRexLabel {
		return label
	}
	cut := maxRexLabel
	for cut > 0 && !utf8.RuneStart(label[cut]) {
		cut--
	}
	return label[:cut]
}

// workspaceLabelOf is the workspace label a window label shows, `<emoji> <ticket>`, or the ticket alone without an emoji.
func workspaceLabelOf(window string) string {
	emoji, tab := SplitWindowLabel(window)
	ticket, _, _ := strings.Cut(tab, " ")
	if emoji == "" {
		return ticket
	}
	return WorkspaceLabel(emoji, ticket)
}

// rexWorkspaceID is the workspace id for a tree in the repo session labeled repo. A folder name
// holds no "/" and a tree starts with one, so the first "|/" splits the id.
func rexWorkspaceID(repo, tree string) string { return repo + "|" + tree }

func splitRexWorkspaceID(id string) (repo, tree string, ok bool) {
	repo, tree, ok = strings.Cut(id, "|/")
	return repo, "/" + tree, ok
}

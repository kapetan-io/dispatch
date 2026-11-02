package ticket

import "strings"

// Ticket is one managed ticket, whichever ticket system it came from.
type Ticket struct {
	// ID is Linear's internal id, used for mutations. A local ticket has none.
	ID string
	// Identifier is the human id, such as ENG-123. It is also the treehouse lease holder.
	Identifier string
	Title      string
	URL        string
	Branch     string
	Status     string
	Team       string
	Labels     []Label
	// HasChildren marks a parent ticket, which never gets a dispatched worker.
	HasChildren bool
	// LabelsTruncated means Labels is only the first page, so a repo or design
	// label may be missing from it.
	LabelsTruncated bool
	// LastComment is the creation time of the ticket's newest comment, or empty
	// when it has none. It is RFC 3339 and sorts as text. Only ManagedTickets
	// fills it in. It feeds the rules' lastHumanComment fact.
	LastComment string
	// Mine means the ticket is assigned to the key's owner. Only ParentChildren
	// sets it, because ManagedTickets returns nothing else.
	Mine bool
	// CreatedAt is Linear's RFC 3339 creation time, which sorts as text.
	// ManagedTickets and ParentChildren fill it in.
	CreatedAt string
	// Blockers are the tickets that block this one. ManagedTickets and
	// ParentChildren fill them in.
	Blockers []Blocker
	// BlockersTruncated means the ticket has more than 10 relations of any kind,
	// so an unfinished blocker may be missing from Blockers.
	BlockersTruncated bool
	// Repo is a local ticket's own [repos.<label>] table. A Linear ticket names its repo in a label.
	Repo string `json:",omitempty"`
	// Attributes are a local ticket's key-value pairs.
	Attributes map[string]string `json:",omitempty"`
	// StatusAt is when a local ticket entered its current status, in CreatedAt's form.
	StatusAt string `json:",omitempty"`
}

// Blocker is a ticket that blocks another and the status it is in.
type Blocker struct {
	Identifier string
	Status     string
	// Abandoned means the blocker's status is of Linear's canceled type, which
	// covers Canceled and Duplicate. It never becomes Done.
	Abandoned bool
}

// Label is a Linear label and the label group it belongs to, if any.
type Label struct {
	Name  string
	Group string
}

// HasLabel reports whether the ticket carries a label with this name. Linear
// keeps label names unique ignoring case, so AI and ai are the same label.
func (t Ticket) HasLabel(name string) bool {
	for _, l := range t.Labels {
		if strings.EqualFold(l.Name, name) {
			return true
		}
	}
	return false
}

// LabelIn returns the ticket's label in the given group, such as its repo.
func (t Ticket) LabelIn(group string) (string, bool) {
	for _, l := range t.Labels {
		if strings.EqualFold(l.Group, group) {
			return l.Name, true
		}
	}
	return "", false
}

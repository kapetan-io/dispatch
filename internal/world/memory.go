package world

import (
	"bytes"
	"encoding/json"
)

// Remembered is a fact's value as canonical JSON, with when dispatch first saw
// that value.
type Remembered struct {
	Value json.RawMessage `json:"value"`
	Since int64           `json:"since"`
}

// Same reports whether v is the remembered value.
func (r Remembered) Same(v []byte) bool { return bytes.Equal(r.Value, v) }

// Pool is what dispatch remembers about one repo's pool between reads.
type Pool struct {
	// Returns counts the trees that came back to the pool, whoever returned them.
	Returns int `json:"returns"`
	// Leases maps each tree path seen leased to its holder.
	Leases map[string]string `json:"leases,omitempty"`
}

// Memory is the part of the world that exists only because dispatch watched it
// over time: when each value was first seen, a ticket's previous status, how long
// a ticket has waited on blockers and how many trees came back to a pool. History
// carries it in each segment's snapshot, so a restart keeps every wait and every
// once key that depends on one.
type Memory struct {
	// Facts maps "kind/id/field" to the value dispatch last saw there.
	Facts          map[string]Remembered `json:"facts,omitempty"`
	PreviousStatus map[string]string     `json:"previousStatus,omitempty"`
	// Waiting maps a ticket to when its current stretch of waiting on blockers began.
	Waiting map[string]int64 `json:"waiting,omitempty"`
	Pools   map[string]Pool  `json:"pools,omitempty"`
	// Children maps each parent to the children its last good read listed, so a read
	// that fails to list them can keep the memory of those children.
	Children map[string][]string `json:"children,omitempty"`
}

// NewMemory returns an empty memory.
func NewMemory() *Memory {
	return &Memory{Facts: map[string]Remembered{}, PreviousStatus: map[string]string{}, Waiting: map[string]int64{}, Pools: map[string]Pool{}, Children: map[string][]string{}}
}

// Ready fills the maps a decoded memory may lack.
func (m *Memory) Ready() *Memory {
	if m.Facts == nil {
		m.Facts = map[string]Remembered{}
	}
	if m.PreviousStatus == nil {
		m.PreviousStatus = map[string]string{}
	}
	if m.Waiting == nil {
		m.Waiting = map[string]int64{}
	}
	if m.Pools == nil {
		m.Pools = map[string]Pool{}
	}
	if m.Children == nil {
		m.Children = map[string][]string{}
	}
	return m
}

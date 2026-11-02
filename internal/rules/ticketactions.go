package rules

import (
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/grafana/sobek"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// Search is a search a then ran over the local tickets and what it found, which a replay can read instead
// of opening the database.
type Search struct {
	Rule    string         `json:"rule,omitempty"`
	Subject string         `json:"subject,omitempty"`
	Query   ticket.Query   `json:"query"`
	Found   []ticket.Found `json:"found,omitempty"`
	// Unknown is why the search got no answer.
	Unknown string `json:"unknown,omitempty"`
}

// The kinds a field of a draft or of changes can have. They are tags for ticketFields, not TypeScript types.
const (
	kindString    = "string"
	kindStrings   = "string[]"
	kindAttrs     = "Record<string, string>"
	kindPR        = "PR number"
	kindJob       = "job"
	kindClearable = "string | null"
)

var draftFields = map[string]string{
	"title": kindString, "repo": kindString, "description": kindString, "labels": kindStrings, "attributes": kindAttrs,
	"parent": kindString, "blockedBy": kindStrings, "startPR": kindPR, "status": kindJob,
}

var changeFields = map[string]string{
	"title": kindString, "description": kindString, "repo": kindString, "addLabels": kindStrings, "removeLabels": kindStrings,
	"setAttributes": kindAttrs, "unsetAttributes": kindStrings, "parent": kindClearable, "blockedBy": kindStrings, "startPR": kindPR,
}

// localSystem checks the system a create names. Only local tickets can be created, and only while they are on.
func (c *call) localSystem(method string, v sobek.Value) string {
	name, _ := v.Export().(string)
	if name != ticket.SystemLocal {
		c.throwError("%s files in the ticket system %q, and only \"local\" can be created", method, v.String())
	}
	if !c.state.Config.Plugins.Local.On {
		c.throwError("%s: local tickets are off. Add a [plugins.local] table to config.toml", method)
	}
	return name
}

// ticketFields reads the draft or the changes of a ticket action into plain values, throwing for a field
// that does not exist or has the wrong type. A null parent clears it, and is kept as "".
func (c *call) ticketFields(method, what string, v sobek.Value, fields map[string]string) map[string]any {
	obj, ok := v.(*sobek.Object)
	if !ok || obj.ClassName() != "Object" {
		c.throwError("%s needs %s to be an object", method, what)
	}
	out := map[string]any{}
	for _, key := range obj.Keys() {
		kind, known := fields[key]
		if !known {
			c.throwError("%s: %s has no field %q. It has %s", method, what, key, strings.Join(slices.Sorted(maps.Keys(fields)), ", "))
		}
		val := obj.Get(key)
		if sobek.IsUndefined(val) {
			continue
		}
		if sobek.IsNull(val) {
			if kind != kindClearable {
				c.throwError("%s: %s.%s cannot be null", method, what, key)
			}
			out[key] = ""
			continue
		}
		out[key] = c.fieldValue(method, what+"."+key, kind, val)
	}
	return out
}

func (c *call) fieldValue(method, name, kind string, v sobek.Value) any {
	switch kind {
	case kindString, kindClearable:
		s, ok := v.Export().(string)
		if !ok {
			c.throwError("%s: %s must be a string", method, name)
		}
		return s
	case kindJob:
		s, _ := v.Export().(string)
		if !slices.Contains(movableJobs, s) {
			c.throwError("%s: %s %q is not a status key. Use one of %s", method, name, v.String(), strings.Join(movableJobs, ", "))
		}
		return s
	case kindStrings:
		return c.stringsOf(method, name, v)
	case kindAttrs:
		return c.attributesOf(method, name, v)
	case kindPR:
		switch n := v.Export().(type) {
		case int64:
			return n
		case float64:
			if n == math.Trunc(n) && math.Abs(n) <= math.MaxInt32 {
				return int64(n)
			}
		}
		c.throwError("%s: %s must be a whole number a PR could have", method, name)
	}
	return nil
}

func (c *call) stringsOf(method, name string, v sobek.Value) []string {
	list, ok := v.Export().([]any)
	if !ok {
		c.throwError("%s: %s must be a list of strings", method, name)
	}
	out := make([]string, len(list))
	for i, item := range list {
		if out[i], ok = item.(string); !ok {
			c.throwError("%s: %s must be a list of strings", method, name)
		}
	}
	return out
}

func (c *call) attributesOf(method, name string, v sobek.Value) map[string]string {
	m, ok := v.Export().(map[string]any)
	if !ok {
		c.throwError("%s: %s must be an object of strings", method, name)
	}
	out := make(map[string]string, len(m))
	for k, item := range m {
		if out[k], ok = item.(string); !ok {
			c.throwError("%s: %s.%s must be a string", method, name, k)
		}
	}
	return out
}

// queryArg reads the filter a search was given.
func (c *call) queryArg(v sobek.Value) ticket.Query {
	var q ticket.Query
	if sobek.IsUndefined(v) || sobek.IsNull(v) {
		return q
	}
	obj, ok := v.(*sobek.Object)
	if !ok || obj.ClassName() != "Object" {
		c.throwError("ctx.ticket.search needs a filter object")
	}
	for _, key := range obj.Keys() {
		val := obj.Get(key)
		if sobek.IsUndefined(val) {
			continue
		}
		switch key {
		case "status":
			job := ticket.Job(val.String())
			if _, ok := val.Export().(string); !ok || !job.Known() {
				c.throwError("ctx.ticket.search: status %q is not a job. Use one of %s", val.String(), jobList())
			}
			q.Job = job
		case "labels":
			q.Labels = c.stringsOf("ctx.ticket.search", "labels", val)
		case "attributes":
			q.Attributes = c.attributesOf("ctx.ticket.search", "attributes", val)
		case "open":
			open, ok := val.Export().(bool)
			if !ok {
				c.throwError("ctx.ticket.search: open must be true or false")
			}
			q.Open = open
		default:
			c.throwError("ctx.ticket.search: the filter has no field %q. It has status, labels, attributes and open", key)
		}
	}
	return q
}

func jobList() string {
	names := make([]string, len(ticket.Jobs))
	for i, j := range ticket.Jobs {
		names[i] = string(j)
	}
	return strings.Join(names, ", ")
}

// search is ctx.ticket.search. A search the database cannot answer holds the rule, like any unknown read.
func (c *call) search(fc sobek.FunctionCall) sobek.Value {
	if c.phase != phaseThen {
		c.throwError("ctx.ticket.search can only be called in then. An if reads and never asks the database")
	}
	if !c.state.Config.Plugins.Local.On {
		c.throwError("ctx.ticket.search: local tickets are off. Add a [plugins.local] table to config.toml")
	}
	q := c.queryArg(fc.Argument(0))
	text, _ := json.Marshal(q)
	if c.state.Search == nil {
		c.unknownRead("ctx.ticket.search has no local tickets to search here")
	}
	found, err := c.state.Search.Search(q)
	if err != nil {
		c.searches = append(c.searches, Search{Query: q, Unknown: err.Error()})
		c.unknownRead(err.Error())
	}
	c.searches = append(c.searches, Search{Query: q, Found: found})
	c.read("ticket.search "+string(text), len(found))
	out := make([]any, len(found))
	for i, f := range found {
		o := c.rt.NewObject()
		_ = o.Set("id", f.ID)
		_ = o.Set("title", f.Title)
		_ = o.Set("status", f.Status)
		_ = o.Set("finished", f.Finished)
		labels := make([]any, len(f.Labels))
		for j, l := range f.Labels {
			labels[j] = l
		}
		_ = o.Set("labels", c.rt.NewArray(labels...))
		attrs := c.rt.NewObject()
		for k, v := range f.Attributes {
			_ = attrs.Set(k, v)
		}
		_ = o.Set("attributes", attrs)
		out[i] = o
	}
	return c.rt.NewArray(out...)
}

// Package startscreen prints what `dispatch run` is doing while it starts and what its first read
// found. In a terminal it prints a logo, color and emoji. Anywhere else, such as a log file, it
// prints the same facts as plain text, so the log stays readable and greppable.
package startscreen

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Screen writes the startup screen to one writer, in one form.
type Screen struct {
	out io.Writer
	// fancy prints the logo and emoji, and color adds ANSI colors to them.
	fancy, color bool
	home         string
}

// New is the screen for out: the terminal form when out is a terminal, colored unless NO_COLOR is
// set, and the plain form otherwise. Only the terminal form shortens paths under HOME to ~, since a
// log keeps the full path for whoever greps it.
func New(out io.Writer, env environment.Environment) *Screen {
	if !isTerminal(out) {
		return &Screen{out: out}
	}
	return &Screen{out: out, fancy: true, color: env.Get("NO_COLOR") == "", home: env.Get("HOME")}
}

// isTerminal is true when out is a character device. /dev/null is one too, and nobody reads what
// lands there, so treating it as a terminal costs nothing.
func isTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// The mock-up's colors as RGB.
var (
	cyan    = rgb{0, 210, 255}
	magenta = rgb{230, 80, 220}
	red     = rgb{255, 95, 95}
	grey    = rgb{140, 140, 160}
)

type rgb struct{ r, g, b int }

func (c rgb) fg() string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.r, c.g, c.b) }

const (
	reset = "\x1b[0m"
	bold  = "\x1b[1m"
)

var logo = []string{
	"██████╗ ██╗███████╗██████╗  █████╗ ████████╗ ██████╗██╗  ██╗",
	"██╔══██╗██║██╔════╝██╔══██╗██╔══██╗╚══██╔══╝██╔════╝██║  ██║",
	"██║  ██║██║███████╗██████╔╝███████║   ██║   ██║     ███████║",
	"██║  ██║██║╚════██║██╔═══╝ ██╔══██║   ██║   ██║     ██╔══██║",
	"██████╔╝██║███████║██║     ██║  ██║   ██║   ╚██████╗██║  ██║",
	"╚═════╝ ╚═╝╚══════╝╚═╝     ╚═╝  ╚═╝   ╚═╝    ╚═════╝╚═╝  ╚═╝",
	"━━━━━━━━━━━●━━━━━━━━━━━━━━━●━━━━━━━━━━━━━━━━━━●━━━━━━━━━━━━▶",
}

const tagline = "event-driven reconcile loop for agents"

// paint wraps text in the color when the screen has color.
func (s *Screen) paint(c rgb, text string) string {
	if !s.color {
		return text
	}
	return c.fg() + text + reset
}

func (s *Screen) printf(format string, args ...any) { _, _ = fmt.Fprintf(s.out, format, args...) }

// section is a section name padded to the column the values start in.
func (s *Screen) section(name string) string {
	text := fmt.Sprintf("%-9s", name)
	if s.color {
		return bold + cyan.fg() + text + reset
	}
	return text
}

// indent starts every line of the terminal form, as in the mock-up.
func (s *Screen) indent() string {
	if s.fancy {
		return " "
	}
	return ""
}

// Begin prints the logo and the config file dispatch is about to read. It runs before the config
// loads, so a bad config still shows where dispatch looked.
func (s *Screen) Begin(configPath string) {
	if s.fancy {
		s.printf("\n")
		for _, line := range logo {
			s.printf("  %s\n", s.fade(line))
		}
		s.printf("  %s\n\n", s.paint(grey, tagline))
	}
	s.printf("%s%s%s\n", s.indent(), s.section("CONFIG"), s.shortPath(configPath))
}

// fade colors each column of line one step further from cyan toward magenta.
func (s *Screen) fade(line string) string {
	if !s.color {
		return line
	}
	runes := []rune(line)
	var b strings.Builder
	for i, r := range runes {
		f := float64(i) / float64(max(len(runes)-1, 1))
		b.WriteString(rgb{
			cyan.r + int(f*float64(magenta.r-cyan.r)),
			cyan.g + int(f*float64(magenta.g-cyan.g)),
			cyan.b + int(f*float64(magenta.b-cyan.b)),
		}.fg())
		b.WriteRune(r)
	}
	return b.String() + reset
}

func (s *Screen) shortPath(path string) string {
	if s.home != "" && strings.HasPrefix(path, s.home+string(os.PathSeparator)) {
		return "~" + strings.TrimPrefix(path, s.home)
	}
	return path
}

// Rules prints how many rules loaded.
func (s *Screen) Rules(n int) {
	s.printf("%s%s%d loaded\n", s.indent(), s.section("RULES"), n)
}

// Reading names the sources dispatch is about to read, in the order the screen lists them after
// the read. It prints before dispatch asks Linear for anything, so a slow source shows as a
// start that is waiting.
func (s *Screen) Reading(cfg config.Config) {
	var names []string
	for _, sl := range slots(cfg) {
		if sl.on {
			names = append(names, sl.name)
		}
	}
	s.printf("%s%s%s\n", s.indent(), s.section("READING"), strings.Join(names, ", "))
}

// slot is one source the screen shows: whether this config uses it, and how its result reads.
type slot struct {
	name string
	on   bool
	read func(config.Config, world.World) source
}

// slots is every source the screen shows, in order. Linear, GitHub and treehouse have no switch,
// so they are always read, and exactly one of herdr and Rex runs the workers.
func slots(cfg config.Config) []slot {
	host, other := slot{"herdr", true, multiplexerSource("herdr")}, slot{"rex", false, nil}
	if cfg.Plugins.Host() == config.HostRex {
		host, other = slot{"rex", true, multiplexerSource("rex")}, slot{"herdr", false, nil}
	}
	return []slot{
		{"linear", true, linearSource}, {"local", cfg.Plugins.Local.On, localSource}, host,
		{"github", true, githubSource}, {"treehouse", true, treehouseSource}, other,
	}
}

// FirstRead is what the first read found, and what the screen reports from it.
type FirstRead struct {
	Config config.Config
	World  world.World
	// Emoji is the workspace label emoji of each tracked ticket whose worker has a label.
	Emoji map[string]string
	// NextResync is when the next resync is due.
	NextResync time.Time
	// History says how many ledger receipts the start restored and from which history file. The
	// terminal form shows it as its own line.
	History string
	// StartLine is the start: line dispatch has always printed, which the plain form ends with for
	// the logs that grep it. The terminal form leaves it out, since the screen says the same.
	StartLine string
}

// Show prints what each source returned, the repos and the tracked tickets.
func (s *Screen) Show(r FirstRead) {
	if s.fancy {
		s.printf("%s%s%s\n", s.indent(), s.section("HISTORY"), r.History)
	}
	s.printf("\n")
	for i, src := range sourcesOf(r.Config, r.World) {
		label := ""
		if i == 0 {
			label = "SOURCES"
		}
		s.printf("%s%s%s%s\n", s.indent(), s.section(label), s.mark(src.state), s.sourceLine(src))
		for _, e := range src.errs {
			s.printf("%s%s%s%s\n", s.indent(), s.section(""), s.blankMark(), s.paint(red, e))
		}
	}
	s.printf("\n%s%s%s\n", s.indent(), s.section("REPOS"), s.repos(r.Config, r.World))
	s.printf("\n%s%s\n", s.indent(), s.watching(len(r.World.Tickets), r.NextResync))
	for _, t := range r.World.Tickets {
		s.printf("%s%s%s\n", s.indent(), s.section(""), s.ticketLine(t, r.Emoji[t.Identifier]))
	}
	if !s.fancy {
		s.printf("%s\n", r.StartLine)
	}
}

type state int

const (
	stateRead state = iota
	stateOff
	stateFailed
)

type source struct {
	name    string
	state   state
	summary string
	errs    []string
}

// mark is the emoji in front of a source, empty in the plain form.
func (s *Screen) mark(st state) string {
	if !s.fancy {
		return ""
	}
	switch st {
	case stateOff:
		return "⚪ "
	case stateFailed:
		return "🔴 "
	}
	return "🟢 "
}

func (s *Screen) blankMark() string {
	if !s.fancy {
		return ""
	}
	return "   "
}

func (s *Screen) sourceLine(src source) string {
	if src.state == stateFailed && src.summary == "" {
		return fmt.Sprintf("%-10s %s", src.name, s.paint(red, "failed"))
	}
	return fmt.Sprintf("%-10s %s", src.name, src.summary)
}

func sourcesOf(cfg config.Config, w world.World) []source {
	var out []source
	for _, sl := range slots(cfg) {
		if !sl.on {
			out = append(out, source{name: sl.name, state: stateOff, summary: "off"})
			continue
		}
		out = append(out, sl.read(cfg, w))
	}
	return out
}

func failed(name string, errs ...string) source {
	return source{name: name, state: stateFailed, errs: errs}
}

func linearSource(cfg config.Config, w world.World) source {
	if w.LinearErr != nil {
		return failed("linear", w.LinearErr.Error())
	}
	teams := "every team"
	if len(cfg.Plugins.Linear.Teams) > 0 {
		teams = "team " + strings.Join(cfg.Plugins.Linear.Teams, ", ")
	}
	n := 0
	for _, t := range w.Tickets {
		if ticket.SystemOf(t.Identifier) != ticket.SystemLocal {
			n++
		}
	}
	return source{name: "linear", summary: fmt.Sprintf("%s, label %s, %s", teams, cfg.Plugins.Linear.AILabel, count(n, "ticket"))}
}

func localSource(_ config.Config, w world.World) source {
	if w.LocalErr != nil {
		return failed("local", w.LocalErr.Error())
	}
	n := 0
	for _, t := range w.Tickets {
		if ticket.SystemOf(t.Identifier) == ticket.SystemLocal {
			n++
		}
	}
	return source{name: "local", summary: count(n, "ticket")}
}

func multiplexerSource(name string) func(config.Config, world.World) source {
	return func(_ config.Config, w world.World) source {
		if w.MultiplexerErr != nil {
			return failed(name, w.MultiplexerErr.Error())
		}
		return source{name: name, summary: fmt.Sprintf("%s, %s", count(len(w.Snapshot.Workspaces), "workspace"), count(len(w.Snapshot.Agents), "agent"))}
	}
}

// githubSource reports the PRs of tracked tickets, which is all a start reads, and not every open PR.
func githubSource(_ config.Config, w world.World) source {
	src := source{name: "github", summary: count(len(w.PRs), "pull request") + " on tracked tickets"}
	var unread []string
	for id := range w.PRReadFailed {
		unread = append(unread, id)
	}
	slices.Sort(unread)
	if len(unread) > 0 {
		msg := "could not read the pull request of " + strings.Join(unread, ", ")
		if len(w.PRRateLimited) > 0 {
			msg += " (GitHub's rate limit is spent)"
		}
		src.errs = append(src.errs, msg)
	}
	if w.InboxErr != nil {
		src.errs = append(src.errs, "inbox: "+w.InboxErr.Error())
	}
	if len(src.errs) > 0 {
		src.state = stateFailed
	}
	return src
}

func treehouseSource(cfg config.Config, w world.World) source {
	leased := 0
	for _, t := range w.Trees {
		if t.LeaseHolder != "" {
			leased++
		}
	}
	src := source{name: "treehouse", summary: fmt.Sprintf("%s, %d leased", count(len(w.Trees), "tree"), leased)}
	for _, label := range cfg.Repos.Labels() {
		if err := w.PoolErrs[label]; err != nil {
			src.errs = append(src.errs, fmt.Sprintf("%s: %v", label, err))
		}
	}
	if len(src.errs) > 0 {
		src.state = stateFailed
		// With every pool unread there are no trees to count.
		if len(src.errs) == len(cfg.Repos) {
			src.summary = ""
		}
	}
	return src
}

func (s *Screen) repos(cfg config.Config, w world.World) string {
	labels := cfg.Repos.Labels()
	if len(labels) == 0 {
		return "none"
	}
	var parts []string
	for _, label := range labels {
		unread := w.UnreadRepos[label]
		switch {
		case s.fancy && unread:
			parts = append(parts, "🔴 "+s.paint(red, label))
		case s.fancy:
			parts = append(parts, "🟢 "+label)
		case unread:
			parts = append(parts, label+" (unread)")
		default:
			parts = append(parts, label)
		}
	}
	return strings.Join(parts, "  ")
}

func (s *Screen) watching(n int, next time.Time) string {
	// Local, as the loop's own lines print their times.
	text := fmt.Sprintf("%s   next resync %s", count(n, "ticket"), next.Local().Format(time.TimeOnly))
	switch {
	case s.color:
		return bold + magenta.fg() + "▶ watching " + text + reset
	case s.fancy:
		return "▶ watching " + text
	}
	return s.section("WATCHING") + text
}

// ticketLine shows a ticket's label emoji in the terminal form, and its status where it has no
// label or the form has no emoji. The emoji is followed by a space, never column math, because
// some carry a variation selector that terminals draw at different widths.
func (s *Screen) ticketLine(t ticket.Ticket, emoji string) string {
	if s.fancy && emoji != "" {
		return fmt.Sprintf("%s %s  %s", emoji, t.Identifier, t.Title)
	}
	return fmt.Sprintf("%s%s  %s  %s", s.blankMark(), t.Identifier, t.Status, t.Title)
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

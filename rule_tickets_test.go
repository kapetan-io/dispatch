package dispatch_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileReview files one review ticket per pull request, searching first so a second cycle files nothing.
const fileReview = `whenever({ name: "file-review", each: "dispatch", if: () => true,
  then: (_, ctx) => {
    if (ctx.ticket.search({ attributes: { pr: "acme/api#7" } }).length > 0) return;
    return ctx.ticket.queueCreate("local", {
      title: "Review acme/api#7", repo: "dispatch", labels: ["review"], attributes: { pr: "acme/api#7", commit: "abc1234" },
    }, { once: ["review", "acme/api#7"] });
  } });`

// createdReceipts reads the create receipts the newest history segment holds, in its snapshot and its lines,
// one per attempt at its final state.
func (d *Dispatch) createdReceipts() []Receipt {
	d.t.Helper()
	_, lines := d.newestSegment()
	var out []Receipt
	at := map[int64]int{}
	add := func(r struct {
		Kind, Subject, Outcome, Detail string
		Started                        int64
	}) {
		if r.Kind != "create" {
			return
		}
		receipt := Receipt{Kind: r.Kind, Subject: r.Subject, State: r.Outcome, Detail: r.Detail}
		if i, ok := at[r.Started]; ok {
			out[i] = receipt
			return
		}
		at[r.Started] = len(out)
		out = append(out, receipt)
	}
	for _, text := range lines {
		var line struct {
			Line, Kind, Subject, Outcome, Detail string
			Started                              int64
			Ledger                               struct {
				Receipts []struct {
					Kind, Subject, Outcome, Detail string
					Started                        int64
				}
			}
		}
		require.NoError(d.t, json.Unmarshal([]byte(text), &line))
		switch line.Line {
		case "snapshot":
			for _, r := range line.Ledger.Receipts {
				add(r)
			}
		case "receipt":
			add(struct {
				Kind, Subject, Outcome, Detail string
				Started                        int64
			}{line.Kind, line.Subject, line.Outcome, line.Detail, line.Started})
		}
	}
	return out
}

func ids(list string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(list), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out
}

func TestTickFilesOneLocalTicketForARuleThatSearchesBeforeItCreates(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview)})
	d.StartBeforeTheEvents()

	d.Tick()
	d.Tick()

	assert.Equal(t, []string{"LOC-1"}, ids(d.ticketOut("list", "--attr", "pr=acme/api#7")))
	show := d.ticketOut("show", "LOC-1")
	assert.Contains(t, show, "Review acme/api#7")
	assert.Contains(t, show, "file-review")
	assert.Equal(t, []Receipt{{Kind: "create", Subject: "local", State: "done", Detail: "LOC-1"}}, d.createdReceipts())
}

func TestTickShowsARuleCreatedTicketToTheNextCycle(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview + `
whenever({ name: "greet", each: "ticket", if: (t) => t.system === "local" && t.attributes.commit === "abc1234",
  then: (t, ctx) => ctx.ticket.queueComment(t, "seen " + t.id, { once: ["greet", t.id] }) });`)})
	d.StartBeforeTheEvents()

	d.Tick()
	d.Tick()

	assert.Contains(t, d.ticketOut("show", "LOC-1"), "seen LOC-1")
}

func TestTickSearchFiltersLocalTicketsAndFindsFinishedOnes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		filter string
		want   string
	}{
		{name: "AttributesFindAFinishedTicket", filter: `{ attributes: { area: "billing" } }`, want: "LOC-2"},
		{name: "AttributesMatchExactly", filter: `{ attributes: { area: "Billing" } }`, want: ""},
		{name: "LabelsIgnoreCase", filter: `{ labels: ["REVIEW"] }`, want: "LOC-1"},
		{name: "StatusIsAJob", filter: `{ status: "finished" }`, want: "LOC-2"},
		{name: "OpenLeavesOutFinished", filter: `{ open: true }`, want: "LOC-1,LOC-3,LOC-4"},
		{name: "EveryFilterMustMatch", filter: `{ labels: ["review"], attributes: { area: "billing" } }`, want: ""},
		{name: "NoFilterFindsEveryTicket", filter: `{}`, want: "LOC-1,LOC-2,LOC-3,LOC-4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`whenever({ name: "found", each: "dispatch", if: () => true,
  then: (_, ctx) => {
    if (ctx.ticket.search({ attributes: { go: "1" } }).length === 0) return;
    return ctx.ticket.queueCreate("local", { title: "found", repo: "dispatch", description: ctx.ticket.search(` + test.filter + `).map((f) => f.id).join(",") },
      { once: ["found"] });
  } });`)})
			d.StartBeforeTheEvents()
			d.ticketOut("create", "--repo", "dispatch", "--title", "one", "--label", "review", "--attr", "area=webhooks")
			d.ticketOut("create", "--repo", "dispatch", "--title", "two", "--attr", "area=billing", "--status", "Done")
			d.ticketOut("create", "--repo", "dispatch", "--title", "three")
			d.ticketOut("create", "--repo", "dispatch", "--title", "go", "--attr", "go=1")

			d.Tick()

			assert.Contains(t, d.ticketOut("show", "LOC-5"), "found")
			assert.Equal(t, test.want, descriptionOf(t, d.ticketOut("show", "LOC-5")))
		})
	}
}

// descriptionOf is the indented description `dispatch ticket show` prints after the fields.
func descriptionOf(t *testing.T, show string) string {
	t.Helper()
	for _, line := range strings.Split(show, "\n") {
		if strings.HasPrefix(line, "  ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// A rule with no search is the case a crash can double: only the ledger stands between it and a second ticket.
func TestTickFilesNoSecondTicketAfterTheCreateLandedButItsReceiptWasNotWritten(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`whenever({ name: "file-review", each: "dispatch", if: () => true,
  then: (_, ctx) => ctx.ticket.queueCreate("local", { title: "Review acme/api#7", repo: "dispatch" }, { once: ["review", "acme/api#7"] }) });`)})
	d.StartBeforeTheEvents()
	d.Tick()
	d.Stop()
	// The crash left the attempted receipt and the filed ticket, and no outcome.
	_, lines := d.newestSegment()
	var kept []string
	for _, text := range lines {
		if !strings.Contains(text, `"line":"receipt"`) || !strings.Contains(text, `"kind":"create"`) || !strings.Contains(text, `"outcome"`) {
			kept = append(kept, text)
		}
	}
	require.Less(t, len(kept), len(lines))
	d.rewriteNewestSegment(kept)

	d.Restart()
	d.Tick()

	assert.Equal(t, []string{"LOC-1"}, ids(d.ticketOut("list")))
}

func TestTickFilesNoSecondTicketAfterHistoryRotates(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview)})
	d.StartBeforeTheEvents()
	d.Tick()
	require.Len(t, d.createdReceipts(), 1)

	d.Advance(7 * time.Hour)
	d.Tick()

	// The new segment starts without the create receipt, so the search is what holds the rule back.
	assert.Empty(t, d.createdReceipts())
	d.Restart()
	d.Tick()
	assert.Equal(t, []string{"LOC-1"}, ids(d.ticketOut("list")))
}

// A service named local in [http.<name>] must not keep the create receipts, whose subject is also local.
func TestTickDropsCreateReceiptsAtRotationEvenWithAnHTTPServiceNamedLocal(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn + "\n[http.local]\nbase_url = \"https://local.test/api\"\n", Rules: rule(fileReview)})
	d.StartBeforeTheEvents()
	d.Tick()
	require.Len(t, d.createdReceipts(), 1)

	d.Advance(7 * time.Hour)
	d.Tick()

	assert.Empty(t, d.createdReceipts())
	assert.Equal(t, []string{"LOC-1"}, ids(d.ticketOut("list")))
}

func TestStateResetQuietStillLetsARulesCreateFileItsTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview)})

	require.NoError(t, d.Run("state", "reset", "--quiet").Err)
	d.Tick()

	assert.Equal(t, []string{"LOC-1"}, ids(d.ticketOut("list")))
}

func TestStateResetQuietSeesATicketTheRuleAlreadyFiled(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview)})
	d.StartBeforeTheEvents()
	d.Tick()
	d.Stop()

	require.NoError(t, d.Run("state", "reset", "--quiet").Err)
	d.Restart()
	d.Tick()

	assert.Equal(t, []string{"LOC-1"}, ids(d.ticketOut("list")))
}

func TestTickGivesACLICreateAndARulesCreateInTheSameCycleDifferentIds(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview)})
	d.StartBeforeTheEvents()

	var wg sync.WaitGroup
	results := make([]Result, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0] = d.Run("ticket", "create", "--repo", "dispatch", "--title", "by hand")
	}()
	go func() {
		defer wg.Done()
		results[1] = d.Run("tick")
	}()
	wg.Wait()
	require.NoError(t, results[0].Err)
	require.NoError(t, results[1].Err)
	d.Tick()

	assert.ElementsMatch(t, []string{"LOC-1", "LOC-2"}, ids(d.ticketOut("list")))
}

func TestTickEndsARuleCreateOrEditThatBreaksTheStoresChecksRefusedAndChangesNothing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		then    string
		receipt string
		detail  string
	}{
		{name: "CreateInAnUnknownRepo", receipt: "create", detail: "no-such-repo",
			then: `ctx.ticket.queueCreate("local", { title: "x", repo: "no-such-repo" }, { once: ["bad"] })`},
		{name: "CreateUnderAMissingParent", receipt: "create", detail: "LOC-99",
			then: `ctx.ticket.queueCreate("local", { title: "x", repo: "dispatch", parent: "LOC-99" }, { once: ["bad"] })`},
		{name: "CreateBlockedByALinearTicket", receipt: "create", detail: "ENG-1",
			then: `ctx.ticket.queueCreate("local", { title: "x", repo: "dispatch", blockedBy: ["ENG-1"] }, { once: ["bad"] })`},
		{name: "CreateWithNoTitle", receipt: "create", detail: "title",
			then: `ctx.ticket.queueCreate("local", { title: " ", repo: "dispatch" }, { once: ["bad"] })`},
		{name: "EditToBeItsOwnParent", receipt: "edit", detail: "itself",
			then: `ctx.ticket.queueEdit(t, { parent: t.id }, { once: ["bad"] })`},
		{name: "EditToAnUnknownRepo", receipt: "edit", detail: "no-such-repo",
			then: `ctx.ticket.queueEdit(t, { repo: "no-such-repo" }, { once: ["bad"] })`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`whenever({ name: "bad", each: "ticket", if: (t) => t.system === "local" && t.id === "LOC-1",
  then: (t, ctx) => ` + test.then + ` });`)})
			d.StartBeforeTheEvents()
			d.ticketOut("create", "--repo", "dispatch", "--title", "first", "--attr", "k=v")
			before := d.ticketOut("show", "LOC-1")

			// A refused receipt makes the tick exit nonzero, depending on when the receipt ends.
			d.Run("tick")

			var outcomes []string
			for _, text := range d.receiptLines(test.receipt) {
				outcomes = append(outcomes, text)
			}
			require.NotEmpty(t, outcomes)
			last := outcomes[len(outcomes)-1]
			assert.Contains(t, last, `"outcome":"refused"`)
			assert.Contains(t, last, test.detail)
			assert.Equal(t, before, d.ticketOut("show", "LOC-1"))
			assert.Equal(t, []string{"LOC-1"}, ids(d.ticketOut("list")))
		})
	}
}

// receiptLines are the receipt lines of the newest history segment for one kind of action.
func (d *Dispatch) receiptLines(kind string) []string {
	d.t.Helper()
	_, lines := d.newestSegment()
	var out []string
	for _, text := range lines {
		if strings.Contains(text, `"line":"receipt"`) && strings.Contains(text, `"kind":"`+kind+`"`) {
			out = append(out, text)
		}
	}
	return out
}

func TestTickEditsALocalTicketARuleNames(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`whenever({ name: "tidy", each: "ticket", if: (t) => t.system === "local" && !t.attributes.tidy,
  then: (t, ctx) => ctx.ticket.queueEdit(t, { title: "Tidied", addLabels: ["seen"], setAttributes: { tidy: "yes" }, unsetAttributes: ["k"], startPR: 12 }, { once: ["tidy", t.id] }) });`)})
	d.StartBeforeTheEvents()
	d.ticketOut("create", "--repo", "dispatch", "--title", "first", "--attr", "k=v")

	d.Tick()

	show := d.ticketOut("show", "LOC-1")
	assert.Contains(t, show, "Tidied")
	assert.Contains(t, show, "seen")
	assert.Contains(t, show, "tidy=yes")
	assert.NotContains(t, show, "k=v")
	assert.Contains(t, show, "12")
}

func TestRulesCheckSaysWhyARuleThatQueuesWhatItCannotThrows(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		config string
		then   string
		want   string
	}{
		{name: "CreateWithLocalTicketsOff", then: `ctx.ticket.queueCreate("local", { title: "x", repo: "dispatch" }, { once: ["x"] })`, want: "local tickets are off"},
		{name: "SearchWithLocalTicketsOff", then: `ctx.ticket.search({}).length`, want: "local tickets are off"},
		{name: "CreateInTheLinearSystem", config: localOn, then: `ctx.ticket.queueCreate("linear", { title: "x", repo: "dispatch" }, { once: ["x"] })`, want: `only "local" can be created`},
		{name: "CreateWithAnUnknownField", config: localOn, then: `ctx.ticket.queueCreate("local", { title: "x", repo: "dispatch", color: "red" }, { once: ["x"] })`, want: `has no field "color"`},
		{name: "CreateWithAWrongType", config: localOn, then: `ctx.ticket.queueCreate("local", { title: "x", repo: "dispatch", labels: "review" }, { once: ["x"] })`, want: "must be a list of strings"},
		{name: "CreateWithAnUnknownStatusKey", config: localOn, then: `ctx.ticket.queueCreate("local", { title: "x", repo: "dispatch", status: "finished" }, { once: ["x"] })`, want: "is not a status key"},
		{name: "SearchWithAnUnknownJob", config: localOn, then: `ctx.ticket.search({ status: "doing" }).length`, want: "is not a job"},
		{name: "EditOfALinearTicket", config: localOn, then: `ctx.ticket.queueEdit(t, { title: "x" }, { once: ["x"] })`, want: "only a local ticket can be edited"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Config: test.config, Rules: rule(`whenever({ name: "broken", each: "ticket", if: (t) => t.id === "ENG-1",
  then: (t, ctx) => { ` + test.then + `; } });`)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			res := d.Run("rules", "check")

			require.Error(t, res.Err)
			assert.Contains(t, res.Stdout, "finding: broken threw in its then for ENG-1")
			assert.Contains(t, res.Stdout, test.want)
		})
	}
}

func TestReplayMatchesARunWhoseRulesSearchedAndCreatedLocalTickets(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview)})
	d.StartBeforeTheEvents()
	d.Tick()
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayReportsADifferenceWhenARulesSearchesChange(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview)})
	d.StartBeforeTheEvents()
	d.Tick()
	d.Stop()

	// The rule now searches for another attribute than the one the run recorded.
	res := d.Run("replay", "--rules", oneRule(t, strings.ReplaceAll(fileReview, `pr: "acme/api#7" } }).length`, `pr: "acme/api#8" } }).length`)))

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "difference")
}

func TestRulesTestAnswersASearchFromTheScenariosLocalTickets(t *testing.T) {
	t.Parallel()
	files := rule(`whenever({ name: "file-review", each: "dispatch", if: () => true,
  then: (_, ctx) => {
    if (ctx.ticket.search({ attributes: { pr: "acme/api#7" } }).length > 0) return;
    return ctx.ticket.queueCreate("local", { title: "Review", repo: "dispatch", attributes: { pr: "acme/api#7" } }, { once: ["review", "acme/api#7"] });
  } });`)
	files["review.test.ts"] = `import { scenario, ticket, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");

scenario(rules, "files a review ticket when none carries the attribute", s => {
  s.start();
  s.expect([["file-review", "create", "local"]]);
});

scenario(rules, "files nothing when a finished ticket carries it", s => {
  s.given(ticket("LOC-1", { status: "done", attributes: { pr: "acme/api#7" } }));
  s.start();
  s.expect([]);
});
`
	d := newDispatch(t, dispatchOptions{Rules: files})

	res := d.Run("rules", "test")

	require.NoError(t, res.Err, res.Stdout)
	assert.Contains(t, res.Stdout, "ok   review.test.ts: files a review ticket when none carries the attribute")
	assert.Contains(t, res.Stdout, "ok   review.test.ts: files nothing when a finished ticket carries it")
}

func TestRulesCheckSaysAStartPRThatNoPRCouldHaveIsRefusedByTheRule(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`whenever({ name: "broken", each: "ticket", if: (t) => t.id === "ENG-1",
  then: (t, ctx) => ctx.ticket.queueCreate("local", { title: "x", repo: "dispatch", startPR: Infinity }, { once: ["x"] }) });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.Error(t, res.Err)
	assert.Contains(t, res.Stdout, "startPR must be a whole number")
}

func TestRulesCheckSaysASearchInIfThrows(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`whenever({ name: "broken", each: "ticket", if: (t, ctx) => ctx.ticket.search({}).length > 0, then: () => {} });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.Error(t, res.Err)
	assert.Contains(t, res.Stdout, "ctx.ticket.search can only be called in then")
}

func TestTickEndsARuleEditThatLinksTicketsWrongRefusedAndChangesNothing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		changes string
		detail  string
	}{
		{name: "ParentThatMakesACycle", changes: `{ parent: "LOC-2" }`, detail: "LOC-2"},
		{name: "ParentThatIsMissing", changes: `{ parent: "LOC-99" }`, detail: "LOC-99"},
		{name: "BlockedByALinearTicket", changes: `{ blockedBy: ["ENG-1"] }`, detail: "ENG-1"},
		{name: "BlockedByAMissingTicket", changes: `{ blockedBy: ["LOC-99"] }`, detail: "LOC-99"},
		{name: "BlockedByItself", changes: `{ blockedBy: ["LOC-1"] }`, detail: "itself"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`whenever({ name: "bad", each: "ticket", if: (t) => t.system === "local" && t.id === "LOC-1",
  then: (t, ctx) => ctx.ticket.queueEdit(t, ` + test.changes + `, { once: ["bad"] }) });`)})
			d.StartBeforeTheEvents()
			d.ticketOut("create", "--repo", "dispatch", "--title", "first")
			d.ticketOut("create", "--repo", "dispatch", "--title", "second", "--parent", "LOC-1")
			first, second := d.ticketOut("show", "LOC-1"), d.ticketOut("show", "LOC-2")

			// A refused receipt makes the tick exit nonzero, depending on when the receipt ends.
			d.Run("tick")

			edits := d.receiptLines("edit")
			require.NotEmpty(t, edits)
			assert.Contains(t, edits[len(edits)-1], `"outcome":"refused"`)
			assert.Contains(t, edits[len(edits)-1], test.detail)
			assert.Equal(t, first, d.ticketOut("show", "LOC-1"))
			assert.Equal(t, second, d.ticketOut("show", "LOC-2"))
		})
	}
}

func TestReplayReportsADifferenceWhenARulesStopsSearchingBeforeItCreates(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(fileReview)})
	d.StartBeforeTheEvents()
	d.Tick()
	d.Stop()

	// The first cycle files the same ticket either way, so only the recorded search tells the runs apart.
	res := d.Run("replay", "--rules", oneRule(t, `whenever({ name: "file-review", each: "dispatch", if: () => true,
  then: (_, ctx) => ctx.ticket.queueCreate("local", {
    title: "Review acme/api#7", repo: "dispatch", labels: ["review"], attributes: { pr: "acme/api#7", commit: "abc1234" },
  }, { once: ["review", "acme/api#7"] }) });`))

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "difference")
}

func TestReplayMatchesASegmentWrittenBeforeRulesCouldSearch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`whenever({ name: "file-review", each: "dispatch", if: () => true,
  then: (_, ctx) => ctx.ticket.queueCreate("local", { title: "Review", repo: "dispatch" }, { once: ["review"] }) });`)})
	d.StartBeforeTheEvents()
	d.Tick()
	d.Stop()
	// A segment of an older dispatch has no searches field on its cycle lines.
	_, lines := d.newestSegment()
	for i, text := range lines {
		lines[i] = strings.Replace(text, `"searches":[]`, "", 1)
		require.NotContains(t, lines[i], `"searches"`)
	}
	d.rewriteNewestSegment(lines)

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

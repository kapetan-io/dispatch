package adrcheck_test

// No exception needed: these tests run adrcheck's own command line through Run, which is that tool's surface.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapetan-io/dispatch/internal/adrcheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const clean = `# 1. Fake external tools at their domain interfaces

Date: 2026-10-12

## Status

Accepted

## Context

- Dispatch drives herdr and treehouse as external programs, and the rules live in a rules directory.
- SHA-256, UTF-8 and ISO-8601 name standards, not tickets. See [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110).
- ADR 0002 and ADR-0002 both name another ADR, and so does [the next one](0002-next.md).

## Decision

We will fake each tool behind ` + "`herdr.Multiplexer`" + ` and set ` + "`[plugins.herdr] host`" + `.

## Consequences

- Each tool's wire format exists once.
`

const next = `# 2. Next

Date: 2026-10-13

## Status

Amends ADR 0001
Superseded in part by ADR-0001

## Decision

We will do the next thing.
`

// repo writes a repo with a docs/adr directory holding files, plus the other repo
// files an ADR might cite.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"internal/rules/state.go": "package rules\n",
		"docs/rules.md":           "# Rules\n",
		"rules/shared.ts":         "export {}\n",
		"go.mod":                  "module x\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs/adr"), 0o755))
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, "docs/adr", name), []byte(body), 0o644))
	}
	return root
}

func run(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := adrcheck.Run(t.Context(), append([]string{"-root", root}, args...), adrcheck.Options{Stdout: &stdout, Stderr: &stderr})
	return code, stdout.String(), stderr.String()
}

func TestCleanADRsPass(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": clean, "0002-next.md": next, "README.md": "Cites ENG-1 but is not an ADR.\n"})
	code, stdout, stderr := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestEachProblemNamesTheADRTheLineAndTheText(t *testing.T) {
	const file = "docs/adr/0001-fake.md"
	for _, test := range []struct {
		name string
		line string
		want string
	}{
		{"linear ticket", "Proposed first in ENG-630.", `:11: cites a ticket: "ENG-630"`},
		{"second team key", "A local ticket, LOC-7, asked.", `:11: cites a ticket: "LOC-7"`},
		{"ticket id inside a URL", "See https://linear.app/x/issue/ENG-153/title.", `:11: cites a ticket: "ENG-153"`},
		{"existing file", "The hold lives in `internal/rules/state.go`.", `:11: references a file or directory in the repo: "internal/rules/state.go"`},
		{"existing directory", "Fakes live in internal/rules/ today.", `:11: references a file or directory in the repo: "internal/rules/"`},
		{"existing root file", "Pinned in go.mod.", `:11: references a file or directory in the repo: "go.mod"`},
		{"missing source file", "Moved to (`internal/email/retry.go`).", `:11: references a file or directory in the repo: "internal/email/retry.go"`},
		{"relative link to a file", "Read [the rules](../rules.md).", `:11: references a file or directory in the repo: "../rules.md"`},
		{"pull request url", "Landed in https://github.com/kapetan-io/dispatch/pull/223.", `:11: cites the repo's pull requests, commits, branches or issues: "https://github.com/kapetan-io/dispatch/pull/223"`},
		{"branch link", "See [the branch](https://github.com/kapetan-io/dispatch/tree/thrawn01/x).", `:11: cites the repo's pull requests, commits, branches or issues: "https://github.com/kapetan-io/dispatch/tree/thrawn01/x"`},
		{"pull request number", "ENG-491's merge retry (#154) stays.", `:11: cites the repo's pull requests, commits, branches or issues: "#154"`},
		{"commit", "It now retries at the resync (`e01aa31`).", `:11: cites the repo's pull requests, commits, branches or issues: "e01aa31"`},
		{"missing ADR link", "Builds on [ADR 9](0009-gone.md).", `:11: links to an ADR that doesn't exist: "0009-gone.md"`},
		{"missing ADR link through the parent", "Builds on [ADR 9](../adr/0009-gone.md).", `:11: links to an ADR that doesn't exist: "../adr/0009-gone.md"`},
		{"commit url", "Landed in https://github.com/kapetan-io/dispatch/commit/e01aa31.", `:11: cites the repo's pull requests, commits, branches or issues: "https://github.com/kapetan-io/dispatch/commit/e01aa31"`},
		{"issue url", "Reported in https://github.com/kapetan-io/dispatch/issues/5.", `:11: cites the repo's pull requests, commits, branches or issues: "https://github.com/kapetan-io/dispatch/issues/5"`},
		{"repo url in other case", "Landed in https://github.com/Kapetan-IO/Dispatch/pull/5.", `:11: cites the repo's pull requests, commits, branches or issues: "https://github.com/Kapetan-IO/Dispatch/pull/5"`},
		{"repo link without a scheme", "Landed in github.com/kapetan-io/dispatch/pull/5.", `:11: cites the repo's pull requests, commits, branches or issues: "github.com/kapetan-io/dispatch/pull/5"`},
		{"owner and repo issue reference", "Reported in kapetan-io/dispatch#12.", `:11: cites the repo's pull requests, commits, branches or issues: "kapetan-io/dispatch#12"`},
		{"lower-case ticket", "Asked in eng-630.", `:11: cites a ticket: "eng-630"`},
		{"pull request in words", "Landed in PR 223.", `:11: cites the repo's pull requests, commits, branches or issues: "PR 223"`},
		{"commit in words", "Fixed in commit e01aa31.", `:11: cites the repo's pull requests, commits, branches or issues: "e01aa31"`},
		{"file with a line number", "See `internal/email/retry.go:42`.", `:11: references a file or directory in the repo: "internal/email/retry.go:42"`},
		{"file with an anchor", "See internal/rules/state.go#L10.", `:11: references a file or directory in the repo: "internal/rules/state.go#L10"`},
		{"repo root path in prose", "See /internal/rules/state.go.", `:11: references a file or directory in the repo: "/internal/rules/state.go"`},
		{"pull request without a space", "Landed in PR#14.", `:11: cites the repo's pull requests, commits, branches or issues: "PR#14"`},
		{"pull request in backticks", "Landed in `#12`.", `:11: cites the repo's pull requests, commits, branches or issues: "#12"`},
		{"command path in prose", "Run `go run ./internal/rules/` to see it.", `:11: references a file or directory in the repo: "./internal/rules/"`},
		{"path in a footnote", "[^1]: See internal/rules/ and the PR.", `:11: references a file or directory in the repo: "internal/rules/"`},
		{"raw file link", "[raw](https://raw.githubusercontent.com/kapetan-io/dispatch/main/go.mod)", `:11: cites the repo's pull requests, commits, branches or issues: "https://raw.githubusercontent.com/kapetan-io/dispatch/main/go.mod"`},
		{"absolute link to a file", "Read [the state](/internal/rules/state.go).", `:11: references a file or directory in the repo: "/internal/rules/state.go"`},
		{"parent path in prose", "The hold lives in ../../internal/rules/state.go today.", `:11: references a file or directory in the repo: "../../internal/rules/state.go"`},
		{"reference link definition", "[state]: ../../internal/rules/state.go", `:11: references a file or directory in the repo: "../../internal/rules/state.go"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := "# 1. Fake\n\nDate: 2026-10-12\n\n## Status\n\nAccepted\n\n## Context\n\n" + test.line + "\n"
			root := repo(t, map[string]string{"0001-fake.md": body})
			code, stdout, stderr := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
			assert.Equal(t, 1, code)
			assert.Contains(t, stdout, file+test.want)
			assert.Contains(t, stderr, "ADR problem")
		})
	}
}

func TestStatusMustBeAnAllowedForm(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		want   string
	}{
		{"amended note", "Accepted, amended", `:7: has a Status outside the allowed forms (Proposed, Accepted, Rejected, Deprecated, Superseded by ADR N, Superseded in part by ADR N, Supersedes in part ADR N, Amends ADR N, Amended by ADR N): "Accepted, amended"`},
		{"prose amendment", "Accepted. Amends ADR 0006, which says dispatch keeps no id record.", `:7: has a Status outside the allowed forms (Proposed, Accepted, Rejected, Deprecated, Superseded by ADR N, Superseded in part by ADR N, Supersedes in part ADR N, Amends ADR N, Amended by ADR N): "Accepted. Amends ADR 0006, which says dispatch keeps no id record."`},
		{"dated", "Accepted 2026-11-04", `:7: has a Status outside the allowed forms (Proposed, Accepted, Rejected, Deprecated, Superseded by ADR N, Superseded in part by ADR N, Supersedes in part ADR N, Amends ADR N, Amended by ADR N): "Accepted 2026-11-04"`},
		{"superseded without number", "Superseded", `:7: has a Status outside the allowed forms (Proposed, Accepted, Rejected, Deprecated, Superseded by ADR N, Superseded in part by ADR N, Supersedes in part ADR N, Amends ADR N, Amended by ADR N): "Superseded"`},
		{"empty", "", `:5: has an empty Status: "## Status"`},
		{"supersedes in part a missing ADR", "Supersedes in part ADR 0009", `:7: has a Status naming an ADR that doesn't exist: "Supersedes in part ADR 0009"`},
		{"supersedes in part without a number", "Supersedes in part", `:7: has a Status outside the allowed forms`},
		{"names a missing ADR", "Superseded by ADR 0009", `:7: has a Status naming an ADR that doesn't exist: "Superseded by ADR 0009"`},
		{"one bad line of two", "Accepted\nAccepted, amended", `:8: has a Status outside the allowed forms (Proposed, Accepted, Rejected, Deprecated, Superseded by ADR N, Superseded in part by ADR N, Supersedes in part ADR N, Amends ADR N, Amended by ADR N): "Accepted, amended"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "\nAccepted\n", "\n"+test.status+"\n", 1), "0002-next.md": next})
			code, stdout, _ := run(t, root)
			assert.Equal(t, 1, code)
			assert.Contains(t, stdout, "docs/adr/0001-fake.md")
			assert.Contains(t, stdout, test.want)
		})
	}
}

func TestEveryAllowedStatusPasses(t *testing.T) {
	for _, status := range []string{
		"Proposed", "Accepted", "Rejected", "Deprecated",
		"Superseded by ADR 0002", "Superseded by ADR-0002",
		"Superseded in part by ADR 2", "Supersedes in part ADR 0002", "Supersedes in part ADR-2", "Amends ADR 0002", "Amended by ADR-0002",
		"Accepted\nAmends ADR 0002",
	} {
		t.Run(status, func(t *testing.T) {
			root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "\nAccepted\n", "\n"+status+"\n", 1), "0002-next.md": next})
			code, stdout, _ := run(t, root)
			assert.Equal(t, 0, code)
			assert.Empty(t, stdout)
		})
	}
}

func TestMissingStatusSectionFails(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "## Status\n\nAccepted\n", "", 1), "0002-next.md": next})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `docs/adr/0001-fake.md:1: has no Status section: "# 1. Fake external tools at their domain interfaces"`)
}

func TestSharedNumberFailsBothADRs(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": clean, "0002-next.md": next, "0002-other.md": next})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `docs/adr/0002-next.md:1: shares its number with another ADR: "0002-other.md"`)
	assert.Contains(t, stdout, `docs/adr/0002-other.md:1: shares its number with another ADR: "0002-next.md"`)
	assert.NotContains(t, stdout, "0001-fake.md")
}

func TestTicketsAreCheckedOnlyForGivenKeys(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "Each tool's wire format", "ENG-1 and ABC-2 say each tool's wire format", 1), "0002-next.md": next})

	code, stdout, _ := run(t, root, "-ticket-keys", "ABC")
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `cites a ticket: "ABC-2"`)
	assert.NotContains(t, stdout, "ENG-1")

	code, stdout, _ = run(t, root)
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
}

func TestOtherReposLinksStayAllowed(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "Each tool's wire format", "Per https://github.com/golang/go/issues/1 each tool's wire format", 1), "0002-next.md": next})
	code, stdout, _ := run(t, root, "-repo", "kapetan-io/dispatch")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
}

func TestBadArgumentsExitTwo(t *testing.T) {
	root := repo(t, nil)
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"key with a dash", []string{"-ticket-keys", "ENG-X"}, `ticket key "ENG-X"`},
		{"repo without owner", []string{"-repo", "dispatch"}, `repo "dispatch" is not owner/name`},
		{"stray argument", []string{"docs"}, `unexpected argument "docs"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := run(t, root, test.args...)
			assert.Equal(t, 2, code)
			assert.Contains(t, stderr, test.want)
		})
	}
}

func TestRepoWithoutADRsPasses(t *testing.T) {
	code, stdout, _ := run(t, t.TempDir(), "-ticket-keys", "ENG")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
}

func TestLinksToOtherADRsAndOutsideDocsStayAllowed(t *testing.T) {
	for _, line := range []string{
		"Builds on [ADR 2](./0002-next.md) and [its decision](0002-next.md#decision).",
		"Builds on [ADR 2](../adr/0002-next.md).",
		"Builds on docs/adr/0002-next.md.",
		"Builds on ADR 0002, ADR-0002 and ADR 2.",
		"Per [the Go spec](https://go.dev/ref/spec), <https://www.rfc-editor.org/rfc/rfc9110> and https://github.com/golang/go/issues/1.",
		"SHA-256, UTF-8, ISO-8601 and ENG-team name no ticket.",
		"Sign with ed25519 keys; deadbeef12 and 3f2b1c4d are not commits.",
		"The project lives at https://github.com/kapetan-io/dispatch.",
		"![flow](images/flow.png)",
		"Limit is `1048576` bytes, split on `/` or read / write.",
		"[readme](https://github.com/kapetan-io/dispatch#readme) and https://github.com/kapetan-io/dispatch?tab=readme-ov-file.",
		"[the index](./) lists them.",
		"A path starting with ../ or ./ or / is relative.",
		"Login hooks in /etc/profile.d/x.sh run first.",
		"https://github.com/kapetan-io/dispatch/#readme and https://github.com/kapetan-io/dispatch/?tab=readme-ov-file.",
		"ADRs live in docs/adr and outside the repo in /etc/hosts.",
	} {
		t.Run(line, func(t *testing.T) {
			body := "# 1. Fake\n\nDate: 2026-10-12\n\n## Status\n\nAccepted\n\n## Context\n\n" + line + "\n"
			root := repo(t, map[string]string{"0001-fake.md": body, "0002-next.md": next})
			code, stdout, _ := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
			assert.Equal(t, 0, code)
			assert.Empty(t, stdout)
		})
	}
}

func TestSharedNumberFailsWhateverItsPadding(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": clean, "0002-next.md": next, "2-other.md": next})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `docs/adr/0002-next.md:1: shares its number with another ADR: "2-other.md"`)
	assert.Contains(t, stdout, `docs/adr/2-other.md:1: shares its number with another ADR: "0002-next.md"`)
}

// This repo's own ADRs pass the check as make adr-check runs it.
func TestThisReposADRsPass(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	code, stdout, stderr := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestATicketInALinkIsReportedOnce(t *testing.T) {
	body := "# 1. Fake\n\nDate: 2026-10-12\n\n## Status\n\nAccepted\n\n## Context\n\nSee [ENG-1](https://linear.app/x/issue/ENG-1/title).\n"
	root := repo(t, map[string]string{"0001-fake.md": body})
	code, stdout, _ := run(t, root, "-ticket-keys", "ENG")
	assert.Equal(t, 1, code)
	assert.Equal(t, "docs/adr/0001-fake.md:11: cites a ticket: \"ENG-1\"\n", stdout)
}

func TestAPullRequestWrittenTwoWaysIsReportedOnce(t *testing.T) {
	body := "# 1. Fake\n\nDate: 2026-10-12\n\n## Status\n\nAccepted\n\n## Context\n\nThis was PR #12.\n"
	root := repo(t, map[string]string{"0001-fake.md": body})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Equal(t, "docs/adr/0001-fake.md:11: cites the repo's pull requests, commits, branches or issues: \"#12\"\n", stdout)
}

func TestReferenceStyleImageBesideTheADRsIsAllowed(t *testing.T) {
	body := "# 1. Fake\n\nDate: 2026-10-12\n\n## Status\n\nAccepted\n\n## Context\n\n![flow][diagram]\n\n[diagram]: images/flow.png\n"
	root := repo(t, map[string]string{"0001-fake.md": body})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
}

func TestMissingRootExitsTwo(t *testing.T) {
	code, stdout, stderr := run(t, filepath.Join(t.TempDir(), "typo"), "-ticket-keys", "ENG")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "typo")
}

// adrWith returns ADR 1 with body in its Context section.
func adrWith(body string) string {
	return "# 1. Fake\n\nDate: 2026-10-12\n\n## Status\n\nAccepted\n\n## Context\n\n" + body + "\n"
}

func TestWildcardPatternsAreNotFileReferences(t *testing.T) {
	for name, body := range map[string]string{
		"in prose":            "Every internal/rules/*.go file.",
		"in a fenced example": "```yaml\nsources: [api/v1/*.proto]\n```",
		"missing directory":   "Every missing/dir/*.go file.",
	} {
		t.Run(name, func(t *testing.T) {
			root := repo(t, map[string]string{"0001-fake.md": adrWith(body)})
			code, stdout, _ := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
			assert.Equal(t, 0, code)
			assert.Empty(t, stdout)
		})
	}
}

func TestQuotedFenceSkipsFilePaths(t *testing.T) {
	const output = "services/git-server/internal/graphapi/graphapi.go:32:6: unused"
	for name, body := range map[string]string{
		"bare marker":                      "```quoted\n" + output + "\n```",
		"marker after language":            "```text quoted\n" + output + "\n```",
		"tilde fence":                      "~~~quoted\n" + output + "\n~~~",
		"existing file":                    "```quoted\ninternal/rules/state.go\n```",
		"longer fence holds a shorter one": "````quoted\n```\nother/x.go\n````",
		"closer with text does not close":  "```quoted\n``` not a closer\nother/x.go\n```",
		"tilde line inside a backtick one": "```quoted\n~~~\nother/x.go\n```",
	} {
		t.Run(name, func(t *testing.T) {
			root := repo(t, map[string]string{"0001-fake.md": adrWith(body)})
			code, stdout, _ := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
			assert.Equal(t, 0, code)
			assert.Empty(t, stdout)
		})
	}
}

func TestOtherFencedBlocksAndTextAfterAQuotedOneStillFail(t *testing.T) {
	const file = "docs/adr/0001-fake.md"
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"unmarked fence", "```\ninternal/email/retry.go\n```", `:12: references a file or directory in the repo: "internal/email/retry.go"`},
		{"language only", "```go\ninternal/email/retry.go\n```", `:12: references a file or directory in the repo: "internal/email/retry.go"`},
		{"marker as part of a word", "```unquoted\ninternal/email/retry.go\n```", `:12: references a file or directory in the repo: "internal/email/retry.go"`},
		{"after the quoted fence closes", "```quoted\nother/x.go\n```\nSee internal/email/retry.go.", `:14: references a file or directory in the repo: "internal/email/retry.go"`},
		{"short closer does not close", "````quoted\n```\n````\nSee internal/email/retry.go.", `:14: references a file or directory in the repo: "internal/email/retry.go"`},
		{"url in a quoted fence", "```quoted\nhttps://github.com/kapetan-io/dispatch/pull/1\n```", `:12: cites the repo's pull requests, commits, branches or issues: "https://github.com/kapetan-io/dispatch/pull/1"`},
		{"emphasised path", "See **internal/email/retry.go**.", `:11: references a file or directory in the repo: "internal/email/retry.go"`},
		{"path on a fence's opening line", "```quoted internal/email/retry.go\nother/x.go\n```", `:11: references a file or directory in the repo: "internal/email/retry.go"`},
		{"quoted fence never closed", "```quoted\nother/x.go", ":11: has a quoted fenced block with no closing line (a closing line holds only the fence characters), so paths after it go unchecked: \"```quoted\""},
		{"ticket in a quoted fence", "```quoted\nENG-12\n```", `:12: cites a ticket: "ENG-12"`},
		{"history in a quoted fence", "```quoted\nsee #12\n```", `:12: cites the repo's pull requests, commits, branches or issues: "#12"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := repo(t, map[string]string{"0001-fake.md": adrWith(test.body)})
			code, stdout, _ := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
			assert.Equal(t, 1, code)
			assert.Contains(t, stdout, file+test.want)
		})
	}
}

func TestHeadingsInsideAFencedBlockAreNotStatus(t *testing.T) {
	for name, body := range map[string]string{
		"status heading in a backtick fence": "```\n## Status\nsome output\n```",
		"status heading in a tilde fence":    "~~~text\n## Status\nsome output\n~~~",
		"status heading in a quoted fence":   "```quoted\n## Status\nsome output\n```",
		"longer fence holds a shorter one":   "````\n```\n## Status\nsome output\n````",
	} {
		t.Run(name, func(t *testing.T) {
			root := repo(t, map[string]string{"0001-fake.md": adrWith(body)})
			code, stdout, _ := run(t, root)
			assert.Equal(t, 0, code)
			assert.Empty(t, stdout)
		})
	}

	// A heading inside a fence does not end the real Status section, so the line after the fence is still checked.
	root := repo(t, map[string]string{"0001-fake.md": "# 1. Fake\n\n## Status\n\n```\n# comment\n```\n\nBogus\n"})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `has a Status outside the allowed forms`)
	assert.Contains(t, stdout, `"Bogus"`)
}

func TestMissingStatusStillFailsWhenOnlyAFenceHoldsOne(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": "# 1. Fake\n\n## Context\n\n```\n## Status\nAccepted\n```\n"})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "has no Status section")
}

func TestPathFindingNamesTheWayToQuote(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": adrWith("See internal/email/retry.go.")})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "(if it is another repo's output, open its fence with ```quoted)")

	root = repo(t, map[string]string{"0001-fake.md": adrWith("```quoted internal/email/retry.go\n```")})
	_, stdout, _ = run(t, root)
	assert.Contains(t, stdout, `references a file or directory in the repo: "internal/email/retry.go"`)
	assert.NotContains(t, stdout, "open its fence")

	root = repo(t, map[string]string{"0001-fake.md": adrWith("Read [the rules](../rules.md).")})
	_, stdout, _ = run(t, root)
	assert.Contains(t, stdout, `references a file or directory in the repo: "../rules.md"`)
	assert.NotContains(t, stdout, "quoted")
}

// Package existingtestlabel labels a pull request that modifies or deletes a Go test function
// that existed at the merge base, and removes the label once that stops being true. Which
// tests changed comes from `tiger changed-tests`.
package existingtestlabel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Label is the label a pull request carries while it changes an existing test.
const Label = "changes-existing-tests"

// reportSchema is the only tiger report version this command reads.
const reportSchema = "tiger.changed-tests/v1"

// Options carries the command's output streams and how it reaches GitHub.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
	// APIURL is GitHub's REST API root. Empty means https://api.github.com.
	APIURL string
	// Token authenticates the REST calls.
	Token string
	// Client sends the REST calls. Nil means http.DefaultClient.
	Client *http.Client
}

// Run reads which existing tests the branch checked out at -root changes since -base, then
// adds or removes the label on pull request -pr in -repo so it matches. It prints the
// changed tests and what it did to the label. It returns 0 when the label matches the
// report and 2 when it could not tell or could not change the label.
func Run(ctx context.Context, args []string, opts Options) int {
	if err := run(ctx, args, opts); err != nil {
		fmt.Fprintln(opts.Stderr, "existingtestlabel:", err)
		return 2
	}
	return 0
}

type report struct {
	Schema               string `json:"schema"`
	ChangesExistingTests bool   `json:"changes_existing_tests"`
	Tests                []struct {
		Package  string `json:"package"`
		File     string `json:"file"`
		Function string `json:"function"`
		Change   string `json:"change"`
	} `json:"tests"`
}

func run(ctx context.Context, args []string, opts Options) error {
	fs := flag.NewFlagSet("existingtestlabel", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	root := fs.String("root", ".", "the checkout of the pull request's head")
	base := fs.String("base", "", "the git ref the pull request merges into, such as origin/main")
	repo := fs.String("repo", "", "the repo as owner/name")
	pr := fs.Int("pr", 0, "the pull request number")
	tiger := fs.String("tiger", "tiger", "the tiger binary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" || *pr <= 0 || strings.Count(*repo, "/") != 1 {
		return errors.New("-base, -repo owner/name and -pr are required")
	}
	// tiger exits 2 without a go.mod, and a repo with no Go module has no Go tests to change.
	if _, err := os.Stat(filepath.Join(*root, "go.mod")); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(opts.Stdout, "no go.mod in %s, so no Go tests to check\n", *root)
		return nil
	}

	r, err := changedTests(ctx, *tiger, *root, *base)
	if err != nil {
		return err
	}
	for _, t := range r.Tests {
		fmt.Fprintf(opts.Stdout, "%s %s.%s (%s)\n", t.Change, t.Package, t.Function, t.File)
	}

	gh := github{opts: opts, repoPath: "repos/" + *repo, issuePath: fmt.Sprintf("repos/%s/issues/%d", *repo, *pr)}
	carried, err := gh.label(ctx)
	if err != nil {
		return err
	}
	has := carried != ""
	switch {
	case r.ChangesExistingTests && !has:
		// Adding a label the repo lacks creates it, which needs issues: write, more than the
		// caller grants. Naming the fix beats GitHub's bare 403.
		err := gh.call(ctx, http.MethodGet, gh.repoPath+"/labels/"+url.PathEscape(Label), nil, nil)
		var refused statusError
		if errors.As(err, &refused) && refused.code == http.StatusNotFound {
			return fmt.Errorf("%s has no %s label, and this token can't create one; someone with write access to the repo runs once: gh label create %s --repo %s", *repo, Label, Label, *repo)
		}
		if err != nil {
			return fmt.Errorf("reading the repo's %s label: %w", Label, err)
		}
		if err := gh.call(ctx, http.MethodPost, gh.issuePath+"/labels", map[string][]string{"labels": {Label}}, nil); err != nil {
			return fmt.Errorf("adding %s: %w", Label, err)
		}
		fmt.Fprintf(opts.Stdout, "added %s\n", Label)
	case !r.ChangesExistingTests && has:
		err := gh.call(ctx, http.MethodDelete, gh.issuePath+"/labels/"+url.PathEscape(carried), nil, nil)
		// A 404 means someone removed it after the read, which is the end this run wanted.
		var refused statusError
		if err != nil && !(errors.As(err, &refused) && refused.code == http.StatusNotFound) {
			return fmt.Errorf("removing %s: %w", carried, err)
		}
		fmt.Fprintf(opts.Stdout, "removed %s\n", carried)
	case r.ChangesExistingTests:
		fmt.Fprintf(opts.Stdout, "%s already set\n", Label)
	default:
		fmt.Fprintln(opts.Stdout, "no existing test changed")
	}
	return nil
}

func changedTests(ctx context.Context, tiger, root, base string) (report, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, tiger, "changed-tests", "-C", root, "--format", "json", base)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return report{}, fmt.Errorf("tiger changed-tests: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var r report
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return report{}, fmt.Errorf("reading tiger's report: %w", err)
	}
	if r.Schema != reportSchema {
		return report{}, fmt.Errorf("tiger's report has schema %q, want %q", r.Schema, reportSchema)
	}
	return r, nil
}

// github calls GitHub's REST API directly rather than through gh, so the labeler needs no gh
// login in CI and tests fake GitHub with an HTTP server.
type github struct {
	opts      Options
	repoPath  string
	issuePath string
}

// label returns the name the pull request carries the label under, which GitHub matches
// without regard to case, or "" when it doesn't carry it.
func (g github) label(ctx context.Context) (string, error) {
	for page := 1; ; page++ {
		var labels []struct {
			Name string `json:"name"`
		}
		path := g.issuePath + "/labels?per_page=100&page=" + strconv.Itoa(page)
		if err := g.call(ctx, http.MethodGet, path, nil, &labels); err != nil {
			return "", fmt.Errorf("reading labels: %w", err)
		}
		for _, l := range labels {
			if strings.EqualFold(l.Name, Label) {
				return l.Name, nil
			}
		}
		if len(labels) < 100 {
			return "", nil
		}
	}
}

func (g github) call(ctx context.Context, method, path string, body, out any) error {
	var in io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		in = bytes.NewReader(b)
	}
	api := strings.TrimSuffix(g.opts.APIURL, "/")
	if api == "" {
		api = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, method, api+"/"+path, in)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.opts.Token)
	}
	client := g.opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return statusError{code: resp.StatusCode, msg: fmt.Sprintf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// statusError is GitHub answering a REST call with a status other than 2xx.
type statusError struct {
	code int
	msg  string
}

func (e statusError) Error() string { return e.msg }

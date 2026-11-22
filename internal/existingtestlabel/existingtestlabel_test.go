package existingtestlabel_test

// No exception needed: these tests run the command line through Run, which is the tool's
// surface. GitHub is an httptest fake of the issue labels REST API, and tiger is this test
// binary standing in for it (see TestMain).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kapetan-io/dispatch/internal/existingtestlabel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// The fake tiger prints fakeReport when the arguments are the ones the command must pass, and
// exits 2 with fakeFail on stderr when fakeFail is set, as tiger does when it can't report.
const (
	fakeTiger  = "EXISTINGTESTLABEL_FAKE_TIGER"
	fakeReport = "EXISTINGTESTLABEL_FAKE_REPORT"
	fakeFail   = "EXISTINGTESTLABEL_FAKE_FAIL"
	fakeArgs   = "EXISTINGTESTLABEL_FAKE_ARGS"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeTiger) != "1" {
		os.Exit(m.Run())
	}
	if msg := os.Getenv(fakeFail); msg != "" {
		fmt.Fprintln(os.Stderr, "tiger changed-tests:", msg)
		os.Exit(2)
	}
	if got := strings.Join(os.Args[1:], " "); got != os.Getenv(fakeArgs) {
		fmt.Fprintf(os.Stderr, "tiger: unexpected arguments %q\n", got)
		os.Exit(2)
	}
	fmt.Print(os.Getenv(fakeReport))
	os.Exit(0)
}

const changed = `{
  "schema": "tiger.changed-tests/v1",
  "base": {"ref": "origin/main", "commit": "b1", "merge_base": true},
  "head": {"commit": "h1", "dirty": false},
  "changes_existing_tests": true,
  "tests": [
    {"package": "example.com/shop/cart", "file": "cart/cart_test.go", "function": "TestTotal", "change": "modified"},
    {"package": "example.com/shop/cart", "file": "cart/tax_test.go", "function": "TestTax", "change": "deleted"}
  ]
}`

const unchanged = `{
  "schema": "tiger.changed-tests/v1",
  "base": {"ref": "origin/main", "commit": "b1", "merge_base": true},
  "head": {"commit": "h1", "dirty": false},
  "changes_existing_tests": false,
  "tests": []
}`

// issues fakes GitHub's labels on one repo's issues. Like GitHub, it answers 404 when removing
// a label the issue doesn't carry, and pages GET results. A readOnly token reads labels but
// gets 403 on writes, as a workflow token without pull-requests: write does. With
// repoLacksLabel the repo has no changes-existing-tests label yet, so reading it answers 404
// and adding it to an issue answers 403, since creating a label needs issues: write.
type issues struct {
	mu             sync.Mutex
	repo           string
	labels         map[int][]string
	writes         []string
	token          string
	readOnly       bool
	repoLacksLabel bool
}

func (s *issues) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	if name, ok := strings.CutPrefix(r.URL.Path, "/repos/"+s.repo+"/labels/"); ok && r.Method == http.MethodGet {
		if s.repoLacksLabel || name != existingtestlabel.Label {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"name": name})
		return
	}
	number, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/repos/"+s.repo+"/issues/"), "/labels")
	n, err := strconv.Atoi(number)
	if !ok || err != nil {
		http.NotFound(w, r)
		return
	}
	if s.readOnly && r.Method != http.MethodGet {
		http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
		return
	}
	switch {
	case r.Method == http.MethodGet && rest == "":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, per = max(page, 1), min(max(per, 1), 100)
		all := s.labels[n]
		from := min((page-1)*per, len(all))
		to := min(from+per, len(all))
		out := []map[string]string{}
		for _, l := range all[from:to] {
			out = append(out, map[string]string{"name": l})
		}
		json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost && rest == "":
		var body struct {
			Labels []string `json:"labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if s.repoLacksLabel && slices.Contains(body.Labels, existingtestlabel.Label) {
			http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
			return
		}
		for _, l := range body.Labels {
			if !slices.Contains(s.labels[n], l) {
				s.labels[n] = append(s.labels[n], l)
			}
		}
		s.writes = append(s.writes, "add "+strings.Join(body.Labels, ","))
		json.NewEncoder(w).Encode([]any{})
	case r.Method == http.MethodDelete && strings.HasPrefix(rest, "/"):
		name, err := url.PathUnescape(strings.TrimPrefix(rest, "/"))
		if err != nil || !slices.Contains(s.labels[n], name) {
			http.Error(w, `{"message":"Label does not exist"}`, http.StatusNotFound)
			return
		}
		s.labels[n] = slices.DeleteFunc(s.labels[n], func(l string) bool { return l == name })
		s.writes = append(s.writes, "remove "+name)
		json.NewEncoder(w).Encode([]any{})
	default:
		http.NotFound(w, r)
	}
}

func (s *issues) state(n int) ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.labels[n]), slices.Clone(s.writes)
}

type result struct {
	code   int
	stdout string
	stderr string
}

// run runs the command against a head checkout at root, a fake tiger that prints report (or
// fails with fail) and the fake GitHub behind gh.
func run(t *testing.T, gh *issues, root, report, fail string) result {
	t.Helper()
	server := httptest.NewServer(gh)
	t.Cleanup(server.Close)
	tiger, err := os.Executable()
	require.NoError(t, err)
	t.Setenv(fakeTiger, "1")
	t.Setenv(fakeReport, report)
	t.Setenv(fakeFail, fail)
	t.Setenv(fakeArgs, "changed-tests -C "+root+" --format json origin/main")
	var stdout, stderr bytes.Buffer
	code := existingtestlabel.Run(context.Background(),
		[]string{"-root", root, "-base", "origin/main", "-repo", "kapetan-io/shop", "-pr", "7", "-tiger", tiger},
		existingtestlabel.Options{Stdout: &stdout, Stderr: &stderr, APIURL: server.URL, Token: "t0ken"})
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func module(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/shop\n"), 0o644))
	return root
}

func TestLabelsPullRequestThatChangesExistingTests(t *testing.T) {
	gh := &issues{repo: "kapetan-io/shop", token: "t0ken", labels: map[int][]string{7: {"ai"}}}

	got := run(t, gh, module(t), changed, "")

	require.Equal(t, 0, got.code, got.stderr)
	labels, writes := gh.state(7)
	assert.Equal(t, []string{"ai", "changes-existing-tests"}, labels)
	assert.Equal(t, []string{"add changes-existing-tests"}, writes)
	assert.Contains(t, got.stdout, "modified example.com/shop/cart.TestTotal (cart/cart_test.go)")
	assert.Contains(t, got.stdout, "deleted example.com/shop/cart.TestTax (cart/tax_test.go)")
	assert.Contains(t, got.stdout, "added changes-existing-tests")
}

func TestNamesFixWhenRepoLacksLabel(t *testing.T) {
	gh := &issues{repo: "kapetan-io/shop", token: "t0ken", labels: map[int][]string{7: {"ai"}}, repoLacksLabel: true}

	got := run(t, gh, module(t), changed, "")

	assert.Equal(t, 2, got.code)
	assert.Contains(t, got.stderr, "gh label create changes-existing-tests --repo kapetan-io/shop")
	labels, writes := gh.state(7)
	assert.Equal(t, []string{"ai"}, labels)
	assert.Empty(t, writes)
}

func TestRemovesLabelOnceNoExistingTestChanges(t *testing.T) {
	gh := &issues{repo: "kapetan-io/shop", token: "t0ken", labels: map[int][]string{7: {"ai", "changes-existing-tests"}}}

	got := run(t, gh, module(t), unchanged, "")

	require.Equal(t, 0, got.code, got.stderr)
	labels, writes := gh.state(7)
	assert.Equal(t, []string{"ai"}, labels)
	assert.Equal(t, []string{"remove changes-existing-tests"}, writes)
	assert.Contains(t, got.stdout, "removed changes-existing-tests")
}

func TestLeavesPullRequestAloneWhenLabelAlreadyMatches(t *testing.T) {
	for _, test := range []struct {
		name   string
		labels []string
		report string
		want   string
	}{
		{name: "no test changed and no label", labels: []string{"ai"}, report: unchanged, want: "no existing test changed"},
		{name: "a test changed and the label is set", labels: []string{"ai", "changes-existing-tests"}, report: changed, want: "changes-existing-tests already set"},
	} {
		t.Run(test.name, func(t *testing.T) {
			gh := &issues{repo: "kapetan-io/shop", token: "t0ken", labels: map[int][]string{7: test.labels}}

			got := run(t, gh, module(t), test.report, "")

			require.Equal(t, 0, got.code, got.stderr)
			labels, writes := gh.state(7)
			assert.Equal(t, test.labels, labels)
			assert.Empty(t, writes)
			assert.Contains(t, got.stdout, test.want)
		})
	}
}

func TestMatchesLabelWithoutRegardToCase(t *testing.T) {
	for _, test := range []struct {
		name   string
		report string
		labels []string
		writes []string
	}{
		{name: "a test changed", report: changed, labels: []string{"Changes-Existing-Tests"}},
		{name: "no test changed", report: unchanged, labels: []string{}, writes: []string{"remove Changes-Existing-Tests"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			gh := &issues{repo: "kapetan-io/shop", token: "t0ken", labels: map[int][]string{7: {"Changes-Existing-Tests"}}}

			got := run(t, gh, module(t), test.report, "")

			require.Equal(t, 0, got.code, got.stderr)
			labels, writes := gh.state(7)
			assert.Equal(t, test.labels, labels)
			assert.Equal(t, test.writes, writes)
		})
	}
}

func TestFindsLabelPastFirstPage(t *testing.T) {
	var labels []string
	for i := range 100 {
		labels = append(labels, fmt.Sprintf("area-%d", i))
	}
	labels = append(labels, "changes-existing-tests")
	gh := &issues{repo: "kapetan-io/shop", token: "t0ken", labels: map[int][]string{7: labels}}

	got := run(t, gh, module(t), unchanged, "")

	require.Equal(t, 0, got.code, got.stderr)
	_, writes := gh.state(7)
	assert.Equal(t, []string{"remove changes-existing-tests"}, writes)
}

func TestSkipsRepoWithoutGoModule(t *testing.T) {
	gh := &issues{repo: "kapetan-io/shop", token: "t0ken", labels: map[int][]string{7: {"ai"}}}

	// The fake tiger would fail if run, as the real one does without a go.mod.
	got := run(t, gh, t.TempDir(), "", "no go.mod")

	require.Equal(t, 0, got.code, got.stderr)
	labels, writes := gh.state(7)
	assert.Equal(t, []string{"ai"}, labels)
	assert.Empty(t, writes)
	assert.Contains(t, got.stdout, "no go.mod")
}

func TestFailsWithoutTouchingLabelWhenNoReport(t *testing.T) {
	for _, test := range []struct {
		name    string
		report  string
		fail    string
		wantErr string
	}{
		{name: "tiger cannot report", fail: "cannot find revision origin/main", wantErr: "cannot find revision origin/main"},
		{name: "report is not JSON", report: "TestTotal modified", wantErr: "reading tiger's report"},
		{name: "report has another schema", report: `{"schema":"tiger.changed-tests/v2","changes_existing_tests":false}`, wantErr: `schema "tiger.changed-tests/v2"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			gh := &issues{repo: "kapetan-io/shop", token: "t0ken", labels: map[int][]string{7: {"changes-existing-tests"}}}

			got := run(t, gh, module(t), test.report, test.fail)

			assert.Equal(t, 2, got.code)
			assert.Contains(t, got.stderr, test.wantErr)
			labels, writes := gh.state(7)
			assert.Equal(t, []string{"changes-existing-tests"}, labels)
			assert.Empty(t, writes)
		})
	}
}

func TestFailsWhenGitHubRefuses(t *testing.T) {
	gh := &issues{repo: "kapetan-io/shop", token: "another-token", labels: map[int][]string{7: {"ai"}}}

	got := run(t, gh, module(t), changed, "")

	assert.Equal(t, 2, got.code)
	assert.Contains(t, got.stderr, "401")
}

func TestRequiresPullRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := existingtestlabel.Run(context.Background(), []string{"-base", "origin/main", "-repo", "kapetan-io/shop"},
		existingtestlabel.Options{Stdout: &stdout, Stderr: &stderr})

	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "-pr are required")
}

func TestFailsWhenTokenCannotWriteLabels(t *testing.T) {
	for _, test := range []struct {
		name   string
		labels []string
		report string
	}{
		{name: "adding", labels: []string{"ai"}, report: changed},
		{name: "removing", labels: []string{"ai", "changes-existing-tests"}, report: unchanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			gh := &issues{repo: "kapetan-io/shop", token: "t0ken", readOnly: true, labels: map[int][]string{7: test.labels}}

			got := run(t, gh, module(t), test.report, "")

			assert.Equal(t, 2, got.code)
			assert.Contains(t, got.stderr, "403")
			assert.Contains(t, got.stderr, "Resource not accessible by integration")
			labels, _ := gh.state(7)
			assert.Equal(t, test.labels, labels)
		})
	}
}

// workflow is the part of a GitHub Actions workflow file these tests read.
type workflow struct {
	On   map[string]any `yaml:"on"`
	Jobs map[string]struct {
		Permissions map[string]string `yaml:"permissions"`
		Uses        string            `yaml:"uses"`
		With        map[string]string `yaml:"with"`
		Steps       []struct {
			Run string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func readWorkflow(t *testing.T, name string) (workflow, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	require.NoError(t, err)
	var w workflow
	require.NoError(t, yaml.Unmarshal(data, &w))
	return w, string(data)
}

// The reusable workflow is what a managed repo calls, so its trigger, header and the
// command it runs are its surface.
func TestReusableWorkflowRunsLabelerForCaller(t *testing.T) {
	w, text := readWorkflow(t, "changes-existing-tests.yml")

	assert.Contains(t, w.On, "workflow_call")
	header, _, ok := strings.Cut(text, "\non:")
	require.True(t, ok)
	assert.Contains(t, header, "must grant pull-requests: write")
	require.Contains(t, w.Jobs, "label")
	var runs []string
	for _, step := range w.Jobs["label"].Steps {
		runs = append(runs, step.Run)
	}
	assert.Contains(t, strings.Join(runs, "\n"), "make -C .dispatch changes-existing-tests")
}

func TestRepoLabelsItsOwnPullRequests(t *testing.T) {
	w, _ := readWorkflow(t, "changes-existing-tests-pr.yml")

	// Without types, pull_request runs on opened, synchronize (every push) and reopened.
	require.Contains(t, w.On, "pull_request")
	if on, ok := w.On["pull_request"].(map[string]any); ok && on["types"] != nil {
		assert.Contains(t, on["types"], "synchronize")
	}
	var callers []string
	for name, job := range w.Jobs {
		if job.Uses == "./.github/workflows/changes-existing-tests.yml" {
			callers = append(callers, name)
			assert.Equal(t, "write", job.Permissions["pull-requests"])
		}
	}
	assert.Len(t, callers, 1)
}

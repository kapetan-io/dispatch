package dispatch_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLaunchReturnsALeaseItTookWhenTheWorkspaceFails(t *testing.T) {
	l := newLaunchHarness(t)
	l.writeFile("trees.json", `[]`)

	result := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", result["status"])
	assert.Equal(t, []string{"return " + l.tree + " --if-lease-holder ENG-1 --force"}, l.returns())
}

func TestLaunchKeepsALeaseFromAnEarlierRunWhenTheWorkspaceFails(t *testing.T) {
	l := newLaunchHarness(t)
	// The tree is already leased to ENG-1, so it may hold a worker's
	// uncommitted work that a forced return would discard.
	l.writeJSON("trees.json", []map[string]string{{"path": l.tree, "status": "leased", "lease_holder": "ENG-1"}})

	result := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", result["status"])
	assert.Empty(t, l.returns())
}

// launchHarness runs bin/dispatch-launch in a throwaway git repo against fake
// herdr and treehouse commands whose `herdr worktree open` always fails.
type launchHarness struct {
	t    *testing.T
	dir  string
	repo string
	tree string
	bin  string
}

func newLaunchHarness(t *testing.T) *launchHarness {
	dir := t.TempDir()
	l := &launchHarness{
		t:    t,
		dir:  dir,
		repo: filepath.Join(dir, "repo"),
		tree: filepath.Join(dir, "tree"),
		bin:  filepath.Join(dir, "bin"),
	}
	require.NoError(t, os.MkdirAll(l.repo, 0o755))
	require.NoError(t, os.MkdirAll(l.bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(l.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	l.git(l.repo, "init", "-q", "-b", "main")
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	l.git(l.repo, "worktree", "add", "-q", "--detach", l.tree)

	l.script("herdr", `case "$1 $2" in
"workspace list") echo '{"result":{"workspaces":[]}}' ;;
"worktree open") echo '{"error":{"code":"refused","message":"no"}}'; exit 1 ;;
"workspace close") echo '{"result":{"type":"ok"}}' ;;
*) exit 2 ;;
esac`)
	l.script("treehouse", `case "$1" in
status) cat "`+dir+`/trees.json" ;;
get) echo '{"path":"`+l.tree+`"}' ;;
return) echo "$*" >> "`+dir+`/returns.log" ;;
*) exit 2 ;;
esac`)
	return l
}

func (l *launchHarness) launch(ticket, branch string) map[string]string {
	launcher, err := filepath.Abs("bin/dispatch-launch")
	require.NoError(l.t, err)
	cmd := exec.Command(launcher, ticket, branch)
	cmd.Dir = l.repo
	cmd.Env = append(os.Environ(), "PATH="+l.bin+":"+os.Getenv("PATH"))
	out, _ := cmd.Output()
	var result map[string]string
	require.NoError(l.t, json.Unmarshal(out, &result))
	return result
}

func (l *launchHarness) returns() []string {
	b, err := os.ReadFile(filepath.Join(l.dir, "returns.log"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(l.t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (l *launchHarness) git(dir string, args ...string) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(l.t, err, string(out))
}

func (l *launchHarness) script(name, body string) {
	require.NoError(l.t, os.WriteFile(filepath.Join(l.bin, name), []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755))
}

func (l *launchHarness) writeFile(name, content string) {
	require.NoError(l.t, os.WriteFile(filepath.Join(l.dir, name), []byte(content), 0o644))
}

func (l *launchHarness) writeJSON(name string, v any) {
	b, err := json.Marshal(v)
	require.NoError(l.t, err)
	l.writeFile(name, string(b))
}

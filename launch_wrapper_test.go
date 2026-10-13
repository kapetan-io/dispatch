package dispatch_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLaunchWrapperNamesThePluginRootForTheGoLauncher(t *testing.T) {
	bin := t.TempDir()
	out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "dispatch"), "./cmd/dispatch").CombinedOutput()
	require.NoError(t, err, string(out))
	wrapper, err := filepath.Abs("bin/dispatch-launch")
	require.NoError(t, err)
	home := t.TempDir()

	// The root check runs before any git, herdr or treehouse call, so a plain
	// directory shows whether the wrapper set the root without touching either tool.
	cmd := exec.Command(wrapper, "ENG-1", "thrawn01/eng-1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "HOME="+home, "XDG_CONFIG_HOME="+home, "DISPATCH_PLUGIN_ROOT=")
	stdout, _ := cmd.Output()

	var result map[string]string
	require.NoError(t, json.Unmarshal(stdout, &result))
	assert.Equal(t, "not_a_repo", result["status"])
}

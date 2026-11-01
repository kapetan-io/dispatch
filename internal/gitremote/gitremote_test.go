package gitremote_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/gitremote"
)

func clone(t *testing.T, remotes map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q", "--initial-branch=main")
	for name, url := range remotes {
		run("remote", "add", name, url)
	}
	return dir
}

func TestReadNamesTheGitHubRepoOfARemote(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		url  string
		want string
	}{
		{name: "ssh", url: "git@github.com:acme/widgets.git", want: "acme/widgets"},
		{name: "sshWithoutSuffix", url: "git@github.com:acme/widgets", want: "acme/widgets"},
		{name: "https", url: "https://github.com/acme/widgets.git", want: "acme/widgets"},
		{name: "httpsWithUser", url: "https://token@github.com/acme/widgets", want: "acme/widgets"},
		{name: "sshScheme", url: "ssh://git@github.com/acme/widgets.git", want: "acme/widgets"},
		{name: "trailingSlash", url: "https://github.com/acme/widgets/", want: "acme/widgets"},
		{name: "otherHost", url: "git@gitlab.com:acme/widgets.git", want: ""},
		{name: "lookalikeHost", url: "https://notgithub.com/acme/widgets.git", want: ""},
		{name: "noName", url: "https://github.com/acme", want: ""},
		{name: "extraPath", url: "https://github.com/acme/widgets/tree/main", want: ""},
		{name: "localPath", url: "/srv/git/widgets.git", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := clone(t, map[string]string{"origin": test.url})

			got, err := gitremote.Read(context.Background(), environment.Process(), dir, "origin")

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestReadChoosesTheRemoteItIsGiven(t *testing.T) {
	t.Parallel()
	dir := clone(t, map[string]string{"origin": "git@github.com:me/widgets.git", "upstream": "git@github.com:acme/widgets.git"})

	origin, err := gitremote.Read(context.Background(), environment.Process(), dir, "origin")
	require.NoError(t, err)
	upstream, err := gitremote.Read(context.Background(), environment.Process(), dir, "upstream")
	require.NoError(t, err)

	assert.Equal(t, "me/widgets", origin)
	assert.Equal(t, "acme/widgets", upstream)
}

func TestReadOfAMissingRemoteIsNoRepoAndNoError(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		remotes map[string]string
		remote  string
	}{
		{name: "noRemotes", remotes: nil, remote: "origin"},
		{name: "namedRemoteMissing", remotes: map[string]string{"origin": "git@github.com:acme/widgets.git"}, remote: "upstream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := gitremote.Read(context.Background(), environment.Process(), clone(t, test.remotes), test.remote)

			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

func TestReadOfAMissingDirectoryIsAnError(t *testing.T) {
	t.Parallel()

	_, err := gitremote.Read(context.Background(), environment.Process(), filepath.Join(t.TempDir(), "gone"), "origin")

	require.ErrorContains(t, err, "read remote origin in")
}

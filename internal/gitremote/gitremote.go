// Package gitremote reads which GitHub repo a clone's remote points at, nothing else.
package gitremote

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/kapetan-io/dispatch/internal/environment"
)

// Read returns the owner/name of the GitHub repo the remote of the clone at dir points at. A
// remote that is missing or not on github.com is no repo and no error. Only a read git itself
// fails is an error.
func Read(ctx context.Context, env environment.Environment, dir, remote string) (string, error) {
	out, err := env.Command(ctx, "git", "-C", dir, "config", "--get", "remote."+remote+".url").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("read remote %s in %s: %w", remote, dir, err)
	}
	return repoOf(strings.TrimSpace(string(out))), nil
}

// repoOf is the owner/name in a github.com URL as git writes it (ssh, https, ssh://), "" for any other.
func repoOf(raw string) string {
	var path string
	if rest, ok := strings.CutPrefix(raw, "git@github.com:"); ok {
		path = rest
	} else if u, err := url.Parse(raw); err == nil && strings.EqualFold(u.Hostname(), "github.com") {
		path = strings.TrimPrefix(u.Path, "/")
	} else {
		return ""
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	return owner + "/" + name
}

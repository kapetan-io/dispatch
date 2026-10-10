// Package mark records which tests ran, so an eval can tell a full-suite run from a targeted one.
package mark

import (
	"os"
	"testing"
)

const path = "@LOG@"

func Ran(t *testing.T) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(t.Name() + "\n")
}

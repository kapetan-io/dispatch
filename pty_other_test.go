//go:build !darwin && !linux

package dispatch_test

import (
	"errors"
	"os"
)

// openPTY has no pseudo-terminal to open here, so the terminal tests skip.
func openPTY() (control, terminal *os.File, err error) { return nil, nil, errors.ErrUnsupported }

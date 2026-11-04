// Package lock keeps two dispatch runs from acting at the same time.
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

// ErrHeld means another dispatch run holds the lock.
var ErrHeld = errors.New("another dispatch run holds the lock")

// Lock is an exclusive flock on a file. The OS releases it when the holding
// process exits, so a crashed run never blocks the runs after it.
type Lock struct {
	file *os.File
}

// Acquire takes the lock at path without waiting. It returns ErrHeld when
// another process holds it. The file records the holder's pid for diagnosis.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("while opening lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("while locking %s: %w", path, err)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &Lock{file: f}, nil
}

// pollEvery is how often Wait tries a held lock again.
const pollEvery = 10 * time.Millisecond

// Wait takes the lock at path, waiting while another process holds it, until ctx ends. A
// blocking flock ignores a context, so Wait polls a non-blocking one. It waits on a held lock
// and never repeats a failed operation: any error but a held lock ends it.
func Wait(ctx context.Context, path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("while opening lock %s: %w", path, err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{file: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("while locking %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("while waiting for lock %s: %w", path, ctx.Err())
		case <-time.After(pollEvery):
		}
	}
}

// Release drops the lock.
func (l *Lock) Release() error {
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

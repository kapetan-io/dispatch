package herdr

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// workLog is what an adapter keeps while a worker starts: the unit it waits in between
// polls, the file it records screens to, and where it reports progress.
type workLog struct {
	pace     time.Duration
	trace    string
	progress io.Writer
}

func newWorkLog() workLog { return workLog{pace: time.Second, progress: io.Discard} }

func (w workLog) record(label, body string) {
	if w.trace == "" {
		return
	}
	f, err := os.OpenFile(w.trace, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "=== %s %s\n%s\n", time.Now().Format("15:04:05"), label, body)
}

func (w workLog) log(format string, args ...any) {
	_, _ = fmt.Fprintf(w.progress, "dispatch launch: "+format+"\n", args...)
}

func (w workLog) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

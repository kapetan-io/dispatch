package sessionlog_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// Package exception: parser input, where arbitrary byte sequences are the case and the CLI would hide them.
func TestParseCommandCutsLongArgumentsOfAnyBytesWithoutPanicking(t *testing.T) {
	for _, test := range []struct {
		name, args string
		wantLen    int
	}{
		{"InvalidContinuationBytes", strings.Repeat("\x80", 2000), 0},
		{"TwoByteRunes", strings.Repeat("é", 1000), 1024},
		{"ThreeByteRunes", strings.Repeat("€", 1000), 1023},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd, ok := sessionlog.ParseCommand("/a " + test.args)

			require.True(t, ok)
			assert.True(t, cmd.Cut)
			assert.LessOrEqual(t, len(cmd.Args), sessionlog.ArgsLimit)
			if test.wantLen > 0 {
				assert.Len(t, cmd.Args, test.wantLen)
			}
		})
	}
}

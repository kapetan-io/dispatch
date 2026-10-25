package billing_test

import (
	"bytes"
	"testing"

	"billing"
)

func run(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := billing.Run(args, &out, &errOut, dir)
	return out.String(), errOut.String(), code
}

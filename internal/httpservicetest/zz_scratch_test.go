package httpservicetest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/httpservice"
)

func TestScratchStale401(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	command := "n=$(cat " + counter + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " + counter + "; echo tok$n"
	release := make(chan struct{})
	var wg sync.WaitGroup
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow401": // held with tok1, answers 401 late
			<-release
			w.WriteHeader(401)
		default:
			if r.Header.Get("Authorization") == "Bearer tok1" {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(200)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	real := httpservice.New(map[string]config.HTTPService{"s": {BaseURL: srv.URL, TokenCommand: command}}, nil)
	_, err := real.Get(context.Background(), "s", "/prime") // runs cmd 1, gets 401, forgets
	require.Error(t, err)
	// prime forgot tok1; now fetch tok2 via a call, and hold a stale tok1 request
	_, err = real.Get(context.Background(), "s", "/ok")
	require.NoError(t, err) // runs cmd 2
	// stale: simulate B in flight with tok1 -> emulate by direct 401 late: B started before forget
	_ = release
	wg.Wait()
	b, _ := os.ReadFile(counter)
	t.Logf("runs=%s", b)
}

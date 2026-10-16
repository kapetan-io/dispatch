package httpservicetest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/httpservice"
)

// recorder serves status for every request and records the Authorization header of each.
type recorder struct {
	mu     sync.Mutex
	auth   []string
	status func(n int) int
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.auth = append(r.auth, req.Header.Get("Authorization"))
	n := len(r.auth)
	r.mu.Unlock()
	w.WriteHeader(r.status(n))
	_, _ = w.Write([]byte(`{}`))
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.auth...)
}

func TestARedirectIsNotFollowedSoTheTokenStaysOnTheServiceHost(t *testing.T) {
	other := &recorder{status: func(int) int { return 200 }}
	elsewhere := httptest.NewServer(other)
	defer elsewhere.Close()
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/stolen", http.StatusFound)
	}))
	defer service.Close()
	real := httpservice.New(map[string]config.HTTPService{
		"sentry": {BaseURL: service.URL, TokenCommand: "printf %s " + token},
	}, nil)

	_, getErr := real.Get(context.Background(), "sentry", "/go")
	_, postErr := real.Post(context.Background(), "sentry", "/go", []byte(`{}`))

	var unavailable *httpservice.Unavailable
	require.ErrorAs(t, getErr, &unavailable)
	assert.Equal(t, "GET sentry /go: returned 302", getErr.Error())
	require.ErrorAs(t, postErr, &unavailable)
	assert.Empty(t, other.seen())
}

func TestATokenCommandThatFailsMakesTheRequestUnavailableAndLeaksNothing(t *testing.T) {
	for _, test := range []struct{ name, command string }{
		{"ExitsNonZero", "echo " + token + " >&2; echo " + token + "; exit 3"},
		{"PrintsNothing", "true"},
		{"PrintsOnlyWhitespace", "echo '   '"},
		{"NotFound", "no-such-command-" + token},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &recorder{status: func(int) int { return 200 }}
			server := httptest.NewServer(service)
			defer server.Close()
			real := httpservice.New(map[string]config.HTTPService{
				"sentry": {BaseURL: server.URL, TokenCommand: test.command},
			}, nil)

			_, err := real.Get(context.Background(), "sentry", "/me")
			_, postErr := real.Post(context.Background(), "sentry", "/me", []byte(`{}`))

			var unavailable *httpservice.Unavailable
			require.ErrorAs(t, err, &unavailable)
			require.ErrorAs(t, postErr, &unavailable)
			assert.False(t, unavailable.Timeout)
			assert.NotContains(t, err.Error(), token)
			assert.NotContains(t, postErr.Error(), token)
			assert.Empty(t, service.seen())
		})
	}
}

func TestATokenCommandRunsOncePerServiceAndAgainAfterA401(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	command := "n=$(cat " + counter + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " + counter + "; echo tok$n"
	service := &recorder{status: func(n int) int {
		if n == 3 {
			return 401
		}
		return 200
	}}
	server := httptest.NewServer(service)
	defer server.Close()
	real := httpservice.New(map[string]config.HTTPService{
		"sentry": {BaseURL: server.URL, TokenCommand: command},
	}, nil)

	for range 2 {
		_, err := real.Get(context.Background(), "sentry", "/me")
		require.NoError(t, err)
	}
	_, err := real.Get(context.Background(), "sentry", "/me")
	require.Error(t, err)
	assert.Equal(t, "GET sentry /me: returned 401", err.Error())
	_, err = real.Post(context.Background(), "sentry", "/me", []byte(`{}`))
	require.NoError(t, err)

	assert.Equal(t, []string{"Bearer tok1", "Bearer tok1", "Bearer tok1", "Bearer tok2"}, service.seen())
	runs, err := os.ReadFile(counter)
	require.NoError(t, err)
	assert.Equal(t, "2\n", string(runs))
}

func TestAServiceThatIsDownIsUnavailableWithoutItsURL(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	real := httpservice.New(map[string]config.HTTPService{
		"sentry": {BaseURL: url, TokenCommand: "printf %s " + token},
	}, nil)

	_, err := real.Get(context.Background(), "sentry", "/me")

	var unavailable *httpservice.Unavailable
	require.ErrorAs(t, err, &unavailable)
	assert.False(t, unavailable.Timeout)
	assert.Equal(t, "GET sentry /me: the request failed", err.Error())
}

func TestASlowTokenCommandDelaysOnlyCallsToItsOwnService(t *testing.T) {
	srv := httptest.NewServer(&recorder{status: func(int) int { return 200 }})
	defer srv.Close()
	real := httpservice.New(map[string]config.HTTPService{
		"slow": {BaseURL: srv.URL, TokenCommand: "sleep 3; echo slow-token"},
		"fast": {BaseURL: srv.URL, TokenCommand: "echo fast-token"},
	}, nil)
	go func() { _, _ = real.Get(context.Background(), "slow", "/x") }()
	time.Sleep(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := real.Get(ctx, "fast", "/x")

	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second)
}

func TestACallerThatGivesUpWhileTheTokenCommandRunsIsNotHeldUntilItEnds(t *testing.T) {
	srv := httptest.NewServer(&recorder{status: func(int) int { return 200 }})
	defer srv.Close()
	real := httpservice.New(map[string]config.HTTPService{
		"slow": {BaseURL: srv.URL, TokenCommand: "sleep 5; echo tok"},
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := real.Get(ctx, "slow", "/x")

	var unavailable *httpservice.Unavailable
	require.ErrorAs(t, err, &unavailable)
	assert.False(t, unavailable.Timeout)
	assert.Less(t, time.Since(start), 3*time.Second)
}

package httpservicetest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/httpservice"
	"github.com/kapetan-io/dispatch/internal/httpservicetest"
)

// token is the token the "sentry" service of a subject sends. The "open" service has none.
const token = "s3cret-token-value"

// subject is a set of services under test: "sentry" with token and "open" with no token.
type subject struct {
	Services httpservice.Services
	// Serve sets the answer to a method and path on a service.
	Serve func(service, method, path string, status int, body string)
	// Stall makes that method and path answer after delay.
	Stall func(service, method, path string, delay time.Duration)
	// Requests lists what the services received, oldest first.
	Requests func() []httpservicetest.Request
}

// scenario is one situation with the outcome both the fake and the real adapter must give.
type scenario struct {
	Name string
	Run  func(t *testing.T, s subject)
}

// scenarios is the contract for httpservice.Services. The fake and the real adapter
// both run it in every test run, the real one against a local test server.
var scenarios = []scenario{
	{Name: "GetReturnsTheStatusAndTheJSONBody", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "GET", "/issues/?query=is:unresolved", 200, `[{"id":"1"}]`)

		res, err := s.Services.Get(context.Background(), "sentry", "/issues/?query=is:unresolved")

		require.NoError(t, err)
		assert.Equal(t, 200, res.Status)
		assert.JSONEq(t, `[{"id":"1"}]`, string(res.Body))
	}},
	{Name: "ABodyThatIsNotJSONComesBackAsAString", Run: func(t *testing.T, s subject) {
		s.Serve("open", "GET", "/status", 200, "all good")

		res, err := s.Services.Get(context.Background(), "open", "/status")

		require.NoError(t, err)
		assert.Equal(t, `"all good"`, string(res.Body))
	}},
	{Name: "TheServicetokenIsSentAsABearerHeader", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "GET", "/me", 200, `{}`)

		_, err := s.Services.Get(context.Background(), "sentry", "/me")

		require.NoError(t, err)
		reqs := s.Requests()
		require.Len(t, reqs, 1)
		assert.Equal(t, "Bearer "+token, reqs[0].Authorization)
	}},
	{Name: "AServiceWithNotokenGetsNoAuthorizationHeader", Run: func(t *testing.T, s subject) {
		s.Serve("open", "GET", "/me", 200, `{}`)

		_, err := s.Services.Get(context.Background(), "open", "/me")

		require.NoError(t, err)
		reqs := s.Requests()
		require.Len(t, reqs, 1)
		assert.Empty(t, reqs[0].Authorization)
	}},
	{Name: "AtokenTheServiceEchoesBackIsRedacted", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "GET", "/echo", 200, `{"you":"`+token+`"}`)

		res, err := s.Services.Get(context.Background(), "sentry", "/echo")

		require.NoError(t, err)
		assert.NotContains(t, string(res.Body), token)
		assert.JSONEq(t, `{"you":"[redacted]"}`, string(res.Body))
	}},
	{Name: "ATokenTheServiceEchoesBackWithJSONEscapesIsRedacted", Run: func(t *testing.T, s subject) {
		escaped := `\u0073` + token[1:]
		s.Serve("sentry", "GET", "/escaped", 200, `{"`+escaped+`":"`+escaped+`","list":["`+escaped+`"],"n":12345678901234567890}`)

		res, err := s.Services.Get(context.Background(), "sentry", "/escaped")

		require.NoError(t, err)
		assert.NotContains(t, string(res.Body), token)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(res.Body, &decoded))
		assert.NotContains(t, decoded, token)
		assert.Equal(t, "[redacted]", decoded["[redacted]"])
		assert.Equal(t, []any{"[redacted]"}, decoded["list"])
		assert.Contains(t, string(res.Body), "12345678901234567890")
	}},
	{Name: "ACancelledContextSendsNothing", Run: func(t *testing.T, s subject) {
		s.Serve("open", "GET", "/me", 200, `{}`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := s.Services.Get(ctx, "open", "/me")

		var unavailable *httpservice.Unavailable
		require.ErrorAs(t, err, &unavailable)
		assert.False(t, unavailable.Timeout)
		assert.Empty(t, s.Requests())
	}},
	{Name: "AServiceTheConfigDoesNotNameIsRefused", Run: func(t *testing.T, s subject) {
		_, err := s.Services.Get(context.Background(), "nowhere", "/x")

		require.ErrorIs(t, err, httpservice.ErrUnknownService)
		assert.Empty(t, s.Requests())
	}},
	{Name: "APathThatLeavesTheServiceIsRefusedBeforeAnyRequest", Run: func(t *testing.T, s subject) {
		for _, path := range []string{
			"issues", "//evil.example/x", "/../admin", "/issues/../../admin", "/%2e%2e/admin", "/a\\b", "/..%5Cadmin", "",
		} {
			_, err := s.Services.Get(context.Background(), "sentry", path)

			require.ErrorIs(t, err, httpservice.ErrPath, path)
		}
		assert.Empty(t, s.Requests())
	}},
	{Name: "ANon2xxAnswerIsUnavailableAndNamesNoBodyOrtoken", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "GET", "/boom", 503, "secret detail "+token)

		_, err := s.Services.Get(context.Background(), "sentry", "/boom")

		var unavailable *httpservice.Unavailable
		require.ErrorAs(t, err, &unavailable)
		assert.False(t, unavailable.Timeout)
		assert.Equal(t, "GET sentry /boom: returned 503", err.Error())
	}},
	{Name: "ACallerThatGivesUpMakesTheRequestUnavailableWithATimeout", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "GET", "/slow", 200, `{}`)
		s.Stall("sentry", "GET", "/slow", 5*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := s.Services.Get(ctx, "sentry", "/slow")

		var unavailable *httpservice.Unavailable
		require.ErrorAs(t, err, &unavailable)
		assert.True(t, unavailable.Timeout)
		assert.NotContains(t, err.Error(), token)
	}},
	{Name: "PostSendsTheBodyAsJSON", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "POST", "/issues/1/resolve", 201, `{"ok":true}`)

		res, err := s.Services.Post(context.Background(), "sentry", "/issues/1/resolve", []byte(`{"status":"resolved"}`))

		require.NoError(t, err)
		assert.Equal(t, 201, res.Status)
		reqs := s.Requests()
		require.Len(t, reqs, 1)
		assert.Equal(t, "POST", reqs[0].Method)
		assert.True(t, json.Valid([]byte(reqs[0].Body)))
		assert.JSONEq(t, `{"status":"resolved"}`, reqs[0].Body)
		assert.Equal(t, "Bearer "+token, reqs[0].Authorization)
	}},
	{Name: "APathThatReachesASiblingOfTheBaseURLPrefixIsRefused", Run: func(t *testing.T, s subject) {
		for _, path := range []string{"/../0evil", "/../../1/0", "/%2e%2e/0evil", "/x/../../0evil"} {
			_, err := s.Services.Get(context.Background(), "sentry", path)

			require.ErrorIs(t, err, httpservice.ErrPath, path)
		}
		assert.Empty(t, s.Requests())
	}},
	{Name: "APathThatStaysUnderTheBaseURLPrefixIsSent", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "GET", "/a/../issues/", 200, `{}`)

		_, err := s.Services.Get(context.Background(), "sentry", "/a/../issues/")

		require.NoError(t, err)
		assert.Len(t, s.Requests(), 1)
	}},
	{Name: "AnEmpty2xxBodyComesBackAsAnEmptyString", Run: func(t *testing.T, s subject) {
		s.Serve("open", "POST", "/hook", 204, "")

		res, err := s.Services.Post(context.Background(), "open", "/hook", []byte(`{}`))

		require.NoError(t, err)
		assert.Equal(t, 204, res.Status)
		assert.Equal(t, `""`, string(res.Body))
	}},
	{Name: "ANon2xxPostAnswerIsUnavailableWithoutATimeout", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "POST", "/hook", 500, token)

		_, err := s.Services.Post(context.Background(), "sentry", "/hook", []byte(`{}`))

		var unavailable *httpservice.Unavailable
		require.ErrorAs(t, err, &unavailable)
		assert.False(t, unavailable.Timeout)
		assert.Equal(t, "POST sentry /hook: returned 500", err.Error())
	}},
	{Name: "APostThatTimesOutIsUnavailableWithATimeout", Run: func(t *testing.T, s subject) {
		s.Serve("sentry", "POST", "/slow", 200, `{}`)
		s.Stall("sentry", "POST", "/slow", 5*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := s.Services.Post(ctx, "sentry", "/slow", []byte(`{}`))

		var unavailable *httpservice.Unavailable
		require.ErrorAs(t, err, &unavailable)
		assert.True(t, unavailable.Timeout)
	}},
}

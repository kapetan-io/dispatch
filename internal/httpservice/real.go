package httpservice

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/environment"
)

// tokenTimeout bounds a token_command, such as a password manager waiting on an unlock prompt.
const tokenTimeout = time.Minute

// Real calls the services over HTTP. A token_command runs the first time its service
// is called, and the token is kept in memory until the service answers 401.
type Real struct {
	services map[string]config.HTTPService
	client   *http.Client
	env      environment.Environment
	dir      string

	mu     sync.Mutex
	tokens map[string]string
	// commands holds one slot per service, so a slow token_command delays only calls to its own service.
	commands map[string]chan struct{}
}

// New returns the services config.toml names. It never follows a redirect, because one
// could carry the token to another host. A nil client means a plain http.Client.
func New(services map[string]config.HTTPService, client *http.Client) *Real {
	if client == nil {
		client = &http.Client{}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Real{services: services, client: &c, tokens: map[string]string{}, commands: map[string]chan struct{}{}}
}

// WithEnv runs each token_command in dir with env instead of the process's environment and
// working directory.
func (r *Real) WithEnv(env environment.Environment, dir string) *Real {
	r.env, r.dir = env, dir
	return r
}

func (r *Real) Get(ctx context.Context, service, p string) (Response, error) {
	return r.do(ctx, http.MethodGet, service, p, nil)
}

func (r *Real) Post(ctx context.Context, service, p string, body []byte) (Response, error) {
	return r.do(ctx, http.MethodPost, service, p, body)
}

func (r *Real) do(ctx context.Context, method, service, p string, body []byte) (Response, error) {
	svc, ok := r.services[service]
	if !ok {
		return Response{}, fmt.Errorf("%w: [http.%s] is not in config.toml", ErrUnknownService, service)
	}
	target, err := Resolve(svc.BaseURL, p)
	if err != nil {
		return Response{}, err
	}
	what := fmt.Sprintf("%s %s %s", method, service, p)
	if ctx.Err() != nil {
		// Nothing went out, so this is not a timeout that may have landed.
		return Response{}, &Unavailable{Reason: what + ": the caller gave up first"}
	}
	token, err := r.token(ctx, service, svc)
	if err != nil {
		return Response{}, &Unavailable{Reason: what + ": " + err.Error()}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return Response{}, &Unavailable{Reason: what + ": could not build the request"}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return Response{}, unavailable(ctx, what)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, unavailable(ctx, what)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		r.forget(service, token)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Response{}, &Unavailable{Reason: fmt.Sprintf("%s: returned %d", what, resp.StatusCode)}
	}
	return Response{Status: resp.StatusCode, Body: RedactedBody(token, data)}, nil
}

// unavailable describes a failed round trip without the URL a transport error carries,
// which could hold a query a rule built from a token.
func unavailable(ctx context.Context, what string) error {
	if ctx.Err() != nil {
		return &Unavailable{Reason: what + ": timed out", Timeout: true}
	}
	return &Unavailable{Reason: what + ": the request failed"}
}

// forget drops token so the next call runs token_command again. A 401 that arrives late for an
// older token leaves a newer one alone.
func (r *Real) forget(service, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tokens[service] == token {
		delete(r.tokens, service)
	}
}

// token runs the service's token_command once and keeps what it prints. A caller whose context
// ends while it waits for another call's command gives up without the token.
func (r *Real) token(ctx context.Context, service string, svc config.HTTPService) (string, error) {
	if svc.TokenCommand == "" {
		return "", nil
	}
	r.mu.Lock()
	if token, ok := r.tokens[service]; ok {
		r.mu.Unlock()
		return token, nil
	}
	slot, ok := r.commands[service]
	if !ok {
		slot = make(chan struct{}, 1)
		r.commands[service] = slot
	}
	r.mu.Unlock()
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	case <-ctx.Done():
		return "", fmt.Errorf("token_command did not finish in time")
	}
	r.mu.Lock()
	token, ok := r.tokens[service]
	r.mu.Unlock()
	if ok {
		return token, nil
	}
	cctx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	cmd := r.env.Command(cctx, "sh", "-c", svc.TokenCommand)
	cmd.Dir = r.dir
	// A child the command started would otherwise keep the output pipe open after the kill.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		// The command's own output and stderr may be the token, so the error says only how it ended.
		return "", fmt.Errorf("token_command failed: %v", exitReason(err))
	}
	token = strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("token_command printed nothing")
	}
	r.mu.Lock()
	r.tokens[service] = token
	r.mu.Unlock()
	return token, nil
}

func exitReason(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ProcessState.String()
	}
	return "it could not run"
}

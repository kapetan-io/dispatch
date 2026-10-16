// Package httpservicetest is an in-process stand-in for the services config.toml names.
// It refuses what the real adapter refuses (an unknown service, a path that leaves the
// service, a non-2xx answer, a timeout), keeps the token out of what it returns, and
// records each request with the Authorization header the real adapter would send.
package httpservicetest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/httpservice"
)

// Request is a request the fake received.
type Request struct {
	Method, Service, Path string
	// Authorization is the header the real adapter would send, empty for a service with no token.
	Authorization string
	Body          string
}

type answer struct {
	status int
	body   string
	delay  time.Duration
}

// Fake is httpservice.Services backed by memory. Tests tell it each service's token
// and what each path answers.
type Fake struct {
	services map[string]config.HTTPService

	mu       sync.Mutex
	tokens   map[string]string
	answers  map[string]answer
	requests []Request
}

// New returns a fake of the services config.toml names.
func New(services map[string]config.HTTPService) *Fake {
	return &Fake{services: services, tokens: map[string]string{}, answers: map[string]answer{}}
}

// Token sets what a service's token_command would print.
func (f *Fake) Token(service, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[service] = token
}

// Respond sets the answer to a method and path on a service.
func (f *Fake) Respond(service, method, path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[key(service, method, path)] = answer{status: status, body: body}
}

// Stall makes a method and path on a service answer only after delay, or never if
// the caller gives up first.
func (f *Fake) Stall(service, method, path string, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.answers[key(service, method, path)]
	a.delay = delay
	f.answers[key(service, method, path)] = a
}

// Requests lists what the fake received, oldest first.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

func key(service, method, path string) string { return service + " " + method + " " + path }

func (f *Fake) Get(ctx context.Context, service, path string) (httpservice.Response, error) {
	return f.do(ctx, "GET", service, path, nil)
}

func (f *Fake) Post(ctx context.Context, service, path string, body []byte) (httpservice.Response, error) {
	return f.do(ctx, "POST", service, path, body)
}

func (f *Fake) do(ctx context.Context, method, service, path string, body []byte) (httpservice.Response, error) {
	svc, ok := f.services[service]
	if !ok {
		return httpservice.Response{}, fmt.Errorf("%w: [http.%s] is not in config.toml", httpservice.ErrUnknownService, service)
	}
	if _, err := httpservice.Resolve(svc.BaseURL, path); err != nil {
		return httpservice.Response{}, err
	}
	what := fmt.Sprintf("%s %s %s", method, service, path)
	f.mu.Lock()
	token := f.tokens[service]
	req := Request{Method: method, Service: service, Path: path, Body: string(body)}
	if token != "" {
		req.Authorization = "Bearer " + token
	}
	f.requests = append(f.requests, req)
	a, ok := f.answers[key(service, method, path)]
	f.mu.Unlock()
	if !ok {
		a = answer{status: 404}
	}
	if a.delay > 0 {
		select {
		case <-time.After(a.delay):
		case <-ctx.Done():
			return httpservice.Response{}, &httpservice.Unavailable{Reason: what + ": timed out", Timeout: true}
		}
	}
	if a.status < 200 || a.status > 299 {
		return httpservice.Response{}, &httpservice.Unavailable{Reason: fmt.Sprintf("%s: returned %d", what, a.status)}
	}
	return httpservice.Response{Status: a.status, Body: httpservice.BodyJSON(httpservice.Redact(token, []byte(a.body)))}, nil
}

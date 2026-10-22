package httpservicetest_test

// Package exception: this package is a fake of ctx.http services, and holds the contract the fake and the real adapter both run.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/httpservice"
	"github.com/kapetan-io/dispatch/internal/httpservicetest"
)

func TestFakeKeepsTheHTTPServiceContract(t *testing.T) {
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			fake := httpservicetest.New(map[string]config.HTTPService{
				"sentry": {BaseURL: "https://sentry.test/api/0"},
				"open":   {BaseURL: "https://open.test"},
			})
			fake.Token("sentry", token)

			scenario.Run(t, subject{
				Services: fake,
				Serve: func(service, method, path string, status int, body string) {
					fake.Respond(service, method, path, status, body)
				},
				Stall:    fake.Stall,
				Requests: fake.Requests,
			})
		})
	}
}

func TestRealAdapterKeepsTheHTTPServiceContract(t *testing.T) {
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			var mu sync.Mutex
			var received []httpservicetest.Request
			answers := map[string]struct {
				status int
				body   string
				delay  time.Duration
			}{}
			handler := func(service, prefix string) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					path := r.URL.Path
					if r.URL.RawQuery != "" {
						path += "?" + r.URL.RawQuery
					}
					path = path[len(prefix):]
					mu.Lock()
					received = append(received, httpservicetest.Request{
						Method: r.Method, Service: service, Path: path, Authorization: r.Header.Get("Authorization"), Body: string(body),
					})
					a, ok := answers[service+" "+r.Method+" "+path]
					mu.Unlock()
					if !ok {
						a.status = 404
					}
					select {
					case <-time.After(a.delay):
					case <-r.Context().Done():
						return
					}
					w.WriteHeader(a.status)
					_, _ = w.Write([]byte(a.body))
				})
			}
			sentry := httptest.NewServer(handler("sentry", "/api/0"))
			defer sentry.Close()
			open := httptest.NewServer(handler("open", ""))
			defer open.Close()
			real := httpservice.New(map[string]config.HTTPService{
				"sentry": {BaseURL: sentry.URL + "/api/0", TokenCommand: "printf '%s\\n' " + token},
				"open":   {BaseURL: open.URL},
			}, nil)

			scenario.Run(t, subject{
				Services: real,
				Serve: func(service, method, path string, status int, body string) {
					mu.Lock()
					defer mu.Unlock()
					a := answers[service+" "+method+" "+path]
					a.status, a.body = status, body
					answers[service+" "+method+" "+path] = a
				},
				Stall: func(service, method, path string, delay time.Duration) {
					mu.Lock()
					defer mu.Unlock()
					a := answers[service+" "+method+" "+path]
					a.delay = delay
					answers[service+" "+method+" "+path] = a
				},
				Requests: func() []httpservicetest.Request {
					mu.Lock()
					defer mu.Unlock()
					return append([]httpservicetest.Request(nil), received...)
				},
			})
		})
	}
}

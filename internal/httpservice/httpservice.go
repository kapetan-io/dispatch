// Package httpservice reaches the services config.toml names under [http.<name>]. A
// rule names a service and a path and never sees the token: this package adds the
// Authorization header, keeps the request on the service's host, and keeps the token
// out of every response and error it returns.
package httpservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

// Response is a service's answer to a request that returned 2xx.
type Response struct {
	Status int `json:"status"`
	// Body is the response as JSON: the service's own JSON, or a JSON string of its text.
	Body json.RawMessage `json:"body"`
}

// Exchange is a request a rule's then waited on and what it got: the response, or the reason
// it got none. It never holds a token or the Authorization header.
type Exchange struct {
	Service string          `json:"service"`
	Path    string          `json:"path"`
	Status  int             `json:"status,omitempty"`
	Body    json.RawMessage `json:"body,omitempty"`
	Unknown string          `json:"unknown,omitempty"`
}

// Services is the set of services config.toml names.
type Services interface {
	Get(ctx context.Context, service, path string) (Response, error)
	Post(ctx context.Context, service, path string, body []byte) (Response, error)
}

// ErrUnknownService is a request for a service [http.<name>] doesn't name. It is a
// mistake in a rule, so it is never an outage.
var ErrUnknownService = errors.New("no such service")

// ErrPath is a path that would leave the service's scheme, host or base_url prefix.
// It is a mistake in a rule, so it is never an outage.
var ErrPath = errors.New("bad path")

// Unavailable is a request that didn't give a 2xx answer: a timeout, a connection
// failure or a status outside 2xx. The service may be fine on the next try.
type Unavailable struct {
	// Reason never holds a token or a response body.
	Reason string
	// Timeout is true when the request ran out of time, so a POST may have landed.
	Timeout bool
}

func (u *Unavailable) Error() string { return u.Reason }

// Resolve joins base with a path a rule gave and returns the URL to request. The path
// must start with "/" and the result must keep base's scheme, host and path prefix.
func Resolve(base, p string) (*url.URL, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("%w: %q must start with /", ErrPath, p)
	}
	if !utf8.ValidString(p) || strings.ContainsAny(p, "\\\r\n\t\x00") || strings.HasPrefix(p, "//") {
		return nil, fmt.Errorf("%w: %q is not a path on the service", ErrPath, p)
	}
	root, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("%w: base_url: %v", ErrPath, err)
	}
	full, err := url.Parse(strings.TrimRight(base, "/") + p)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrPath, p, err)
	}
	// An encoded backslash decodes to one, which some servers read as a slash.
	if strings.Contains(full.Path, "\\") {
		return nil, fmt.Errorf("%w: %q is not a path on the service", ErrPath, p)
	}
	prefix := strings.TrimRight(root.Path, "/")
	clean := path.Clean("/" + full.Path)
	if full.Scheme != root.Scheme || full.Host != root.Host || full.User != nil ||
		(clean != prefix && !strings.HasPrefix(clean, prefix+"/")) {
		return nil, fmt.Errorf("%w: %q leaves the service", ErrPath, p)
	}
	return full, nil
}

// RedactedBody turns a response body into JSON with token removed from every string and key. It
// decodes JSON first, because a service can spell the token with escapes such as \u0073 that
// only the decoder undoes.
func RedactedBody(token string, b []byte) json.RawMessage {
	if token == "" {
		return BodyJSON(b)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if !json.Valid(b) || dec.Decode(&v) != nil {
		text, _ := json.Marshal(strings.ReplaceAll(string(b), token, "[redacted]"))
		return text
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(redactValue(token, v))
	return json.RawMessage(bytes.TrimSpace(out.Bytes()))
}

func redactValue(token string, v any) any {
	switch v := v.(type) {
	case string:
		return strings.ReplaceAll(v, token, "[redacted]")
	case []any:
		for i := range v {
			v[i] = redactValue(token, v[i])
		}
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[strings.ReplaceAll(k, token, "[redacted]")] = redactValue(token, item)
		}
		return out
	}
	return v
}

// BodyJSON turns a response body into JSON: the body itself when it is JSON, and a
// JSON string of its text when it isn't.
func BodyJSON(b []byte) json.RawMessage {
	if json.Valid(b) && len(bytes.TrimSpace(b)) > 0 {
		return json.RawMessage(bytes.TrimSpace(b))
	}
	text, _ := json.Marshal(string(b))
	return text
}

package rules

import (
	"context"
	"errors"
	"fmt"

	"github.com/grafana/sobek"

	"github.com/kapetan-io/dispatch/internal/httpservice"
)

// Exchange is a request a then waited on and what it got.
type Exchange = httpservice.Exchange

func exchangeKey(service, path string) string { return service + " " + path }

// checkRequest throws for a service config.toml doesn't name and a path that would
// leave it, which are mistakes in the rule and not an outage.
func (c *call) checkRequest(method string, service, path sobek.Value) {
	name, ok := service.Export().(string)
	if !ok || name == "" {
		c.throwError("%s needs the name of a service from [http.<name>] as its first argument", method)
	}
	p, ok := path.Export().(string)
	if !ok {
		c.throwError("%s needs a path that starts with /, as its second argument", method)
	}
	svc, ok := c.state.Config.HTTP[name]
	if !ok {
		c.throwError("%s: %v: [http.%s] is not in config.toml", method, httpservice.ErrUnknownService, name)
	}
	if _, err := httpservice.Resolve(svc.BaseURL, p); err != nil {
		c.throwError("%s: %v", method, err)
	}
}

// httpAPI builds ctx.http.
func (c *call) httpAPI() *sobek.Object {
	o := c.rt.NewObject()
	_ = o.Set("get", func(call sobek.FunctionCall) sobek.Value {
		if c.phase != phaseThen {
			c.throwError("ctx.http.get can only be called in then, because it waits for the service. An if reads the response a then kept with ctx.http.last")
		}
		c.checkRequest("ctx.http.get", call.Argument(0), call.Argument(1))
		service, path := call.Argument(0).String(), call.Argument(1).String()
		if c.state.HTTP == nil {
			c.unknownRead("ctx.http has no services to call here. rules check calls none, so test the rule with rules test")
		}
		ctx, cancel := context.WithDeadline(context.Background(), c.deadline)
		defer cancel()
		res, err := c.state.HTTP.Get(ctx, service, path)
		if err != nil {
			if errors.Is(err, httpservice.ErrUnknownService) || errors.Is(err, httpservice.ErrPath) {
				c.throwError("ctx.http.get: %v", err)
			}
			c.exchanges = append(c.exchanges, Exchange{Service: service, Path: path, Unknown: err.Error()})
			c.unknownRead(err.Error())
		}
		ex := Exchange{Service: service, Path: path, Status: res.Status, Body: res.Body}
		c.exchanges = append(c.exchanges, ex)
		c.state.Kept[exchangeKey(service, path)] = ex
		c.read("http.get "+service+" "+path, res.Status)
		return c.responseValue(ex)
	})
	_ = o.Set("last", func(call sobek.FunctionCall) sobek.Value {
		c.checkRequest("ctx.http.last", call.Argument(0), call.Argument(1))
		service, path := call.Argument(0).String(), call.Argument(1).String()
		ex, ok := c.state.Kept[exchangeKey(service, path)]
		if !ok {
			c.unknownRead(fmt.Sprintf("no response is kept for %s %s since the last resync", service, path))
		}
		c.read("http.last "+service+" "+path, ex.Status)
		return c.responseValue(ex)
	})
	for _, spec := range Actions {
		if spec.Plugin == "http" {
			_ = o.Set(spec.Method, c.queueFn(spec))
		}
	}
	return o
}

// responseValue is the { status, body } a rule sees.
func (c *call) responseValue(ex Exchange) sobek.Value {
	parse, ok := sobek.AssertFunction(c.rt.Get("JSON").ToObject(c.rt).Get("parse"))
	if !ok {
		panic("JSON.parse is missing")
	}
	body, err := parse(sobek.Undefined(), c.rt.ToValue(string(ex.Body)))
	if err != nil {
		c.throwError("the response body is not JSON: %v", err)
	}
	o := c.rt.NewObject()
	_ = o.Set("status", ex.Status)
	_ = o.Set("body", body)
	return o
}

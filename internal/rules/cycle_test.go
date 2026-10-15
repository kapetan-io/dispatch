package rules_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/grafana/sobek"
)

type dyn struct{ rt *sobek.Runtime }

func (d dyn) Get(k string) sobek.Value {
	if k == "bad" {
		ctor := d.rt.Get("Unknown")
		o, err := d.rt.New(ctor, d.rt.ToValue("herdr unread"))
		if err != nil {
			panic(err)
		}
		panic(o)
	}
	if k == "x" {
		return d.rt.ToValue(42)
	}
	return nil
}
func (d dyn) Set(k string, v sobek.Value) bool { panic(d.rt.NewTypeError("read-only")) }
func (d dyn) Has(k string) bool                { return k == "x" }
func (d dyn) Delete(k string) bool             { return false }
func (d dyn) Keys() []string                   { return []string{"x"} }

func TestSpike(t *testing.T) {
	src := `
import { whenever } from "dispatch";
import type { Ticket } from "dispatch";
const f = (t: Ticket) => t?.x ?? 1;
whenever({ name: "a", each: "linear.ticket", if: (t: any) => { try { return t.bad } catch (e) { return e instanceof Unknown } }, then: (t: any) => ({...t, k: f(t)}) });
whenever({ name: "b", each: "x", if: (t: any) => { t.x = 3; return true }, then: () => {} });
whenever({ name: "loop", each: "x", if: () => { for(;;){} }, then: () => {} });
`
	res := api.Build(api.BuildOptions{
		Stdin:  &api.StdinOptions{Contents: src, Loader: api.LoaderTS, ResolveDir: "."},
		Bundle: true,
		Format: api.FormatIIFE,
		Target: api.ES2017,
		Write:  false,
		Plugins: []api.Plugin{{Name: "dispatch", Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: "^dispatch$"}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				return api.OnResolveResult{Path: "dispatch", Namespace: "dispatch"}, nil
			})
			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "dispatch"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				c := "export const whenever = globalThis.__d.whenever;"
				return api.OnLoadResult{Contents: &c, Loader: api.LoaderJS}, nil
			})
		}}},
	})
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors[0].Text)
	}
	code := string(res.OutputFiles[0].Contents)
	t.Log(code)
	prog, err := sobek.Compile("rules.js", code, false)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var fn map[string]sobek.Callable
	for i := 0; i < 1000; i++ {
		rt := sobek.New()
		fn = map[string]sobek.Callable{}
		d := rt.NewObject()
		_ = d.Set("whenever", func(c sobek.FunctionCall) sobek.Value {
			o := c.Arguments[0].ToObject(rt)
			f, _ := sobek.AssertFunction(o.Get("if"))
			fn[o.Get("name").String()] = f
			return sobek.Undefined()
		})
		_ = rt.Set("__d", d)
		_, err := rt.RunString(`class Unknown extends Error { constructor(r){ super(r); this.name="Unknown" } }; globalThis.Unknown = Unknown;`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rt.RunProgram(prog); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("per runtime+program: %v", time.Since(start)/1000)

	rt := sobek.New()
	rt.SetMaxCallStackSize(256)
	fn = map[string]sobek.Callable{}
	d := rt.NewObject()
	_ = d.Set("whenever", func(c sobek.FunctionCall) sobek.Value {
		o := c.Arguments[0].ToObject(rt)
		f, _ := sobek.AssertFunction(o.Get("if"))
		fn[o.Get("name").String()] = f
		return sobek.Undefined()
	})
	_ = rt.Set("__d", d)
	_, _ = rt.RunString(`class Unknown extends Error { constructor(r){ super(r); this.name="Unknown" } }; globalThis.Unknown = Unknown;`)
	if _, err := rt.RunProgram(prog); err != nil {
		t.Fatal(err)
	}
	obj := rt.NewDynamicObject(dyn{rt})
	v, err := fn["a"](nil, obj)
	fmt.Println("a:", v, err)
	_, err = fn["b"](nil, obj)
	fmt.Println("b err:", err)
	timer := time.AfterFunc(100*time.Millisecond, func() { rt.Interrupt("timeout") })
	_, err = fn["loop"](nil, obj)
	timer.Stop()
	fmt.Printf("loop err: %T %v\n", err, err)
}

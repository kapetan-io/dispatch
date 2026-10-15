package rules

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/grafana/sobek/ast"
	"github.com/grafana/sobek/file"
	"github.com/grafana/sobek/parser"
)

// lintSource checks what can be checked without running a rule: every queued
// action carries a once key, and nothing asynchronous or dynamic is used. The
// source is the bundled script, so every rule file is covered.
func lintSource(src string) []string {
	prog, err := parser.ParseFile(nil, "rules.js", src, 0)
	if err != nil {
		return []string{"while parsing: " + firstLine(err.Error())}
	}
	l := &linter{src: src}
	l.walk(reflect.ValueOf(prog))
	return l.problems
}

type linter struct {
	src      string
	problems []string
}

func (l *linter) problem(idx file.Idx, format string, args ...any) {
	line := 1 + strings.Count(l.src[:max(0, min(int(idx)-1, len(l.src)))], "\n")
	l.problems = append(l.problems, fmt.Sprintf("bundled rules line %d: %s", line, fmt.Sprintf(format, args...)))
}

var nodeType = reflect.TypeFor[ast.Node]()

// walk visits every node reachable from v through exported fields.
func (l *linter) walk(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		if v.Kind() == reflect.Interface || v.Type().Implements(nodeType) {
			l.visit(v)
		}
		l.walk(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				l.walk(v.Field(i))
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			l.walk(v.Index(i))
		}
	}
}

func (l *linter) visit(v reflect.Value) {
	switch n := v.Interface().(type) {
	case *ast.FunctionLiteral:
		if n.Async {
			l.problem(n.Idx0(), "async functions are not allowed in a rule. A rule is plain code that returns")
		}
		if n.Generator {
			l.problem(n.Idx0(), "generators are not allowed in a rule")
		}
	case *ast.ArrowFunctionLiteral:
		if n.Async {
			l.problem(n.Idx0(), "async functions are not allowed in a rule. A rule is plain code that returns")
		}
	case *ast.AwaitExpression:
		l.problem(n.Idx0(), "await is not allowed in a rule")
	case *ast.YieldExpression:
		l.problem(n.Idx0(), "yield is not allowed in a rule")
	case *ast.DynamicImportExpression:
		l.problem(n.Idx0(), "import() is not allowed in a rule")
	case *ast.Identifier:
		switch n.Name.String() {
		case "Promise", "eval", "Function":
			l.problem(n.Idx0(), "%s is not available in a rule", n.Name)
		}
	case *ast.CallExpression:
		l.checkQueueCall(n)
	}
}

// checkQueueCall refuses a ctx.<plugin>.queue<Action>(...) call whose last
// argument is an object literal with no once key. A last argument that isn't a
// literal can't be told, and the call still fails when it runs.
func (l *linter) checkQueueCall(n *ast.CallExpression) {
	dot, ok := n.Callee.(*ast.DotExpression)
	if !ok || !strings.HasPrefix(dot.Identifier.Name.String(), "queue") || len(n.ArgumentList) == 0 {
		return
	}
	if !isAction(dot.Identifier.Name.String()) {
		return
	}
	last, ok := n.ArgumentList[len(n.ArgumentList)-1].(*ast.ObjectLiteral)
	if !ok {
		return
	}
	for _, p := range last.Value {
		switch p := p.(type) {
		case *ast.PropertyShort:
			if p.Name.Name.String() == "once" {
				return
			}
		case *ast.PropertyKeyed:
			if s, ok := p.Key.(*ast.StringLiteral); ok && s.Value.String() == "once" {
				return
			}
			if id, ok := p.Key.(*ast.Identifier); ok && id.Name.String() == "once" {
				return
			}
		case *ast.SpreadElement:
			return
		}
	}
	l.problem(n.Idx0(), "%s is queued with no once key. Every action needs once: [topic, ...values]", dot.Identifier.Name)
}

func isAction(method string) bool {
	for _, a := range Actions {
		if a.Method == method {
			return true
		}
	}
	return false
}

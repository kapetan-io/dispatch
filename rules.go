package dispatch

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/world"
)

// shippedRules are the rules `dispatch rules init` writes, and the rules the
// tests run, so the repo's rules/ directory is the one place they live.
//
//go:embed rules
var shippedRules embed.FS

const rulesUsage = `usage: dispatch rules init [dir]
       dispatch rules check [dir]
       dispatch rules test [dir]

  init   write the shipped rules to dir, which defaults to [dispatch].rules in the config. It
         refuses to overwrite a file that exists
  check  lint the rules, run one cycle against the world as it is now, and print what each rule
         wants. Exits 1 on any finding
  test   run the *.test.ts scenarios in the same runtime a live run uses. Exits 1 when one fails`

const rulesEntry = "dispatch.ts"

// typesDir holds the generated declarations. They can't sit beside dispatch.ts,
// because tsc ignores a dispatch.d.ts that has a dispatch.ts next to it.
const typesDir = "types"

func rulesCommand(ctx context.Context, opts Options, args []string) error {
	if len(args) == 0 || len(args) > 2 {
		_, _ = fmt.Fprintln(opts.Stderr, rulesUsage)
		return errors.New("rules takes init, check or test")
	}
	dir := ""
	if len(args) == 2 {
		dir = args[1]
	}
	switch args[0] {
	case "init":
		return rulesInit(opts, dir)
	case "check":
		return rulesCheck(ctx, opts, dir)
	case "test":
		return rulesTest(opts, dir)
	}
	_, _ = fmt.Fprintln(opts.Stderr, rulesUsage)
	return fmt.Errorf("unknown rules command %q", args[0])
}

// rulesDir is where the rules live: the argument, else Options.RulesDir, else
// the config's [dispatch].rules.
func rulesDir(opts Options, arg string, cfg *config.Config) (string, error) {
	switch {
	case arg != "":
		return opts.abs(opts.Env.ExpandHome(arg)), nil
	case opts.RulesDir != "":
		return opts.abs(opts.RulesDir), nil
	case cfg != nil:
		return opts.abs(cfg.Dispatch.Rules), nil
	}
	return opts.Env.ExpandHome("~/Development/dispatch/rules"), nil
}

func rulesInit(opts Options, arg string) error {
	var cfg *config.Config
	if c, err := config.Load(opts.ConfigPath, opts.Env); err == nil {
		cfg = &c
	} else if _, statErr := os.Stat(opts.ConfigPath); statErr == nil {
		return err
	}
	dir, err := rulesDir(opts, arg, cfg)
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	err = fs.WalkDir(shippedRules, "rules", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := shippedRules.ReadFile(path)
		files[strings.TrimPrefix(path, "rules/")] = b
		return err
	})
	if err != nil {
		return err
	}
	rulesTable := config.ShippedRules()
	if cfg != nil {
		for k, v := range cfg.Rules {
			rulesTable[k] = v
		}
	}
	files[typesDir+"/dispatch.d.ts"] = []byte(rules.Declarations(rulesTable))
	files[typesDir+"/testing.d.ts"] = []byte(rules.TestingDeclarations())
	files["config.schema.json"] = config.JSONSchema()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var exist []string
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			exist = append(exist, name)
		}
	}
	if len(exist) > 0 {
		shown := exist
		if len(shown) > 5 {
			shown = append(slices.Clone(shown[:5]), fmt.Sprintf("and %d more", len(exist)-5))
		}
		return fmt.Errorf("%s already has %s. rules init never overwrites a file, so move them or pick another directory", dir, strings.Join(shown, ", "))
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(opts.Stdout, "wrote %d files to %s\nadd the [rules] keys in %s to your config.toml, then run dispatch rules check\n", len(names), dir, filepath.Join(dir, "config.example.toml"))
	return nil
}

func rulesCheck(ctx context.Context, opts Options, arg string) error {
	cfg, err := config.Load(opts.ConfigPath, opts.Env)
	if err != nil {
		return err
	}
	dir, err := rulesDir(opts, arg, &cfg)
	if err != nil {
		return err
	}
	var findings []string
	// The types follow the config, so a rule reading a [rules] key the file lacks
	// fails in tsc as well as when it runs.
	if err := writeTypes(dir, cfg); err != nil {
		return err
	}
	loaded, err := rules.Load(dir, rulesEntry, opts.ruleTimeouts())
	if err != nil {
		var lerr *rules.LoadError
		if errors.As(err, &lerr) {
			for _, p := range lerr.Problems {
				_, _ = fmt.Fprintln(opts.Stdout, "finding:", p)
			}
			return fmt.Errorf("%d problems found, so no rule loaded", len(lerr.Problems))
		}
		return err
	}

	key, err := (&keyLookup{}).find(ctx, opts, cfg)
	if err != nil {
		return err
	}
	lin := opts.Linear(key)
	viewer, err := lin.Viewer(ctx)
	if err != nil {
		return err
	}
	w, err := world.Read(ctx, world.Sources{
		Linear: lin, Herdr: opts.Herdr, Treehouse: opts.Treehouse, PullRequests: opts.PullRequests, Env: opts.Env,
		StateDir: opts.StateDir, Stderr: opts.Stderr, Tolerant: true,
	}, world.ScopeOf(cfg), opts.Clock.Now())
	if err != nil {
		return err
	}
	state := rules.FromWorld(w, cfg, nil, viewer.Name)
	// A check acts on nothing, and a token_command can prompt to unlock a password manager,
	// so it calls only services a caller gave it. A get otherwise reads as an unknown.
	state.HTTP = opts.HTTP
	result := loaded.Cycle(state, nil)

	printCheck(opts, cfg, state, loaded, result)
	for _, t := range result.Throws {
		how := "threw"
		if t.Timeout {
			how = "ran out of time"
		}
		findings = append(findings, fmt.Sprintf("%s %s in its %s for %s: %s", t.Rule, how, t.Phase, t.Subject, t.Error))
	}
	findings = append(findings, typeCheck(ctx, opts, dir)...)
	for _, f := range findings {
		_, _ = fmt.Fprintln(opts.Stdout, "finding:", f)
	}
	if len(findings) > 0 {
		return fmt.Errorf("%d problems found", len(findings))
	}
	return nil
}

// writeTypes writes the generated declarations the rules import.
func writeTypes(dir string, cfg config.Config) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, text := range map[string]string{
		typesDir + "/dispatch.d.ts": rules.Declarations(cfg.Rules),
		typesDir + "/testing.d.ts":  rules.TestingDeclarations(),
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if old, err := os.ReadFile(path); err == nil && string(old) == text {
			continue
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// typeCheck runs tsc over the rules when it is installed. It is how a typo in
// a field name or a mixed-up type is found without waiting for the record to exist.
// It hands tsc a config of its own, because the flags that name files can't be
// mixed with a tsconfig.json on every tsc version, and "node" module resolution
// is gone from the newest ones.
func typeCheck(ctx context.Context, opts Options, dir string) []string {
	tsc, err := opts.Env.LookPath("tsc")
	if err != nil {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return []string{"tsc: " + err.Error()}
	}
	files := []string{filepath.Join(abs, typesDir, "dispatch.d.ts"), filepath.Join(abs, typesDir, "testing.d.ts"), filepath.Join(abs, rulesEntry)}
	tests, _ := filepath.Glob(filepath.Join(abs, "*.test.ts"))
	files = append(files, tests...)
	conf, err := json.Marshal(map[string]any{
		"compilerOptions": map[string]any{
			"strict": true, "noEmit": true, "target": "ES2017", "lib": []string{"ES2017"}, "module": "ESNext",
			"moduleResolution": "bundler", "skipLibCheck": true, "types": []string{},
		},
		"files": files,
	})
	if err != nil {
		return []string{"tsc: " + err.Error()}
	}
	tmp, err := os.MkdirTemp(opts.Env.Get("TMPDIR"), "dispatch-tsc-")
	if err != nil {
		return []string{"tsc: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	confPath := filepath.Join(tmp, "tsconfig.json")
	if err := os.WriteFile(confPath, conf, 0o644); err != nil {
		return []string{"tsc: " + err.Error()}
	}
	cmd := opts.Env.Command(ctx, tsc, "-p", confPath)
	cmd.Dir = abs
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	var findings []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			findings = append(findings, "tsc: "+line)
		}
	}
	if len(findings) == 0 {
		findings = []string{"tsc failed: " + err.Error()}
	}
	return findings
}

func printCheck(opts Options, cfg config.Config, state *rules.State, loaded *rules.Rules, result rules.Result) {
	tw := tabwriter.NewWriter(opts.Stdout, 0, 0, 2, ' ', 0)
	defer func() { _ = tw.Flush() }()
	statuses := cfg.Plugins.Linear.Statuses
	for _, line := range state.Summary(statuses) {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", line.Name, line.Text)
	}
	_, _ = fmt.Fprintln(tw, "\t")
	for _, def := range loaded.Defs() {
		desc := ""
		switch {
		case def.Shadow:
			desc = "shadow: reports only"
			if would := wantSummary(result.Shadow, def.Name); would != "" {
				desc = "shadow: would " + would
			}
		default:
			desc = "wants nothing now"
			if wants := wantSummary(result.Wants, def.Name); wants != "" {
				desc = "wants " + wants
			}
		}
		if waiting := waitingSummary(result, def.Name); waiting != "" {
			desc += "; " + waiting
		}
		if held := heldSummary(result, def.Name); held != "" {
			desc += "; " + held
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", def.Name, def.Kind(), def.Target(), desc)
	}
}

// wantSummary names what a rule wants now, such as "launch now for ENG-12, ENG-15".
func wantSummary(wants []rules.Want, rule string) string {
	by := map[string][]string{}
	var order []string
	for _, w := range wants {
		if w.Rule != rule {
			continue
		}
		verb := w.Receipt
		if _, ok := by[verb]; !ok {
			order = append(order, verb)
		}
		by[verb] = append(by[verb], w.Subject)
	}
	var parts []string
	for _, verb := range order {
		parts = append(parts, fmt.Sprintf("%s now for %s", verb, strings.Join(by[verb], ", ")))
	}
	return strings.Join(parts, "; ")
}

// waitingSummary names what a rule wants that waits, such as "waiting: launch for ENG-5 (4 of 4 in progress, 1st in line)".
func waitingSummary(result rules.Result, rule string) string {
	var waiting []string
	for _, w := range result.Waiting {
		if w.Rule == rule {
			waiting = append(waiting, fmt.Sprintf("%s for %s (%s)", w.Receipt, w.Subject, w.Reason))
		}
	}
	if len(waiting) == 0 {
		return ""
	}
	return "waiting: " + strings.Join(waiting, ", ")
}

func heldSummary(result rules.Result, rule string) string {
	var held []string
	for _, h := range result.Held {
		if h.Rule == rule {
			held = append(held, h.Subject)
		}
	}
	if len(held) == 0 {
		return ""
	}
	return "held for " + strings.Join(held, ", ")
}

func rulesTest(opts Options, arg string) error {
	var cfgPtr *config.Config
	if loaded, err := config.Load(opts.ConfigPath, opts.Env); err == nil {
		cfgPtr = &loaded
	}
	dir, err := rulesDir(opts, arg, cfgPtr)
	if err != nil {
		return err
	}
	summary, err := rules.RunScenarios(dir, opts.ruleTimeouts())
	for _, line := range summary.Lines {
		_, _ = fmt.Fprintln(opts.Stdout, line)
	}
	if err != nil {
		return err
	}
	if summary.Failed > 0 {
		return fmt.Errorf("%d of %d scenarios failed", summary.Failed, summary.Passed+summary.Failed)
	}
	return nil
}

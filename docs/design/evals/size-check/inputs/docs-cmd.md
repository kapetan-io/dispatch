# `duh docs` Subcommand Blueprint

Tracking: ENG-72 — https://linear.app/kapetan-io/issue/ENG-72/duh-cli-docs-subcommand

## Objective

Add a `docs` subcommand that renders an OpenAPI spec as browsable API documentation
using [ReDoc](https://github.com/Redocly/redoc), with two modes:

- **`duh docs serve [spec]`** — a local web server that renders the spec and
  auto-reloads the browser whenever the spec file changes on disk. This is the
  contract author's live authoring loop: edit the spec, see it rendered instantly,
  no manual rebuild or refresh.
- **`duh docs export [spec]`** — writes a single, self-contained HTML file that
  renders offline with no build step, for handing to non-engineers and partners.

`docs` is a *viewer*, not a validator. It renders any parseable OpenAPI document and
deliberately does **not** require DUH-RPC lint compliance (that is `duh lint`'s job).

## Mental Model

`duh docs` is a live-preview + publish tool for an OpenAPI spec:

- **`serve`** is the authoring loop — start it once, edit the spec, and the browser
  updates itself on every save.
- **`export`** is the snapshot you share — one portable `.html` file that opens
  anywhere, forever, with no tooling and no network.

The two modes share a renderer (embedded ReDoc) but differ in lifecycle: `serve` is
a long-running process terminated with Ctrl-C; `export` is a one-shot build step.

## Personas

- **Contract author** (primary, per `docs/CONTEXT.md`) — owns an `openapi.yaml` and
  runs `serve` while authoring. Also runs `export` to produce a shareable artifact.
- **Non-engineers and partners** (export recipients) — receive the exported HTML and
  open it directly. They run no tooling, tolerate no build step, and may be offline.
  This audience is the reason the export must be fully self-contained.

## Correctness Constraints

### State Invariants

- **INV-1 — The served spec is never a half-written or truncated file rendered as
  valid.** Editors save in multiple syscalls or via atomic rename, so a watch event
  can fire mid-write. The `/openapi.yaml` endpoint must always return either the
  last successfully-parsed spec or an explicit parse error — never partial bytes
  presented as a valid document.
  *Violation attempt:* a save event fires while the file is half-written.
  *System response:* debounce the event, re-read and re-parse from disk; only swap
  the served bytes when parsing succeeds; on failure keep the last-known-good bytes
  and surface the parse error.

- **INV-2 — An export never leaves a truncated output file.** If rendering or writing
  fails partway, a previously-good `docs.html` must remain intact and no partial file
  may be left in its place.
  *Violation attempt:* the process is killed, or an I/O error occurs, mid-write.
  *System response:* render to a temp file in the target directory and atomically
  rename it into place only after a complete successful write.

### Behavioral Constraints

- **BC-1 — Never fetch anything over the network at serve, view, or export time.**
  ReDoc and all assets are embedded; the served and exported HTML contain no external
  `<script src>` / `<link href>` references to CDNs. (Verifiable by grepping the
  output for `http://` / `https://` asset references.) This is what makes the export
  work offline for partners and the serve work inside locked-down remote workstations.

- **BC-2 — Never crash the serve process on an invalid spec save.** A save that fails
  to parse leaves the previous render up, surfaces the error, and recovers
  automatically on the next valid save.

- **BC-3 — Never clobber an existing good export with garbage on failure.** (Enforced
  structurally by INV-2's atomic write.)

<!-- Architectural constraint (no user-facing story): the debounce window collapses
     rapid multi-syscall saves into a single reload so the browser is never asked to
     render a transient state. -->

## Acceptance Criteria

Each is mechanically verifiable through the CLI surface (`RunCmd` + an HTTP client for
serve, file inspection for export).

1. `duh docs export spec.yaml` exits `0` and writes a single HTML file containing both
   the inlined spec and the inlined ReDoc bundle; the file contains no external asset
   `<script src>` / `<link href>` references (grep-verifiable).
2. The exported HTML renders with no network available (asserted by the inlined spec
   content and the ReDoc bootstrap being present in the file).
3. `duh docs export` on an unparseable spec exits `2`, prints an error, and does not
   create or modify the output file.
4. `duh docs export` over an existing `docs.html` replaces it silently and never leaves
   a truncated file when the write fails.
5. `duh docs serve spec.yaml --port 0` binds a free port, prints the resolved URL to
   stdout; `GET /` returns HTML referencing the embedded ReDoc, `GET /openapi.yaml`
   returns the current spec bytes, and `GET /events` is the SSE endpoint the browser
   subscribes to for reload notifications.
6. After the spec file changes on disk, a client connected to the SSE endpoint receives
   a reload event within the debounce window.
7. While serving, replacing the spec with invalid content does not crash the server;
   the spec endpoint surfaces the parse error and a later valid save recovers.
8. `duh docs serve` on a missing or unparseable spec at startup exits `2`, prints the
   error, and binds no port. Serve requires a spec that parses before it will start
   (symmetric with `export`); recovery from a bad *save* mid-session is AC #7, which is
   distinct from this start-time gate.
9. `duh docs serve` shuts down cleanly on context cancellation / interrupt and exits `0`.
10. On a Google Cloud Workstation (`WEB_HOST` set), `serve` prints
    `https://<port>-<WEB_HOST>` and does not attempt to open a browser.

## Scope

### In Scope

- `duh docs serve [spec]` and `duh docs export [spec]`; spec argument defaults to
  `openapi.yaml` (consistent with `lint`, `generate`, `init`, `add`).
- Embedded ReDoc renderer (no network).
- SSE-driven browser auto-reload on file change (in-place spec re-render).
- Atomic, self-contained HTML export.
- Flags: `--port` (serve), `-o/--output` (export), `--public-url` and `--no-open`
  (serve).
- Remote URL resolution for Google Cloud Workstations; OS browser auto-open on local.

### Out of Scope / Non-Goals

- **Alternative renderers** (Swagger UI, Stoplight) — ReDoc only.
- **Theming / branding / custom CSS** — default ReDoc styling only.
- **Auth on the served docs** — it is a localhost / dev-workstation tool.
- **Hosting / deployment / publishing** (GitHub Pages, S3) — `export` produces a file;
  where it goes is the user's concern.
- **Multiple specs / multi-file doc sites** — one spec → one doc view.
- **Spec bundling / dereferencing of external `$ref`s** — the spec is rendered as-is.
  ReDoc resolves in-document `$ref`s (`#/components/...`) itself.

## Functional

### CLI shape

Two cobra sub-subcommands under a `docs` parent command, registered in `run_cmd.go`
alongside the existing commands. Sub-subcommands (not a single `docs` with
`--serve`/`--export` mode flags) because the modes have different flags, different
lifecycle, and different exit semantics.

```
duh docs serve  [spec]   # long-running; default spec openapi.yaml
duh docs export [spec]    # one-shot;     default spec openapi.yaml
```

| Flag | Mode | Default | Meaning |
|------|------|---------|---------|
| `--port` | serve | `8080` | Port to bind. `0` selects a free port; the resolved URL is printed. |
| `--public-url` | serve | (none) | Override the printed URL for any remote env; suppresses auto-open. |
| `--no-open` | serve | `false` | Suppress local browser auto-open. |
| `-o, --output` | export | `docs.html` | Output file path (`-o` matches `generate`). |

### URL resolution and browser opening (serve)

Resolved in order:

1. `--public-url` set → print it; do not open a browser.
2. `WEB_HOST` set (Google Cloud Workstation) → print `https://<port>-<WEB_HOST>`; do
   not open a browser. (The forwarded URL is gated by Google IAM/IAP, so it is
   reachable only by the authenticated workstation user — correct for the author's own
   loop, and the reason sharing goes through `export`, not the serve URL.)
3. Otherwise (local) → print `http://localhost:<port>` and attempt to open it with the
   OS opener (`open` on macOS, `xdg-open` on Linux, `start` on Windows), unless
   `--no-open`.

Codespaces (`CODESPACE_NAME` + `GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN`) and Gitpod
(`GITPOD_WORKSPACE_URL`) are *not* wired in v1; their URL formulas are recorded under
Limitations & Future Work as a cheap follow-on (the resolver is an ordered env check).

### Serve reload loop

1. Start an `fsnotify` watch on the spec file.
2. On a change event, debounce (~100 ms quiet period) to absorb multi-syscall and
   atomic-rename saves.
3. Re-read and re-parse the spec from disk. On success, swap the served bytes; on
   failure, keep the last-known-good bytes and record the parse error (INV-1, BC-2).
4. Push a reload event to every connected browser over Server-Sent Events.
5. The browser, on receiving the event, refetches `/openapi.yaml` and re-renders ReDoc
   **in place** (no full page reload — smoother loop, scroll position roughly
   preserved). A full `location.reload()` is the documented fallback if ReDoc's
   re-init proves fussy.

## Architecture

- **Package:** new `internal/docs/` mirroring the existing subcommand packages, with
  entry points `Serve(ctx, cfg) error` and `Export(cfg) error` invoked from the cobra
  `Run:` closures in `run_cmd.go`.
- **CLI surface gains a context.** `serve` is long-running and must be cancelable from
  a test and from a signal. The entry point is changed to
  `RunCmd(ctx context.Context, stdout io.Writer, args []string) int`; `cmd/duh/main.go`
  supplies a `signal.NotifyContext` (SIGINT/SIGTERM → cancel) and existing surface
  tests pass `context.Background()`. `Serve` shuts its `http.Server` down on
  `ctx.Done()`.
- **Renderer delivery:** `redoc.standalone.js` is vendored into `internal/docs/` and
  embedded with `go:embed` (the codebase already uses `go:embed` in
  `internal/generate/duh/`). The HTML template is a small ReDoc bootstrap page.
  - *serve:* the page references the embedded JS at a local route and points ReDoc at
    the `/openapi.yaml` endpoint.
  - *export:* the JS and the spec are inlined into a single HTML document.
- **Transport:** plain `net/http` for the server, the spec endpoint, the static ReDoc
  asset, and the SSE endpoint. SSE (not WebSocket or polling) — unidirectional
  server→browser, no third-party dependency.
- **Spec format:** the raw YAML is passed through unchanged; ReDoc consumes YAML
  directly, so there is no YAML→JSON conversion step. Parsing for INV-1/BC-2 validity
  checks reuses the project's existing OpenAPI loader (`lint.Load` / the YAML parser
  already in use).

## Data Design

### Invariant Preservation

- **INV-1 (served spec integrity):** the served state is modeled as a value holding
  the last-known-good spec bytes plus an optional current parse error. It is seeded at
  startup by a successful initial parse — `serve` exits `2` rather than start with no
  good bytes (AC #8), so the state is never observed in a nil-good-bytes condition. The
  only operation that mutates it is the debounced reload handler, which parses *before*
  swapping and never replaces good bytes with a parse failure. Concurrent reads
  (HTTP handlers) and the single writer (watch loop) are guarded so a reader never
  observes a partial swap.
- **INV-2 (export integrity):** `Export` writes to a temp file in the destination
  directory and `os.Rename`s it over the target only after the full write succeeds.
  `rename(2)` within a directory is atomic, so a reader/observer sees either the old
  file or the complete new one, never a truncated file.

### Illegal State Analysis

- "Serving a half-written file as valid" is unrepresentable: the served state cannot
  hold spec bytes that did not pass the parse step (the swap is gated on a successful
  parse). A parse failure is represented as the *error* field beside the retained good
  bytes — distinct from the good-bytes field, so the two cannot be conflated.
- "A partially written export visible on disk" is unrepresentable via the atomic
  temp+rename; there is no code path that writes incrementally to the final path.

## Security

- The serve listener binds for local / single-user dev use; there is no auth layer (a
  declared non-goal). On Google Cloud Workstations the forwarded port is additionally
  gated by Google IAM/IAP outside this tool.
- The spec endpoint and static asset are read-only; there are no mutating HTTP routes.
- No untrusted input is executed; the spec is rendered as data by ReDoc in the browser.

## PII

None handled. The tool reads an OpenAPI spec file and serves/writes its rendered form;
it stores nothing and transmits nothing off the machine.

## Testing

Testing follows the `surface-testing` skill — all tests call `duh.RunCmd(ctx, &buf,
args)` and assert on exit code, stdout, HTTP responses, and output files; no internal
functions are called directly. Tests live in `internal/docs/docs_test.go`
(`package docs_test`).

Key surfaces and seams:

- **export** — `RunCmd` with `docs export`; assert exit code and inspect the written
  file (inlined spec, inlined ReDoc, no external asset refs, atomic-overwrite behavior,
  exit `2` + untouched file on bad spec).
- **serve** — run `RunCmd` in a goroutine with `--port 0`; read the resolved URL/port
  from stdout; drive it over a real HTTP client:
  - `GET /` and `GET /openapi.yaml` content assertions.
  - connect to the **SSE endpoint as the reload observability surface**, edit the spec
    in a `t.TempDir()`, and assert a reload event arrives (`require.Eventually`).
  - replace the spec with invalid YAML; assert the server stays up and recovers on a
    later valid save.
  - cancel the context; assert clean shutdown and exit `0`.
  - set `WEB_HOST` in the test env; assert the printed URL is
    `https://<port>-<WEB_HOST>` and no browser open is attempted.
- **External dependencies / substitutes:** none. No external services; `fsnotify`
  operates on real files under `t.TempDir()`; ReDoc is embedded; SSE is exercised over
  a real local HTTP connection. No fakes required.
- **Time handling:** the debounce duration is a config field with a small default so
  tests can lower it; reload assertions use `require.Eventually` rather than mocking
  the clock.
- **Browser opener:** the OS-open step is behind a seam (an injectable opener func /
  the `--no-open` path) so tests never actually launch a browser.

## Dependencies and Constraints

- **`fsnotify`** — new `go.mod` dependency for cross-platform file watching.
- **ReDoc standalone bundle** — `redoc.standalone.js` (~1 MB, MIT-licensed) vendored
  into the repo and embedded via `go:embed`. A specific ReDoc version is pinned;
  updating it is a deliberate manual step. This is the accepted cost of BC-1 (zero
  network dependency).

## Limitations & Future Work

- **External `$ref`s are not bundled.** A truly self-contained export works for a
  single-file spec (the common `duh init` case). If a spec pulls in external files via
  `$ref: ./schemas/user.yaml`, those are not inlined into the export and not watched by
  serve. Documented limitation; bundling/dereferencing is deferred.
- **Watcher covers the top-level spec file only** (follows from the above).
- **Remote URL detection covers Google Cloud Workstations only in v1.** Codespaces and
  Gitpod follow-on, using the documented formulas:
  - Codespaces: `https://${CODESPACE_NAME}-${PORT}.${GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN}`
  - Gitpod: `https://${PORT}-${GITPOD_WORKSPACE_URL#https://}`
- **No theming** — a branded export for partners is a plausible future addition.

## Open Questions

None blocking. (The serve in-place re-render has a documented full-reload fallback;
the Cloud Workstations URL mechanism is verified against Google's docs — `WEB_HOST`
holds `<workstation>.<cluster>.cloudworkstations.dev` and the port URL is
`https://<port>-<WEB_HOST>`.)

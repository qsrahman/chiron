# AGENTS.md — developer guide

Read [PLAN.md](PLAN.md) first for goals, decisions and the phase table.

## Layout

```
cmd/desk-agent/     main: flags, wiring, -p mode
internal/config/    config file, env and flag precedence
internal/agent/     loop, events, history, system prompt (no UI imports)
internal/tools/     Tool interface, MustNew, Registry (built-in tools are added from Phase 5)
internal/tui/       Bubble Tea chat UI (model, theme, layout, cards, chrome; golden tests in testdata/)
internal/tools/     Tool interface, registry, built-in tools
internal/mcp/       MCP servers adapted to tools.Tool
internal/tui/       Bubble Tea model / update / view
```

Packages are created in the phase that needs them; do not add empty ones.

## Commands

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...   # required whenever concurrency code changes
```

Build, vet and test must pass before a commit.

Manual TUI check: build the binary and run it in a real terminal, or drive it through a pseudo-terminal
(Python `pty`) sending a prompt and `Ctrl-C`; the TUI is not covered by the go tests' real terminal output.

Real-server checks (cost a few tokens, never run by default; skip without a key or base URL). They use the
default config file and the environment, like the CLI:

```bash
go test -tags=integration -run TestRealAPI -v -count=1 ./internal/agent
```

## Conventions

- **Do not hand-roll.** Before writing a utility, check `openai-go`, `modelcontextprotocol/go-sdk`,
  `google/jsonschema-go` and the standard library. Tool schemas come from `jsonschema.For[Args]()`.
- **The agent core never imports the TUI.** It emits `Event` values; front-ends render them. Approvals and
  questions are events carrying a reply channel.
- **History is local** (`store=false`). Append response output items unchanged (via `param.Override` and
  `RawJSON()`); never rebuild them from parsed fields, or reasoning items and unknown fields are lost.
- **Tool errors go to the model** as the tool output text, not as Go errors that abort the loop.
- **Adding a tool:** define an `Args` struct (`json` tags for names, `jsonschema` tags for descriptions), write
  `func(ctx, Args) (string, error)`, wrap it with `tools.MustNew`, and add it to the registry in `main`. The
  agent loop and the TUI need no change; the TUI shows every call as a card.
- **Read-only tools run concurrently; mutating tools run alone, in model order.** Mark a tool read-only only if
  it cannot change anything outside the process.
- **File tools are confined to the working directory tree.**
- **`agent.Send` contract:** the caller must read the returned channel until it closes (the last event is `Done`
  or `Error`); cancel the context to stop early. History changes only when a turn succeeds.
- **TUI conventions.** Bubble Tea copies the model on every `Update`, so keep it a plain value: no
  `strings.Builder` or mutex fields. Anything slow or blocking (reading agent events) runs in a `tea.Cmd` and
  returns a message. The TUI depends on the small `tui.Sender` interface, not on `*agent.Agent`, so tests drive
  it with a fake and synthetic messages (see `internal/tui/model_test.go`). Bubble Tea v2 APIs: `View()`
  returns `tea.View`, keys are `tea.KeyPressMsg`.
- **UI look.** Colours come only from the palette in `theme.go`; never hard-code a colour or an ANSI index
  elsewhere, and never paint the terminal background (bands are derived from it). Everything in the transcript
  shares the `gutter` indent and is capped at `maxTextWidth`; `render` clamps every line to the content width,
  because a line wider than the terminal wraps and wrecks the layout. Tool cards show parsed arguments, never raw
  JSON. Key bindings live in `keys.go` and feed both the key handling and the help bar.
- **Glamour gotcha.** Glamour registers a custom `Chroma` colour table once under a single global name, so
  the first theme to render wins for the whole process. Code colours therefore use named chroma styles
  (`monokai`, `github`) via `CodeBlock.Theme`; `TestCodeColoursFollowTheTheme` guards this. Glamour also counts a
  tab as zero cells, so tabs are expanded before rendering, and it pads output with blank lines, which
  `trimBlankLines` removes so every gap between items is exactly one row.
- **UI tests.** The main screens have golden files (`internal/tui/testdata/*.golden`, plain text). After an
  intended visual change run `go test ./internal/tui -update` and review the diff. The flag goes after the
  package. `TestNoLineIsWiderThanTheTerminal` guards the layout at several widths; colours are tested in
  `theme_test.go` because the goldens have none. To see the real colours, dump `View().Content` and convert the
  ANSI to HTML.
- **System prompt.** The text lives in `internal/agent/prompt.md.tmpl` (embedded, `text/template`); change the
  wording there, and add new facts about the session through `promptData` in `prompt.go`. Do not mention tools
  that are not registered yet.
- **Guards.** `guard.go` holds loop detection (window 10, more than 5 repeats); the stream idle timeout is a field
  on `Agent` so tests can shorten it. Cancelling must stay distinguishable from timeouts: only a user cancel
  satisfies `errors.Is(err, context.Canceled)`.
- **Tests use no network.** Point the SDK at an `httptest` server with `option.WithBaseURL` and scripted SSE.
  Test the TUI through `Update` with synthetic messages.
- **Comments explain why**, not what. No line-by-line commentary.
- Config uses `encoding/json`, `os.ExpandEnv` (for `${VAR}` in string values) and the standard `flag` package
  only. Never print `api_key` or MCP `headers`/`env` values in logs or errors.

## Code quality

Priorities, in order: simple and easy to read, then easy to extend, then efficient.

- **Obvious over clever.** Prefer plain loops, explicit types and straight-line code. If a reader needs a
  comment to follow the mechanics, rewrite the code; keep comments for the *why*.
- **Modular, not over-modular.** One package per real responsibility (see Layout). No interface until there are
  two implementations or a test needs a seam; no wrapper types, factories or layers "for later".
- **Extend by adding, not editing.** A new tool is a new file that is built with `tools.MustNew` and added to the registry; a new
  front-end consumes `agent.Event`. Adding either must not require changes to the loop.
- **Go conventions.**
  - `gofmt` and `go vet` clean; exported identifiers have doc comments starting with the name.
  - `context.Context` is the first parameter of anything that blocks or does I/O; honour cancellation.
  - Accept small interfaces, return concrete types. Define interfaces where they are used.
  - Return errors; wrap with `fmt.Errorf("doing x: %w", err)`; compare with `errors.Is` / `errors.As`. No
    `panic` outside `main`, tests and `Must...` constructors (like `tools.MustNew`) whose only failure is a
    programming error.
  - No package-level mutable state and no `init()` side effects; pass dependencies explicitly (constructor
    arguments), so tests can substitute them.
  - Every goroutine has an owner, ends when its `ctx` is cancelled, and its channel is closed by the sender.
  - Table-driven tests with `t.Run`; use `t.TempDir()` and `t.Cleanup()`; run with `-race` when touching
    concurrency.
  - Short, lower-case package names; no stutter (`agent.Agent` is fine, `agent.AgentLoop` is not).

## Workflow for every change

1. Write or update tests first when the behaviour is observable.
2. Implement, then run build, vet and test.
3. Review and update `PLAN.md`, `README.md`, `AGENTS.md` and `CHANGELOG.md` if the change affects features,
   architecture or conventions.
4. Commit with a Conventional Commit message (`feat(tools): ...`, `fix(agent): ...`, `docs: ...`). No co-author
   trailer.
5. At the end of a phase, move `CHANGELOG.md` entries from `Unreleased` to the new version and tag `v0.N.0`.

## Changelog

`CHANGELOG.md` follows Keep a Changelog 1.1.0 and SemVer. Add entries under `Unreleased` in the same commit as
the change.

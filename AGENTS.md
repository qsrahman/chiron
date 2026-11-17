# AGENTS.md — developer guide

Read [PLAN.md](PLAN.md) first for goals, decisions and the phase table.

## Layout

```
cmd/desk-agent/     main: flags, wiring, -p mode
internal/config/    config file, env and flag precedence
internal/agent/     loop, events, history            (no UI imports)
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

Real-API check (costs a few tokens, never run by default; skips without a key):

```bash
OPENAI_API_KEY=... go test -tags=integration -run TestRealAPIReplay -v ./internal/agent
```

## Conventions

- **Do not hand-roll.** Before writing a utility, check `openai-go`, `modelcontextprotocol/go-sdk`,
  `google/jsonschema-go` and the standard library. Tool schemas come from `jsonschema.For[Args]()`.
- **The agent core never imports the TUI.** It emits `Event` values; front-ends render them. Approvals and
  questions are events carrying a reply channel.
- **History is local** (`store=false`). Append response output items unchanged (via `param.Override` and
  `RawJSON()`); never rebuild them from parsed fields, or reasoning items and unknown fields are lost.
- **Tool errors go to the model** as the tool output text, not as Go errors that abort the loop.
- **Read-only tools run concurrently; mutating tools run alone, in model order.** Mark a tool read-only only if
  it cannot change anything outside the process.
- **File tools are confined to the working directory tree.**
- **`agent.Send` contract:** the caller must read the returned channel until it closes (the last event is `Done`
  or `Error`); cancel the context to stop early. History changes only when a turn succeeds.
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
- **Extend by adding, not editing.** A new tool is a new file that registers itself through `tools.New`; a new
  front-end consumes `agent.Event`. Adding either must not require changes to the loop.
- **Go conventions.**
  - `gofmt` and `go vet` clean; exported identifiers have doc comments starting with the name.
  - `context.Context` is the first parameter of anything that blocks or does I/O; honour cancellation.
  - Accept small interfaces, return concrete types. Define interfaces where they are used.
  - Return errors; wrap with `fmt.Errorf("doing x: %w", err)`; compare with `errors.Is` / `errors.As`. No
    `panic` outside `main` and tests.
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

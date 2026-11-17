# AGENTS.md — developer guide

Read [PLAN.md](PLAN.md) first for goals, decisions and the phase table.

## Layout

```
cmd/desk-agent/     main: flags, config, wiring
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
```

All three must pass before a commit.

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
- **Tests use no network.** Point the SDK at an `httptest` server with `option.WithBaseURL` and scripted SSE.
  Test the TUI through `Update` with synthetic messages.
- **Comments explain why**, not what. No line-by-line commentary.
- Config uses `encoding/json`, `os.ExpandEnv` (for `${VAR}` in string values) and the standard `flag` package
  only. Never print `api_key` or MCP `headers`/`env` values in logs or errors.

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

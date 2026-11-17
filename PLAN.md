# desk-agent — Product Requirements Document

Status: living document. Update it whenever a change affects scope, architecture or phases.
Last reviewed: 2026-11-17 (Phase 0).

## 1. Goal

A terminal-based AI coding agent written in Go. It talks to the user in a Bubble Tea TUI, uses the
OpenAI Responses API (`openai-go`) and operates on the local machine through tools: file read, write and edit,
shell, web search, web fetch, todo list and asking the user. It streams output, runs independent tools
concurrently, and can load extra tools from MCP servers.

Non-goals for v1: a GUI, sandboxing of shell commands, multi-agent orchestration, MCP over HTTP, per-project
config, glob/grep tools (the model uses `rg`/`find` through `shell`).

## 2. Principles

- **Simple and modular, not over-modular.** Four packages and a `main`; no extra layers.
- **Do not hand-roll what a dependency already provides.** Check `openai-go`, the MCP SDK and the standard
  library before writing a utility.
- **The agent core knows nothing about the UI.** It emits events; front-ends render them.
- **Every phase ships code, tests, docs and one commit.**
- **Comments explain why, not what.**

## 3. Decisions

| Area | Decision | Reason |
|---|---|---|
| Language / UI | Go, Bubble Tea v2 (`charm.land/bubbletea/v2`) | Single binary, first-class concurrency, strongest Go TUI ecosystem. |
| API | OpenAI Responses via `github.com/openai/openai-go/v3` | Requested. Typed streaming events and an accumulator are built in. |
| History | Local history, `store=false` | We own sessions, compaction and resume; works under zero data retention. Output items are replayed as-is (including reasoning items) using the SDK's `param.Override` + `RawJSON()` pattern. |
| Safety | Ask before every `write_file`, `edit_file`, `shell`; per-tool "always allow for this session". File tools are confined to the working directory tree. Shell is not sandboxed. | Predictable and simple. |
| Search | No glob/grep tools in v1 | Fewer tools; `rg`/`find` via shell. |
| Tool schemas | Generated from Go structs with `google/jsonschema-go` (`jsonschema.For[T]`) | Already a dependency of the MCP SDK; nothing hand-written. |
| MCP | Official `github.com/modelcontextprotocol/go-sdk`, stdio transport (`CommandTransport`) | Official SDK; tools adapt to our `Tool` interface. |
| Config | One file: `$XDG_CONFIG_HOME/desk-agent/config.json`, else `~/.config/desk-agent/config.json`. No per-project config. | Requested. `os.UserConfigDir` would give `~/Library/Application Support` on macOS, so the path is built explicitly. Precedence: flags > env vars > file. |
| Model | `gpt-5.4-mini` is the intended default; configurable via flag, env `DESK_AGENT_MODEL` or config. | Requested. Model IDs change often, so it is never hard-coded in logic. Confirm the ID against the account when Phase 1 first hits the real API. |
| API key | `OPENAI_API_KEY`, read by the SDK | No custom key handling. |
| Commits | Conventional Commits, no co-author trailer | User preference. |
| Versioning | Tag `v0.N.0` at the end of each phase | Keep a Changelog + SemVer. |

## 4. Architecture

```
cmd/desk-agent/main.go     flags, config, wiring
internal/agent/            loop, events, history
internal/tools/            Tool interface + registry; fs, shell, web, todo, ask
internal/mcp/              MCP servers adapted to tools.Tool
internal/tui/              Bubble Tea model / update / view
```

### 4.1 Tool interface

```go
type Tool interface {
    Name() string
    Spec() responses.ToolUnionParam              // JSON schema sent to the model
    ReadOnly() bool                              // true: may run concurrently, never needs approval
    Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Generic constructor: schema and argument decoding derive from the Args struct.
func New[Args any](name, desc string, readOnly bool,
    run func(ctx context.Context, a Args) (string, error)) Tool
```

Tool errors are returned to the model as the `function_call_output` text, not raised as Go errors, so the model
can recover. MCP tools implement the same interface.

### 4.2 Agent loop

1. Send history + tools with `store=false` and `include: ["reasoning.encrypted_content"]` (verify in Phase 1).
2. Stream the response (`client.Responses.NewStreaming`), emitting `TextDelta` events and accumulating output
   items (`responses.ResponseAccumulator`; verified against SSE in the Phase 1 spike).
3. Append all output items to history unchanged.
4. If there are no function calls, emit `Done`.
5. Otherwise split the calls, in model order, into groups: each run of consecutive read-only calls is one
   concurrent group (`sync.WaitGroup`, results written by index); each mutating call is its own serial group.
6. Mutating calls emit `ApprovalRequested` and block on a reply channel (honouring `ctx`) unless the user chose
   "always allow" for that tool.
7. Append a `function_call_output` per `call_id`, then loop.

`Ctrl-C` cancels the context of the current turn only. Shell commands run in their own process group so cancel
kills the whole tree.

### 4.3 Events

`TextDelta`, `ToolStarted`, `ToolFinished`, `ApprovalRequested{Reply chan bool}`,
`QuestionAsked{Reply chan string}`, `TodoUpdated`, `Done`, `Error`. The agent returns `<-chan Event`.
Approvals and questions are events with reply channels so any front-end (TUI now, others later) can answer them.

### 4.4 Testing strategy

Tests never touch the network. `openai-go` accepts `option.WithBaseURL`, so tests point the client at an
`httptest` server that replays scripted SSE fixtures. The TUI is tested through its `Update` function with
synthetic messages. Real-API checks are manual and documented in the README.

## 5. Tools (v1)

| Tool | Read-only | Notes |
|---|---|---|
| `read_file` | yes | Line-range support, output size cap, path confined to working tree. |
| `list_dir` | yes | Confined to working tree. |
| `write_file` | no | Create or overwrite; approval required. |
| `edit_file` | no | Exact string replace; fails when `old` is missing or not unique unless `replace_all`. |
| `shell` | no | `sh -c`, timeout, output cap, process-group kill on cancel. |
| `todo` | yes | In-memory list shown in the TUI; replaces the whole list per call. |
| `ask_user` | yes | Blocks on a `QuestionAsked` reply. |
| `web_search` | n/a | Built-in OpenAI tool (`{"type":"web_search"}`), executed server-side. |
| `web_fetch` | yes | `net/http` + HTML-to-markdown library (chosen in Phase 7), size cap, timeout. |
| MCP tools | per server | Namespaced `server__tool`; treated as mutating (approval) unless the MCP annotations mark them read-only. |

## 6. Configuration

```json
{
  "model": "gpt-5.4-mini",
  "approval": "ask",
  "mcpServers": {
    "name": { "command": "npx", "args": ["-y", "pkg"], "env": {} }
  }
}
```

Parsed with `encoding/json`. Unknown keys are ignored. `mcpServers` uses the common
`{command, args, env}` shape so existing MCP configs can be pasted in.

## 7. Phases

Each phase ends with: code, tests, docs review (PLAN, README, AGENTS, CHANGELOG), one commit, and a tag `v0.N.0` where N is the phase number (Phase 0, the scaffold, is untagged).

| # | Phase | Done when |
|---|---|---|
| 0 | Scaffold: module, git, docs, skeleton, smoke test | `go vet ./...` and `go test ./...` pass; docs describe the skeleton. |
| 1 | Agent core: config loading, streaming, local history, `-p "prompt"` non-interactive mode. Starts with the `ResponseAccumulator`-over-SSE and reasoning-replay spikes. | Fake-server test streams text; a second turn replays history correctly. |
| 2 | TUI shell: transcript viewport, input, token streaming, `Ctrl-C` cancels the turn, markdown for completed messages | `Update` tests pass; manual run against the real API. |
| 3 | Tool loop, `read_file`, `list_dir`, parallel dispatch, tool cards in the TUI | Three slow read-only calls finish in about 1x, not 3x, the time of one. |
| 4 | `write_file`, `edit_file`, approval prompt | Mutations are refused without approval; an ambiguous edit fails cleanly. |
| 5 | `shell` | A hung command is killed on cancel and the loop recovers. |
| 6 | `todo`, `ask_user` | The model can pause, ask and resume. |
| 7 | `web_search`, `web_fetch` | Redirects, size limits and non-HTML handled. |
| 8 | MCP stdio client, `mcpServers` config, namespacing | A real MCP server's tools work, including in parallel. |
| 9 | Session save/resume, compaction via `responses.compact`, slash commands | A resumed session continues; a long one compacts. |

## 8. Open items

- Confirm `gpt-5.4-mini` is valid for the account (Phase 1, first real call).
- Verify `ResponseAccumulator` over SSE and `include: reasoning.encrypted_content` behaviour (Phase 1 spike).
- Verify Bubble Tea v2 / `bubbles` v2 / `glamour` import paths and APIs (Phase 2).
- Pick the HTML-to-markdown library (Phase 7).
- Decide the session storage format and location (Phase 9).

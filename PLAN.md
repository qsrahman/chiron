# chiron — Product Requirements Document

Status: living document. Update it whenever a change affects scope, architecture or phases.
Last reviewed: 2026-11-19 (after the rename to chiron, v0.7.0).

## 1. Goal

A terminal-based AI coding agent written in Go. It talks to the user in a Bubble Tea TUI, uses the
OpenAI Responses API (`openai-go`) and operates on the local machine through tools: file read, write and edit,
shell, web fetch, todo list and asking the user. It streams output, runs independent tools
concurrently, and can load extra tools from MCP servers. Web search is deliberately undecided (see section 8);
OpenAI's built-in `web_search` tool will not be used.

Non-goals for v1: a GUI, sandboxing of shell commands, multi-agent orchestration, MCP OAuth flows, per-project
config, glob/grep tools (the model uses `rg`/`find` through `shell`).

## 2. Principles

- **Simple and modular, not over-modular.** Four packages and a `main`; no extra layers. Readable code beats
  clever code, and new tools or front-ends must be addable without editing the agent loop. The detailed Go
  rules are in `AGENTS.md`.
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
| MCP | Official `github.com/modelcontextprotocol/go-sdk`; stdio servers (`CommandTransport`) and remote HTTP servers (Streamable HTTP client transport) | Official SDK; tools adapt to our `Tool` interface. Both transports are needed by the config. |
| Config | One file: `$XDG_CONFIG_HOME/chiron/config.json`, else `~/.config/chiron/config.json`. No per-project config. | Requested. `os.UserConfigDir` would give `~/Library/Application Support` on macOS, so the path is built explicitly. Precedence: flags > file > defaults; the only environment variable is `OPENAI_API_KEY` (section 6). |
| Model | `gpt-5.4-mini` is the intended default; configurable via `--model` or the config file (the `CHIRON_MODEL` variable is removed in Phase 3.7). | Requested. Model IDs change often, so it is never hard-coded in logic. Confirm the ID against the account when Phase 1 first hits the real API. |
| API key / endpoint | `api_key` and `base_url` in the config, or the `OPENAI_API_KEY` env var (the `OPENAI_BASE_URL` variable is removed in Phase 3.7); `--base-url` flag. There is deliberately no `--api-key` flag (shell history, process lists). The resolved values are passed to the client with `option.WithAPIKey` / `option.WithBaseURL`; an API key is only required when `base_url` is unset. | Lets the agent target OpenAI-compatible servers (for example a local one at `http://localhost:11434/v1`). `base_url` is unset by default, which means the OpenAI API. |
| Secrets in config | Every string value goes through `os.ExpandEnv`, so `"${NAME}"` pulls from the environment. The loader warns when the file is readable by group or others. | Keep keys out of the file where possible; no custom expansion code. |
| Commits | Conventional Commits, no co-author trailer | User preference. |
| Versioning | Tag `v0.N.0` at the end of each phase | Keep a Changelog + SemVer. |

## 4. Architecture

```
cmd/chiron/main.go     flags, wiring, -p mode, starts the TUI
internal/config/           config file, env and flag precedence
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
    Run(ctx context.Context, arguments string) (string, error)   // arguments: the model's raw JSON
}

// Generic constructor: the schema (google/jsonschema-go) and the argument decoding derive from the Args
// struct. Unknown argument fields are rejected. It panics on a type that cannot be a schema (a programming
// error, like regexp.MustCompile).
func MustNew[Args any](name, desc string, readOnly bool,
    run func(ctx context.Context, a Args) (string, error)) Tool
```

A `tools.Registry` holds the tools (unique names, registration order); the zero value is empty.

Tool failures, an unknown tool name and invalid arguments are all returned to the model as the
`function_call_output` text (`error: ...`), not raised as Go errors, so the model can recover. MCP tools
implement the same interface.

### 4.2 Agent loop

1. Send history + tools with `store=false` and `include: ["reasoning.encrypted_content"]` (implemented in
   Phase 1; not yet confirmed against the real API, see section 8).
2. Stream the response (`client.Responses.NewStreaming`), emitting `TextDelta` events. The final
   `response.completed` event carries the whole response, so its output items are used directly;
   `responses.ResponseAccumulator` is not needed.
3. Append all output items to the turn's input unchanged.
4. If there are no function calls, replace the history with the turn's input and emit `Done`.
5. Otherwise split the calls, in model order, into groups: each run of consecutive read-only calls is one
   concurrent group (`sync.WaitGroup`, results written by index); every other call (mutating or unknown) is its
   own serial group. Each call emits `ToolStarted` and `ToolFinished`. (Phase 3, done.)
6. Mutating calls emit `ApprovalRequested` and block on a reply channel (honouring `ctx`) unless the user chose
   "always allow" for that tool. (Phase 4.)
7. Append a `function_call_output` per `call_id` (in call order, whatever order they finished), then loop.
   A turn is limited to `max_turns` model calls (default 25); hitting the limit ends the turn with an error and keeps it with a closing note (section 4.7).
   If the context is cancelled while tools run, the model is not asked again. Loop detection (Phase 3.5): a
   step is identified by its calls, arguments and outputs; if one step appears more than 5 times among the last
   10, the turn stops with an error and is kept with a closing note.
8. Each response yields a `Usage` event (input and output tokens). A stream that sends nothing for 2 minutes
   (`defaultIdleTimeout`) is abandoned with a "stopped sending data" error, which is not a user cancellation.

`Ctrl-C` cancels the context of the current turn only. Shell commands run in their own process group so cancel
kills the whole tree.

### 4.3 Events

`TextDelta`, `Usage{InputTokens, OutputTokens}`, `Retry{Reason}`, `ToolStarted{CallID, Name, Arguments}`,
`ToolFinished{CallID, Name, Output, Outcome}` (`CallOK`, `CallFailed`, `CallCancelled`, `CallDenied`),
`ApprovalRequested{CallID, Tool, Summary, Details, GrantKey, Reply chan<- Decision}` (done in 4a.1),
`QuestionAsked{Reply chan string}`, `TodoUpdated`, `Done`, `Error`. The agent returns `<-chan Event`.
Approvals and questions are events with reply channels so any front-end (TUI now, others later) can answer them.

### 4.4 Testing strategy

Tests never touch the network. `openai-go` accepts `option.WithBaseURL`, so tests point the client at an
`httptest` server that replays scripted SSE fixtures. The TUI is tested through its `Update` function with
synthetic messages. Real-API checks are manual and documented in the README.

### 4.5 Approvals, queue and cards (Phase 4a)

- **Which calls ask:** every call to a tool whose `ReadOnly()` is false. Read-only tools never ask and keep running
  concurrently. Mutating calls run one at a time, so at most one prompt is open.
- **Validate before asking:** the arguments are checked first (a JSON object with the schema's required keys). A call
  with invalid arguments, or to an unknown tool, is answered with the error (an unknown tool with the list of
  available ones) and the user is never asked about it.
- **Event:** `ApprovalRequested{CallID, Tool, Summary, Details, GrantKey, Reply chan<- Decision}`. `Summary` is one
  line ("Run a shell command"); `Details` is what is about to happen (a command, a diff, MCP parameters). The reply
  channel is buffered so the UI never blocks sending the answer.
- **Decision:** `Deny`, `Allow` (once) or `AllowSession`. `AllowSession` is remembered by tool name plus `GrantKey`,
  so for `shell` it covers that command and not "any shell"; tools without a key are granted per tool.
- **Tools describe their own prompt** through an optional interface, so Phases 5 and 6 supply a diff or a command
  without changing the loop. Tools that do not implement it get a generic prompt showing the arguments.
- **Denial:** the model gets `error: the user denied this call; do not retry it or work around it` and the card shows
  it as denied. Cancelling the turn abandons an open prompt (the call is reported cancelled, not denied).
- **Non-interactive mode:** with `-p` nobody can answer, so a mutating call is denied with the usual denial text unless
  `--yes` was given, which allows every call for that run. There is no per-tool list.
- **Grants last until the program exits** and survive `/reset` (a reset clears context, not permissions). There is no
  `auto_approve` list for built-in tools yet; it arrives with the tools in Phase 5 (MCP's `autoApprove` is in 4b.1).
- **What the user approves is what runs:** `Details` is shown without the usual sanitising: control characters appear as
  visible escapes (`\x1b`, `\r`), long details are never cut (the modal scrolls) and a size line says how big they are.
  The denial text is fixed; there is no note to the model.
- **Stray keys:** the modal ignores every key but Ctrl-C for 500 ms after it opens, so type-ahead (a "y" in the middle
  of a word) cannot answer it. A window too small for the modal gets a screen no taller than itself.
- **Modal (TUI):** a rounded, primary-coloured dialog centered over the transcript (lipgloss `Layer`/`Compositor`),
  scrollable when the details are long. Options: Allow, Allow for session, Deny, with **Deny preselected** so an
  answer by reflex refuses; keys `y`, `s`, `n`/`Esc`, arrows and Enter. A prototype of the overlay comes first; if
  `Layer`/`Compositor` does not fit, fall back to an inline panel that replaces the input box.
- **Queue:** Enter while a turn runs adds the text to a queue shown above the input; when a turn ends without error the
  next prompt is sent. Ctrl-C cancels the turn and drops the queue, and the UI says how many were dropped. After a turn
  that fails on its own the queue is held (a notice says how many are waiting) so a broken server is not hit once per
  prompt; the next Enter sends the first. Up on an empty input pulls the last queued prompt back into the input.
  Commands are never queued (they stay refused during a turn).
- **Expandable cards:** the model keeps a selected card index; Tab / Shift+Tab move it when the input is empty;
  Enter or Space toggle that card; Esc clears the selection. Closed cards (the default) show only their header, with
  the line count and duration at the right end (and one line of an error), expanded ones show the whole output (still width-clamped, at most the 20 KiB the agent keeps) and the viewport scrolls to keep the selected card in view.

### 4.6 MCP client (Phases 4b.1 and 4b.2)

Informed by a read of muagent's `mcp_client.py` and `mcp_hub.py` (Python; see section 9.2).

- **Transports:** stdio via `mcp.CommandTransport` (shortened `TerminateDuration`, about 2 s, so quitting never hangs)
  and remote servers via `mcp.StreamableClientTransport` with our own `http.Client`. No OAuth, no sampling, no
  elicitation (no such capabilities are declared, so servers do not ask).
- **Trust:** the server's own `readOnlyHint` is **ignored**: a server can claim anything about itself. Every MCP call
  asks for approval unless the tool is listed in that server's `autoApprove`. Because nothing is declared
  read-only, MCP calls run one at a time.
- **Prompt:** the adapter describes its own calls (`tools.Describer`): the summary is `server → tool`, the details are
  the pretty-printed arguments, and a muted line under them says where the server runs (its command, or the host of
  its URL), so the user sees which program receives the call. The server's own description is not shown there. For
  this `tools.Prompt` gets a `Source` field that the dialog shows. **Allow for session covers that tool with any
  arguments** (an empty grant key), listed by `/tools`; there is no per-server grant.
- **Descriptions** and schemas go to the model as the server gives them (only results are treated as untrusted data).
- **Process environment:** a stdio server inherits the full environment of chiron plus its own `env` (a user
  decision). This means the server can see variables such as `OPENAI_API_KEY`; the README says so. Tightening it
  later is a small change (an allow-list in `internal/mcp`).
- **Remote servers:** the URL must be https unless it points at loopback, and may not contain credentials. The
  configured headers are sent only to the configured origin: redirects to another origin are refused, and the
  header-adding `RoundTripper` checks the host of every request itself (Go's default client forwards custom headers
  across a cross-domain redirect; it strips only `Authorization`, `Cookie` and `WWW-Authenticate`).
- **Stderr:** a stdio server's stderr goes to a private log file (mode 0600, under the user's state directory, emptied
  at each launch and appended to on a restart), never to the terminal, which would corrupt the TUI. The status shows
  the log path.
- **Config values:** `${NAME}` in `command`, `args`, `env`, `url` and `headers` is expanded strictly: an unset variable
  is an error that names the variable and where it is used (never the value), the expanded text is not scanned
  again, and that server fails without stopping the others. (The three top-level fields keep the lenient
  environment fallback of section 6.) Server names may only use letters, digits, `_` and `-`.
- **Connecting:** all enabled servers are launched before any is waited for, connect and list their tools in
  parallel, and share one 10 s deadline counted from the launch (not configurable per server; `--no-mcp` starts none
  for a run, in the TUI and with `-p`). The chat opens at once and shows "connecting"; a
  prompt sent before they finish waits (up to that deadline) so the model sees the tools. Failure notices reach the
  TUI as messages, never printed over the screen. The registry is built once from the servers that connected in time;
  a late or failed server stays out for the session, and tool-list change notifications are ignored.
- **Lost connection:** the connection is watched with the SDK's `KeepAlive` and `Wait`. After a server dies, the next
  call starts it again, at most 3 times per session; the call that found it dead fails and is not repeated, since it
  may have done part of its work. Restarting does not change the registry: if the restarted server no longer has a
  tool, that call fails with the server's error text.
- **Tool adapter:** `server__tool`, characters outside `[a-zA-Z0-9_-]` replaced by `_`, cut to 64 characters. Names
  are made unique across servers with a numeric suffix (`a` + `b__c` and `a__b` + `c` would both give `a__b__c`) and
  are assigned in sorted server order after every server has answered, so they never depend on who was faster. A name
  the API rejects fails the whole request, so this matters. Schemas pass through, with an empty `properties` added
  when missing. Arguments are checked only for being a JSON object with the schema's required keys; types are left
  to the server, whose own errors go back to the model as text.
- **Config extras:** `tools: ["a","b"]` exposes only those tools and is an error for a name the server does not have
  (a typo would otherwise offer nothing, silently); the count of hidden tools is shown in the status.
  `instructions: true` adds the server's own usage notes to the system prompt, framed as notes from the server and not
  as orders, cut to 4000 characters. Without it they are not used.
- **Results:** text parts joined; structured content used when there is no text; images and audio become a short
  placeholder (`[image: image/png, 12 KB]`); resource links keep their URL; embedded text resources are included.
  `isError` becomes a tool failure. Output goes through the agent's central 20 KiB cap like every tool result (cut with a
  note); each call has a 2-minute timeout, and cancelling the turn cancels the request.
- **Shutdown:** servers close in parallel (closing one can take seconds), including servers that failed to start.
- **Cards:** MCP calls show as `server → tool`, not the mangled name.
- **Built-in clash:** a built-in tool with the same name as an MCP tool is kept and the MCP tool is left out with a
  notice (a built-in name containing `__` could clash).
- **Status:** a line per server at startup (connected with N tools, hidden count, or the error, never a secret) and a
  count in the header. The `/servers` and `/reconnect` commands arrive with this phase (the slash-command framework is Phase 3.8); a reconnect
  applies between turns so a turn's tools never change under it.
- **Limits to respect:** OpenAI function names allow `[a-zA-Z0-9_-]`; the docs I found give a 64 (chat) or 128
  (responses) character maximum and at most 128 tools per request. If a configuration exceeds the tool count, extra
  tools are skipped with a warning. (Native namespaces and `tool_search` exist on newer models and are the way out
  for very large tool sets, not used now.)
- **Tests:** `mcp.NewInMemoryTransports()` with a test server; the test binary re-run as a stdio server (including one
  that exits, for the restart rule); an `httptest` server with the SDK's streamable handler to check that headers
  arrive, that a cross-origin redirect does not carry them, and that no secret appears in errors or logs.

### 4.7 Turn semantics and context (Phases 3.7 and 9)

From a read of muagent's `agent.py`, `context.py`, `compaction.py` and `TURN.md`.

**What must stay true** (pinned by tests and listed in AGENTS.md):

- The history changes in a few places only: save a turn, undo (used by retry), reset, compact. A turn in progress
  works on a copy.
- Every function call has its output before anything is sent or saved, and an output never outlives its call; calls
  and outputs are dropped together.
- User and assistant messages alternate in the saved history; a turn is saved with a reply or a closing note, or not
  at all.
- The newest user message and the summary are never trimmed away.
- The agent prints nothing and imports no UI; the TUI shows what events say.

**How a turn can end:**

| Ending | Saved? | You see |
|---|---|---|
| the model answers | whole turn | the reply |
| cut off after some text (`incomplete`) | the partial reply | the text, then the reason |
| failed, refused, no text, other status | nothing, unless a round already ran | the matching message |
| empty reply | retried once, with a notice | notice, then the reply |
| stream breaks (no final event, no `error` event, or connection lost) | retried up to twice if nothing ran | notice, streamed text cleared, reply |
| stream ends with an `error` event | as a failure (the same request would get the same answer) | the server's reason |
| step cap, loop stop, idle timeout | whole turn with a closing note | the message |
| context budget exceeded after shortening | whole turn with results dropped, closing note | the message |
| cancelled (Ctrl-C) | after a completed round: whole turn, open calls answered as interrupted, closing note; before: nothing | `Cancelled` |
| API or network failure | after a completed round: whole turn with a note; before: nothing | the described error |

**Context budget and compaction (Phase 9):** a budget `max_context_tokens` (default 65,536) counts the whole request:
instructions, tool definitions and history, not the reply. It is meant to sit well below the model's real window.
Tokens are estimated at about 4 bytes of UTF-8 each (the opaque encrypted reasoning content left out) and corrected
with the server's `usage.input_tokens` when present.

- **In a turn,** after each round with tools, if the next request would pass the budget: shorten tool results of earlier
  rounds, oldest first (a result over 8,000 characters keeps its first 4,000 and last 1,000 with a note that says
  how much is hidden and that the tool can be called again), then replace what is still too much by a placeholder;
  then shorten the latest round's results; only if even that does not fit, keep the turn with a note and stop.
- **Before a turn, if `auto_compact` is on (it is off by default: a summary is an extra model call that rewrites the
  history, so it is the user's choice; `/compact` does it on request),** and the next request would pass `compact_at`
  (0.75) of the budget, summarise the oldest turns with
  the chat model, keeping about `keep_recent_tokens` (15,000, at most half the trigger) of the newest verbatim, cutting
  at a turn start so a call stays with its output. The turns go to the model as labelled text, not as a conversation
  to continue, with no tools. The answer has fixed headings (goal, constraints, progress, decisions, requests in
  order, exact references, next steps); an `<analysis>` scratchpad is dropped and a missing `<summary>` tag forgiven,
  since not every model writes it. The previous summary is an input, so there is always one summary. A summary that is
  empty, has no headings, is longer than what it replaces, or hit the output cap is refused, and the history stays
  whole. After 3 failures in a row summaries pause until `/compact` works.
- **The summary** goes first in every request as a user message that says it is notes about what happened and not
  instructions (it may repeat text from a web page that tried to give orders). It is not an item of the history, so
  `/undo` and `/retry` keep working on later turns.
- **A server that refuses a request as too long** (HTTP 400 or 413 with `context_length_exceeded` or similar wording)
  gets one retry: summarise before the first round, otherwise shorten earlier results.

### 4.8 Slash commands (Phase 3.8 onwards)

From muagent's `commands.py`, the completer in `repl.py` and `tests/test_commands.py`.

- **Definition:** `Command{Name, Description, Args, Handler}`. The name matches `/[a-z][a-z0-9-]*` (the slash means a
  plain prompt can never be mistaken for a command; lowercase means the typed word can be lowercased before lookup).
  The description is one short line (under 90 characters). Duplicate names are an error found by a test, not a panic at
  run time. Aliases are separate entries (`/exit`, `/quit`). The handler returns a message, which may be empty, and an
  error.
- **Dispatch:** trim the line; split at the first space; lowercase the word; the rest is the argument, trimmed, case
  kept. A **lone** `/word` that is not a command gives `Unknown command: /rest. Type /help to list commands.` and is
  not sent. A line with more words that starts with `/` is a prompt. A handler error is shown as `Command error: ...`
  and the session goes on.
- **While a turn runs** every command is refused with a message ("wait for the turn to finish, or press Ctrl-C").
  Nothing is queued: a queued `/reset` or `/model` could apply a change the user no longer wants.
- **Suggestions:** an inline list above the input while the line starts with `/` and has no space yet: matching
  commands, sorted, each with its description. After the name, the command's argument source supplies the words
  (a fixed list, or a function of live state such as the servers that failed). Commands that take free text (`/model`)
  suggest nothing. Plain text never opens the list.
- **Output:** info entries in the transcript, dim, one line for a message or aligned rows for a table (label column
  and text column, long text wrapped under its column). They are not part of the model's history.
- **Behaviours to keep (each pinned by a test):**
  - `/retry` drops its own exchange first (a partial reply that was kept, or nothing after a failed turn) and resends
    the prompt; `Nothing to retry` with no prompt. After `/undo`, `/retry` must not remove an older exchange.
  - `/undo` removes the last exchange (`Last exchange removed` or `Nothing to undo`).
  - `/reset` forgets the history (and the summary); `/clear` clears the transcript view only.
  - `/model` shows the model, or switches it keeping the name's case; the next request uses it.
  - `/effort` shows `model default` or the level; sets a level case-insensitively; `default` leaves it out; an unknown
    level is rejected with the list of valid ones.
  - `/context` rows: instructions, tools (N), history, total (marked `~` while estimated, or "counted by the server"
    when the server's count of the last request still holds), last request.
  - `/help` lists every registered command, sorted, in aligned columns wrapped to the width, then the keys that work.
  - `/tools` (4a.1): groups tools into run without asking, allowed this session, ask first; `Tools: none` when empty.
  - `/servers` and `/reconnect` (4b.2): see section 4.6.
- **Settings are read where used,** so `/model` and `/effort` take effect on the next request. The agent exposes
  `SetModel` and `SetReasoningEffort`, which are only called between turns.

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
| `web_fetch` | yes | `net/http` + HTML-to-markdown library (chosen in Phase 8), size cap, timeout. |
| MCP tools | no | Namespaced `server__tool`; every call asks for approval unless listed in the server's `autoApprove`. The server's own read-only hint is ignored. |

## 6. Configuration

```json
{
  "model": "gpt-5.4-mini",
  "base_url": "http://localhost:11434/v1",
  "api_key": "${OPENAI_API_KEY}",
  "mcpServers": {
    "context7": {
      "url": "https://mcp.context7.com/mcp",
      "headers": { "CONTEXT7_API_KEY": "${CONTEXT7_API_KEY}" },
      "enabled": true
    },
    "grep": { "url": "https://mcp.grep.app", "enabled": true },
    "toolbox": {
      "command": "npx",
      "args": ["-y", "@qsrahman/workshop-mcp@latest"],
      "env": { "BRAVE_API_KEY": "${BRAVE_API_KEY}" },
      "enabled": true
    }
  }
}
```

- Parsed with `encoding/json`. Every string value is expanded with `os.ExpandEnv`. **From Phase 3.7:** unknown keys
  are an error that names the key and the file (a typo like `max_turn` must not be ignored silently); the only
  environment variable read is `OPENAI_API_KEY` (no variable for the base URL or the model, so a project's `.envrc`
  cannot redirect the key and the prompts), and the explicit base URL option also stops the SDK from reading
  `OPENAI_BASE_URL` itself (to be verified).
- Precedence (until Phase 3.7): flags (`--model`, `--base-url`) override env vars (`CHIRON_MODEL`,
  `OPENAI_BASE_URL`, `OPENAI_API_KEY`), which override the file, which overrides the built-in default model. From
  Phase 3.7: flags, then `OPENAI_API_KEY` for the key only, then the file, then the defaults. `base_url` and `api_key`
  may be omitted.
- `max_output_tokens` (after v0.7.0): the most tokens the model may generate for one reply; unset sends no cap; the
  summary request keeps its own cap of 8,000.
- Settings of Phase 9: `max_context_tokens` (the budget, default 65,536), `auto_compact` (default false, see section
  4.7), `sessions` (default true: every conversation is saved after each turn in private files; `false`, or the flag
  `--no-save`, saves nothing).
- New settings in Phase 3.7, each with a flag: `reasoning_effort` (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`;
  unset sends none, because non-reasoning models reject it; the flag also takes `default`), `max_turns` (model calls
  per prompt, default 25, replaces the fixed constant) and `streaming` (default true; false makes one non-streaming
  request, for servers with broken SSE).
- Each `mcpServers` entry may list `"autoApprove": ["tool", ...]`, or give `true` for every tool of the server (those it
  adds later too): tools of that server that never prompt (names as
  the server reports them). Everything else asks: the server's own read-only hint is ignored (section 4.6).
- Each `mcpServers` entry is either a **stdio server** (`command`, `args`, `env`) or a **remote HTTP server**
  (`url`, `headers`). An entry with both, or with neither, is a config error. `enabled` defaults to true when
  omitted; disabled servers are not started.
- The status line shows the share of the budget `max_context_tokens` that the conversation uses. A `context_window`
  key existed until after v0.7.0; it only fed this display and was removed, so the model's real window is never
  guessed (local models differ) and the budget is the one number that matters.
- Approval mode is not configurable in v1: mutating tools always ask (see section 3).

## 7. Phases

Each phase ends with: code, tests, docs review (PLAN, README, AGENTS, CHANGELOG), one commit, and a tag `v0.N.0` where N is the phase number (Phase 0, the scaffold, is untagged).

| # | Phase | Done when |
|---|---|---|
| 0 | Scaffold: module, git, docs, skeleton, smoke test | `go vet ./...` and `go test ./...` pass; docs describe the skeleton. |
| 1 (done, v0.1.0) | Agent core: config loading, streaming, local history, `-p "prompt"` non-interactive mode. | Fake-server test streams text; a second turn replays history correctly. |
| 2 (done, v0.2.0) | TUI shell: transcript viewport, input, token streaming, `Ctrl-C` cancels the turn, markdown for completed messages | `Update` tests pass; manual run against the real API. |
| 3 (done, v0.3.0) | Tool loop only: `Tool` interface and registry, function-call handling, concurrent read-only / serial mutating dispatch, `function_call_output` replay, tool cards in the TUI. Exercised with fake tools defined in tests; no real tools ship. | Three slow fake read-only calls finish in about 1x, not 3x, the time of one; mutating fakes run one at a time in model order; a tool error reaches the model as output and the loop continues. |
| 3.5 (done, v0.3.1) | Quick wins from the Crush study: a real system prompt (embedded template with an `<env>` block: working directory, platform, date, git state; loads `AGENTS.md`/`CLAUDE.md` from the working directory), loop detection (the same tool call, arguments and output repeated more than 5 times in the last 10 steps stops the turn), a stream idle timeout, and token/context usage read from `response.completed` and shown in the status line | Prompt renders with the env block and project file; a scripted repeating tool call is stopped before the step limit; a stalled stream fails with a clear error; usage appears in the status line. Tagged v0.3.1. |
| 3.6 (done, v0.3.2) | UI foundation (from the Crush UI study). `internal/tui/theme.go`: a semantic palette (primary, info, success, warning, danger, subtle and muted text, band background; diff colours are added in Phase 5 when diffs exist) with dark and light variants chosen from the terminal background; bands and diff colours are derived from that background with lipgloss `Darken`/`Lighten`/`Alpha`, and the terminal background is never painted. Layout: 1-column margin, text width capped at 120, a 2-column gutter shared by every item, a 1-row gap between items, a left bar in the primary colour on user messages. A one-row header (brand, working directory, git branch; model and context % on the right). Tool cards: arguments parsed into a main parameter plus `key=value` (never raw JSON), state-coloured icon (spinner via `bubbles/spinner` while running, then `✓` / `×`, muted when cancelled), elapsed time for slow calls, output on a background band with a `… (N more lines)` hint, an `ERROR` badge for failures, collapsed to 5 lines; syntax highlighting with chroma (already in the module graph). Input: prompt on the first line only, dynamic height (textarea `DynamicHeight`), focus and busy styles. Bottom help bar built with `bubbles/help` and `key.Binding` (bright key, dim description). Glamour style derived from the theme with no document margin. Welcome screen (name, version, directory, model, hint). Golden-file tests of the main views. No sidebar. | The golden views (welcome, conversation, each card state, error, narrow and wide widths, dark and light themes) match; no raw JSON in card headers; every item shares one gutter; existing behaviour tests still pass. Tagged v0.3.2. |
| 3.7 (done, v0.3.3) | Robustness (from the muagent study, section 9.3 and 4.7). **Agent:** a `history` type (the history changes in few places; earlier turns keep only their messages, so reasoning, calls and outputs of every turn but the newest are removed, calls and outputs together); after at least one completed round a cancel, failure, step cap, loop stop, idle timeout or budget stop keeps the turn, with every unanswered call answered `The user interrupted this call: it did not finish, or did not run.` and a closing note `[Stopped after N model calls: reason]` (a failure in the first round saves nothing); response classification (incomplete keeps its partial text, failed, other status, refused, empty reply retried once with a notice); retry of a broken stream up to twice (no final event and no `error` event, or the connection lost after it began; a `Retry` event makes the TUI clear the streamed text); use of `output_item.done` items when `response.completed` has no output; error hints (401, 403, 404, 429, context overflow); unknown tool answered with the list of available tools; at most 8 read-only calls at once; a central 20 KiB cap on every tool result with a note. **Prompt:** one prompt with tools and one without (no tool language, which made some models type calls as text or answer nothing), the model told the user sees only a short preview of results, stable text first and the volatile environment last for caching, statements about denial and previews pinned by tests. **TUI:** everything from the model, tools and servers is sanitised before display (control characters except newline and tab, escape sequences, carriage returns, bidi overrides and isolates, tag characters); Ctrl-C clears typed text, asks for a second press on an empty input once there is a conversation (and quits at once when there is none), and Ctrl-D does the same; the two count together. **Config:** unknown keys rejected, `CHIRON_MODEL` and `OPENAI_BASE_URL` removed, `reasoning_effort`, `max_turns` and `streaming` added. **Tests:** a scenario-driven fake API (broken stream, error event, overflow 400, empty reply, incomplete, refusal), one helper that isolates config and environment, and a mutation check (break the code, see the test fail) for each behaviour. | Each ending in the table of section 4.7 behaves as specified; an interrupted turn keeps its tool work with a note; a broken stream is retried and the partial text cleared; an empty reply is retried once; escape sequences and bidi overrides never reach the screen; a typo in config.json is an error naming the key; the golden screens are unchanged except where the new behaviour shows. Tagged v0.3.3. |
| 3.8 (done, v0.3.4) | Slash commands (from the muagent study, section 4.8). A command list in `internal/tui` (a slice built in one place, no global map): each `Command` has a name, a one-line description, an argument source and a handler. Dispatch as in muagent: the first word is lowercased, the rest keeps its case, whitespace around the line is ignored; a **lone** unknown `/word` is reported and never sent to the model, a longer line such as `/usr/bin is missing` is sent as a prompt; a handler error is shown and the session survives. **Inline suggestion list** between the transcript and the input while typing `/` (matching commands with descriptions, Up/Down to pick, Tab to accept; Enter accepts a partly typed word and runs a whole one; Esc to close), with argument completion from a fixed list (`/effort` levels plus `default`) or from live state. **Output** as dim info entries in the transcript (aligned tables for `/help`, `/context`), never part of the model's history. **Commands refused while a turn runs** (the message says to wait or press Ctrl-C). Commands: `/help` (every command in sorted aligned columns, plus the keys that work), `/exit` and `/quit`, `/clear` (clears the transcript view; history kept), `/reset`, `/model [NAME]`, `/effort [LEVEL]`, `/undo`, `/retry`, `/context` (instructions, tool definitions and history estimated at about 4 bytes of UTF-8 per token, the server's count of the last request when known). Agent additions: `Reset`, `Undo`, `Rewind`, `SetModel`, `SetReasoningEffort`, a token estimator and `ContextEstimate`. Later phases add their own commands with their features: `/tools` (4a), `/servers` and `/reconnect` (4b), `/compact` and `/summary` (9). | Each command behaves as specified in section 4.8; names are unique and every description is one short line; `/help` lists every registered command in sorted order; a lone unknown `/word` is not sent to the model and `/usr/bin is missing` is; a command typed during a turn is refused; `/retry` after `/undo` does not remove an older exchange; `/effort` rejects an unknown level and lists the valid ones; the suggestion list filters as you type; golden screens for the list and for `/help`. Tagged v0.3.4. |
| 4a.1 (done, v0.4.0) | Approvals (section 4.5). **Agent:** a mutating call has its arguments validated, then emits `ApprovalRequested` and waits for the answer or a cancel; an optional interface lets a tool describe its own prompt (summary, details, grant key); session grants kept on the agent by tool name plus grant key, until the program exits and surviving `/reset`; the denial text for the model. Non-interactive `-p`: mutating calls are denied (the model is told so) unless `--yes` is given, which allows every call for that run. **TUI:** centered approval modal (starts on Deny; `y`, `s`, `n`/Esc, arrows, Enter; details scroll, are never cut and show control characters as visible escapes such as `\x1b` and `\r`, with a size line), a denied card state, and `/tools` (run without asking / allowed this session / ask first). Exercised with fake mutating tools in tests; no real mutating tool exists until Phase 5. | Allow, Allow for session and Deny each behave as specified; a denied call is reported to the model and not retried; an invalid call is answered without asking; cancelling closes an open prompt and the call is reported cancelled; session grants are scoped by grant key and survive `/reset`; `-p` denies without `--yes` and allows with it; the modal shows `\r` and `\x1b` as text; golden tests for the modal and `/tools`. Tagged v0.4.0. |
| 4a.2 (done, v0.4.1) | Queue and expandable cards. **Queue:** Enter while a turn runs adds the prompt to a list shown above the input; the next is sent when a turn ends without error; Ctrl-C cancels the turn and drops the queue (the UI says how many were dropped); after a turn that fails on its own the queue is held, a notice says N prompts are waiting and nothing is sent until the next Enter; Up on an empty input pulls the last queued prompt back into the input. Commands are still refused while a turn runs and are never queued. **Cards:** Tab / Shift+Tab select a card when the input is empty, Enter or Space toggle 5 lines versus the whole result, Esc returns to typing; the viewport scrolls so the selected card stays visible. | Queued prompts run in order; an error holds the queue and Ctrl-C drops it; Up returns the last queued prompt; cards expand and collapse and the selected one stays in view; golden tests for the queue and an expanded card. Tagged v0.4.1. |
| 4b.1 (done, v0.5.0) | MCP client, first part (section 4.6). `mcpServers` config typed and validated (`command`/`args`/`env` or `url`/`headers`, `enabled`, `autoApprove`; the other fields arrive in 4b.2 and are an unknown key until then), strict `${VAR}` expansion, URL and redirect checks, parallel background connect with a shared 10 s deadline (a server that misses it stays out until 4b.2's `/reconnect`), stdio and streamable HTTP transports, private stderr logs, tool adapter with unique names and its own approval prompt (server, tool, arguments, where the server runs), result mapping, the central output cap, `server → tool` cards, a startup status line per server and a count in the header, parallel shutdown, and `--no-mcp`. Registry and instructions are built when the connect finishes and handed to the agent (`SetTools`, between turns). `-p` waits for the servers. | A real stdio server and an httptest HTTP server both work, including headers; an unapproved MCP call asks and an `autoApprove`d one does not; Allow for session covers the tool; a cross-origin redirect does not carry headers; a failing or slow server is reported and does not stop the agent; no secret appears in errors, logs or status; `--no-mcp` starts nothing; golden tests for the startup status and the MCP prompt. Tagged v0.5.0. |
| 4b.2 (done, v0.5.1) | MCP client, second part. Restart after a lost connection (3 per session; the call that found it dead fails and is not repeated), `/servers` (state, tool count, hidden-tool count, token cost of the definitions, log path) and `/reconnect [name]` (waits for background startup before deciding what failed, applies its change between turns through `SetTools`, and is how a late or failed server joins), the `tools` allow-list and `instructions` options. | A killed server is restarted by the next call (at most 3 times); `/reconnect` adds a server that missed the deadline and takes effect at the next turn; `tools` rejects a name the server lacks; `instructions` appear in the system prompt framed as notes from the server; golden tests for `/servers`. Tagged v0.5.1. |
| 5 | `read_file` (line-numbered, `offset`/`limit`, default 200 lines, lines cut at 2000 characters), `list_dir`, `write_file`, `edit_file`, working-directory confinement. `edit_file` and `write_file` over an existing file require that it was read in this session and fail if it changed on disk since; a non-unique match returns a message asking for more context or `replace_all`. Edits show a unified diff in the approval prompt (`go-udiff`, already in the module graph). Tool descriptions live in embedded `.md` files. | Reads work and run in parallel; mutations go through the approval prompt; an edit without a prior read, after an outside change, or with an ambiguous match fails cleanly; paths outside the working tree are rejected. |
| 6 | `shell`: every command asks for approval (no read-only allowlist in v1). Output is capped; the truncation marker names a file holding the full output. No background jobs in v1. | A hung command is killed on cancel and the loop recovers; long output is truncated with a pointer to the full file. |
| 7 | `todo` (states pending/in_progress/completed, at most one in_progress, shown in the TUI), `ask_user` with typed questions (`yes_no`, `single_choice`, `multi_choice`, `free_text`; at most 5 choices; a description is required). The todo list lives on the agent and is saved in the session file; it is not rebuilt from the history, because earlier turns keep only their messages and a summary drops more | The model can pause, ask and resume; an invalid question is rejected with a clear error. |
| 8 | `web_fetch` (web search is decided later; see section 8) | Redirects, size limits and non-HTML handled. |
| 9a (done, v0.6.0) | Compaction and budget (section 4.7). `max_context_tokens` budget over the whole request, estimated and corrected by the server's `usage.input_tokens`; **automatic** in-turn shortening of the tool results of earlier rounds when the next request would pass the budget; `/compact [focus]` summarises the oldest turns with the chat model (headings, refusal rules, a summary that is notes and not instructions, always one summary, never an item of the history); `auto_compact: true` does the same before a turn at 75% (off by default); one retry after a server refuses a request as too long; `/summary` and the budget rows of `/context`. | A long conversation is summarised and still answers questions about the early turns (checked against a real model); a bad summary is refused and the history stays whole; `/retry` and `/undo` leave a valid history after a summary; shortened results say how much is hidden; `/context` shows the budget and where the summary starts. Tagged v0.6.0. |
| 9b (done, v0.6.1) | Sessions. Every conversation is saved automatically after each turn (JSON in `$XDG_STATE_HOME/chiron/sessions/`, directory `0700`, files `0600`, written atomically; opt out with `sessions: false` or `--no-save`); `--resume` resumes the latest session of this directory and `--resume ID` a named one (v0.6.1 had `--continue` for the first; it was folded into `--resume` after v0.7.0), `/sessions` opens a list of this directory's sessions that resumes the chosen one in place or deletes it after a confirmation (not the one in use); the transcript (messages and tool cards) is rebuilt from the saved items; reasoning items are dropped when the model or endpoint differs from the one that wrote them. | A resumed session continues and answers about what came before; a file is private and a failed write never loses the previous file; opting out writes nothing; a resumed history is valid (every call has its output). Tagged v0.6.1. |
| 9c (done, v0.6.2) | Input niceties. Prompt history: **Up and Down recall earlier prompts** while the input is empty (or holds an unedited recalled prompt), persisted in a private file (about 1,000 entries), with Ctrl-R search; this replaces Up/Down line scrolling, which goes back to PgUp/PgDn and the mouse wheel (a queued prompt is still taken back first by Up); `$EDITOR` for a long prompt (`tea.ExecProcess`); Ctrl-Z suspend (`tea.Suspend`). | Up and Down walk through the history and back to the draft; a recalled prompt can be edited; history survives a restart and holds no duplicates in a row; Ctrl-R finds an old prompt; the editor's text comes back as the prompt; the live end-to-end check is updated for the changed keys. Tagged v0.6.2. |

## 8. Open items

- Real checks passed on 2026-11-17: (1) local server `http://localhost:11434/v1`, model `beam:cloud`; (2) the OpenAI
  API, model `gpt-5.4-mini`. In both, `chiron -p` works and a two-turn replay with `store=false` and
  `include: reasoning.encrypted_content` recalls earlier context. Caveat: the OpenAI run produced no reasoning
  items (history held only the user and assistant messages), so replaying an encrypted reasoning item is covered
  by the fake-server test but not yet by a real response. Phase 3 added `TestRealAPIToolCall` (function call,
  result, follow-up turn); it passed against the local server (`beam:cloud`). Run it against OpenAI by setting
  `base_url` to `""` in the config to see whether `gpt-5.4-mini` produces reasoning items with tools.
- Bubble Tea stack verified in Phase 2: `charm.land/bubbletea/v2` v2.1.1, `bubbles/v2` v2.2.1, `lipgloss/v2`
  v2.1.1, `glamour/v2` v2.0.1. In v2, `View()` returns `tea.View`, keys arrive as `tea.KeyPressMsg`, and the
  terminal background is requested with `tea.RequestBackgroundColor`.
- Known limit of the TUI: the transcript is rebuilt as one string on every streamed token (see section 9).
- Check which features survive a custom `base_url`. Servers that only partly implement the Responses API may
  lack `reasoning.encrypted_content` or `responses.compact`. Phase 1
  should test against the real API first; then decide whether to disable those features when `base_url` is set.
- Web search: undecided, to be chosen by the user. Candidates: an MCP server (the config already supports
  search-capable ones such as a Brave-backed toolbox) or a local tool. The OpenAI built-in tool is excluded.
- Confirm the exact name of the MCP SDK's Streamable HTTP client transport and how to attach custom headers
  (Phase 4).
- Phase 4a.1: the modal overlay was prototyped with lipgloss `Layer`/`Compositor` and works (done); no fallback needed.
- Phase 4b.1: confirm OpenAI's tool-count and function-name limits for the Responses API with a real request.
- Pick the HTML-to-markdown library (Phase 8).

## 9. Lessons from other agents (studied 2026-11-17)

Crush (`charmbracelet/crush`) is the reference; gocode (`firebitsbr/gocode`) is a scaffold: no agent loop, tools
never invoked, permissions defined in config but never enforced, so it was only a source of ideas.

Adopted (see the phase table): a real system prompt, loop detection, stream idle timeout, usage and context
tracking, queued prompts, scoped session grants with a Deny-first dialog, read-before-edit and modified-since-read
checks, diffs in approvals, view limits, output cap with a spill file, a one-in_progress todo rule, typed
questions, embedded tool descriptions, expandable tool cards with spinner and timing.

Decided against for now: a read-only shell allowlist (would add `mvdan.cc/sh` and a list to keep strict),
background jobs, whitespace-tolerant edit fallback, `multiedit`, hooks, sub-agents, LSP, SQLite, multiple
providers, telemetry, and a virtualised chat list (revisit when long sessions feel slow: the transcript is currently
rebuilt as one string on every streamed token). Live markdown while streaming was first decided against and built
later: the reply in progress is drawn as markdown, redrawn on each spinner tick and not on every token.

TUI rules we already follow and keep: no I/O in `Update`, no state changes inside commands, use `x/ansi` for any
string work that may contain escape codes.

### 9.1 UI decisions (2026-11-17)

Built in Phase 3.6. Known limits: code blocks in answers use glamour's syntax colours on the terminal background
without a band (glamour paints backgrounds per token, which looked patchy); text is not width-checked below
40 columns; the input box and header assume at least 20 columns.

Decided after a second pass over Crush's UI code (`internal/ui`: styles, layout, header, sidebar, chat items, tool
renderers, diff view, permission dialog, spinner) and a render of chiron's own UI. Problems found in ours:
misaligned left edges (user and tool lines at column 0, assistant at column 2), raw JSON in tool headers, no
visual identity for user messages, a prompt repeated on every input row, a fixed 3-row input, key hints and
usage crammed into one faint line above the input, and no header or welcome state.

Chosen: Phase 3.6 before Phase 4; a palette adapted to the terminal background (not a painted fixed theme); a
header row and no sidebar (MCP and todo status can become small pills above the input later); collapsed tool
cards of 5 lines. Not copied: the gradient logo, the sidebar, the painted background and Crush's custom screen
buffer renderer. Overlays for the Phase 4 permission dialog can use lipgloss v2 `Layer`/`Compositor`
(present in v2.1.1; to be verified when used).

### 9.2 Lessons from muagent's MCP client (2026-11-17)

Read: `mcp_client.py` and `mcp_hub.py` (Python). Adopted: ignoring a server's own read-only claim, strict `${VAR}`
expansion with an error naming the variable, https or loopback URLs without credentials and no cross-origin redirects,
private stderr log files, unique stable tool names, required-keys-only argument checks, one shared startup deadline
with parallel connect and close, restart after a lost connection (3 per session, the failed call not repeated),
a `tools` allow-list with a typo guard, and opt-in server instructions. Decided otherwise: the child process
inherits the full environment (muagent passes only a short allow-list), and a `readOnly` flag per server was not
chosen (only per-tool `autoApprove`). Not needed in Go: the background thread and event-loop machinery. Not adopted
now: per-server timeouts, image pass-through, a live tool list.

### 9.3 Lessons from muagent as a whole (2026-11-17)

Read: its README, `CLAUDE.md`, `TOOLS.md`, `TURN.md`, `agent.py`, `responses.py`, `errors.py`, `tools.py`, `console.py`,
`compaction.py`, `context.py`, `commands.py`, `config.py`, `repl.py`, `prompts.py` and the fake API's header. Adopted
(Phase 3.7 unless noted): keeping stopped turns with a note, response classification and the empty-reply retry,
stream retry with a cleared partial text, the output fallback for servers that omit it, error hints, validation
before approval (4a), the 8-call cap and the central output cap, sanitising everything displayed, two prompts and
prompt statements pinned by tests, Ctrl-C and Ctrl-D that protect an in-memory conversation, rejecting unknown
config keys, a single environment variable, `reasoning_effort`, `max_turns` and `streaming`, a scenario-driven fake
API, mutation checks, and pruning the tool work of older turns. Phase 9 takes its client-side compaction, slash
commands, history and `$EDITOR` ideas. To do after 4a: a `TURN.md` walkthrough of one turn for this project.
Decided against copying: its one-line-per-call display with no result (our cards show a preview) and its Python
thread machinery. Also noted: its tests show prompt wording must be checked on real models (tool language in a no-tools
prompt made gpt-5.5 type calls as text and gemma4 return nothing, and a rule against typing calls made gpt-5.5 do
it more often), so prompt changes get pass/fail scenarios run several times on the models in use.

### 9.4 Lessons from muagent's slash commands (2026-11-18)

Read: `commands.py`, the command completer in `repl.py`, `tests/test_commands.py` and the command tests in
`tests/test_repl.py`. Adopted: the dispatch rules (a lone unknown `/word` reported, a longer line sent as a prompt,
the first word lowercased and the rest case-preserved), one-line descriptions with a test, completion with
descriptions and dynamic arguments, a `/help` that lists every command and only the working keys, and the specific
behaviours of `/retry`, `/undo`, `/effort`, `/model`, `/context`, `/tools`, `/servers` and `/reconnect`. Changed for
Go and an asynchronous agent: a command list instead of a global registry, commands refused while a turn runs, an
inline suggestion list instead of prompt_toolkit's menu, and output as transcript entries. Commands that need a
feature arrive with that feature.

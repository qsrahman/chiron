# AGENTS.md — developer guide

Read [PLAN.md](PLAN.md) first for goals, decisions and the phase table.

## Layout

```
cmd/desk-agent/     main: flags, wiring, -p mode
internal/config/    config file, env and flag precedence
internal/agent/     loop, events, history, system prompt (no UI imports)
internal/tools/     Tool interface, MustNew, Registry (built-in tools are added from Phase 5)
internal/tui/       Bubble Tea chat UI (model, theme, layout, cards, chrome; golden tests in testdata/)
internal/mcp/       MCP servers adapted to tools.Tool: connect, adapter, results, transports (Phase 4b)
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
(Python `pty`) sending a prompt and `Ctrl-C`; the TUI is not covered by the go tests' real terminal output. A pseudo-terminal check races the process exit: poll for it instead of reading it once.

Real-server checks (cost a few tokens, never run by default; skip without a key or base URL). They use the
default config file and the environment, like the CLI:

```bash
go test -tags=integration -run TestRealAPI -v -count=1 ./internal/agent
```

## Live end-to-end check

`e2e/run.sh` runs the real binary in a pseudo-terminal and reads the screen through a terminal emulator (pyte, in a
venv it creates under `e2e/.venv`). Nothing is mocked: the model is the one in your config (or
`DESK_AGENT_LIVE_BASE_URL` and `DESK_AGENT_LIVE_MODEL`), the MCP server is the test binary of `internal/mcp` run as a
real stdio process. It costs a few dozen model calls and takes a few minutes; `-k name` runs the scenarios whose name
contains `name`. The `toolbox_*` scenarios use the `toolbox` server of your own config file when it has one (and skip
otherwise): files are written and edited only in a temporary working directory and checked on disk afterwards, the
shell runs `echo`, one page is fetched and one Brave search is made, and your `autoApprove` list decides which calls
ask. It is never part of `go test`. Run it after a change to the UI, the turn loop or MCP, because the
unit tests look at the model's state and the entries, not at the screen (a command's output that was never refreshed
onto the screen passed every unit test and was only seen here). When a scenario fails, decide first whether the
harness or the program is wrong: the emulator must implement every sequence the renderer uses (it needed scroll up
and down added), and the model is fast, so a prompt that must still be running when the next key is typed has to be
a slow one (an essay, not a short count). Invariants checked after every turn: nothing in the last column, no stale
text from a redraw, no leftover stand-in character, no banner.

## Conventions

- **Do not hand-roll.** Before writing a utility, check `openai-go`, `modelcontextprotocol/go-sdk`,
  `google/jsonschema-go` and the standard library. Tool schemas come from `jsonschema.For[Args]()`.
- **The agent core never imports the TUI.** It emits `Event` values; front-ends render them. Approvals and
  questions are events carrying a reply channel.
- **History is local** (`store=false`). Append response output items unchanged (via `param.Override` and
  `RawJSON()`); never rebuild them from parsed fields, or reasoning items and unknown fields are lost. Items carry
  their kind (`item` in `history.go`) because a replayed raw item cannot be asked what it is.
- **What must stay true of a turn** (`turn.go`, pinned by tests):
  - The history changes only through `history.save`; a turn in progress works on a copy.
  - Every function call has its output before anything is sent or saved (`answerOpenCalls`).
  - A turn is saved whole (with its reply or a closing note) or not at all. After at least one completed round, a
    cancel, failure, guard or step cap keeps the turn with a note, because tools may have had effects; a failure in
    the first round saves nothing.
  - The newest turn keeps its reasoning, calls and outputs; earlier turns keep only their messages, and calls and
    outputs are dropped together.
  - The note the model reads names the kind of failure (`reasonOf`) and never repeats text a server wrote.
  - A cancellation stays recognisable (`errors.Is(err, context.Canceled)`) through `describeError`.
- **Text from the model, tools and servers is sanitised** before display (`sanitize.go`, applied in `render` and to
  the streamed text and the session info). New code that shows such text must go through the same path.
- **Config:** unknown keys are an error; the only environment variable is `OPENAI_API_KEY`; the client is always
  built with an explicit base URL (`newClient`), because the OpenAI library reads `OPENAI_BASE_URL` itself.
- **Tool errors go to the model** as the tool output text, not as Go errors that abort the loop.
- **Adding a tool:** define an `Args` struct (`json` tags for names, `jsonschema` tags for descriptions), write
  `func(ctx, Args) (string, error)`, wrap it with `tools.MustNew`, and add it to the registry in `main`. The
  agent loop and the TUI need no change; the TUI shows every call as a card.
- **MCP.** `internal/mcp` is the only package that imports the MCP SDK. A `Hub` starts the servers in parallel
  (`Start`), `Wait` blocks until they have answered, `Tools` returns the adapters with unique names, `Close` ends
  everything. Its tools are never read-only and may be `tools.Trusted` (config `autoApprove`), `tools.Titled`
  (`server → tool`) and `tools.Describer` (with a `Source`). The agent gets them through `Agent.SetTools`, only while
  idle: `main` runs `connectMCP` in the TUI's `Info.Startup`, and the UI holds prompts until it is done. Secrets
  (header and env values, URLs) never go into an error, a log or a status. Tests start the test binary itself as a
  stdio server (`TestMain` with an environment switch; its tools include `die` and `pid` for the restart tests) and use
  `httptest` with the SDK's streamable handler. A server restarts itself in `server.live` (called by every tool call);
  `Hub.Reconnect` is for the commands and must only run between turns.
- **Queue and cards.** Prompts typed during a turn wait in `Model.queue` (`queue.go`) and start in
  `nextQueued` when the turn closes; startTurn must never touch the input, so a draft survives. Cards are selected
  with `Model.card` (`cards.go`); anything that rewrites `entries` must keep `card` valid (`/clear` resets it).
- **Approvals.** Every call to a tool whose `ReadOnly()` is false goes through `Agent.approve` (`approve.go`)
  unless `Settings.AutoApprove` or a session grant covers it. A tool may implement `tools.Describer` (summary,
  details, grant key) and `tools.Validator`; `MustNew` tools validate against their schema, and an invalid call is
  answered without asking. The TUI shows the details with `escape`, never `sanitize`: the user must see exactly what
  runs. Tests that are not about approval use `testSettings`, which allows every call.
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
- **Use what Bubble Tea gives, before writing it.** The `View` carries more than the screen: `WindowTitle`,
  `ProgressBar`, `ReportFocus` (then `FocusMsg` and `BlurMsg`), `Cursor`, `MouseMode`, and `KeyboardEnhancements`
  (then `KeyboardEnhancementsMsg`); `tea.Raw` writes a control sequence (the bell), `tea.SetClipboard` copies through the
  terminal. The input's cursor is the terminal's (`SetVirtualCursor(false)`, `Cursor()`), moved by the rows above the
  box and its border in `View`. Hit-testing of the dialog is the compositor's: the buttons are child layers with an ID
  and `Compositor.Hit` says which was clicked; **a layer's `Z` is absolute, not relative to its parent**, so children
  need a higher one or they sit under the parent and never get the hit. Terminals ignore what they do not support, so
  none of these needs a check, except `Shift+Enter`, which is named in the help only after `KeyboardEnhancementsMsg`.
- **UI look.** There is a margin of one column on each side. Colours come only from the palette in `theme.go`; never hard-code a colour or an ANSI index
  elsewhere, and never paint the terminal background (bands are derived from it). Everything in the transcript
  shares the `gutter` indent and is capped at `maxTextWidth`; `render` clamps every line to the content width,
  because a line wider than the terminal wraps and wrecks the layout. Tool cards show parsed arguments, never raw
  JSON. Key bindings live in `keys.go` and feed both the key handling and the help bar.
- **Glamour gotcha.** Glamour registers a custom `Chroma` colour table once under a single global name, so
  the first theme to render wins for the whole process. Code colours therefore use named chroma styles
  (`monokai`, `github`) via `CodeBlock.Theme`; `TestCodeColoursFollowTheTheme` guards this. Glamour also counts a
  tab as zero cells, so tabs are expanded before rendering, and it pads output with blank lines, which
  `trimBlankLines` removes so every gap between items is exactly one row. Fenced code blocks at the left edge are
  not glamour's: `splitFences` cuts them out and `codeBand` draws them on a shaded band (the highlighter resets its
  colours after every token, so the band colour is put back after each reset). Fences inside list items stay with
  glamour. Lines are wrapped after a hyphen, which splits `--config`: `keepHyphenated` swaps the hyphens inside words
  for a stand-in before rendering and they are swapped back last (after the list items are re-wrapped).
- **Slash commands.** A command is one entry of `commandList` in `internal/tui/commands.go` (name, usage,
  one-line description, optional argument list, handler); adding one needs no other change, and `/help` and the
  suggestion list pick it up. The TUI talks to the agent through the `tui.Agent` interface, so a new command that
  needs the agent adds a method there and to the test fake. Output goes to the transcript with `say` and
  `sayTable` (dim info entries, never in the model's history). Commands run only between turns.
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
- **Tests use no network.** Point the SDK at an `httptest` server with `option.WithBaseURL` and scripted SSE. The
  fake server in `agent_test.go` scripts one reply per request (`newFakeServer` for streams, `newScriptedServer` for
  HTTP errors and JSON) and has builders for the cases a client must handle: a broken stream, an `error` event, an
  empty, refused, incomplete or failed response, and an API error. Add a builder there when a new case appears. To
  keep a test off the network when the code builds its own client, inject a transport with
  `option.WithHTTPClient` (see `newClient`). Test the TUI through `Update` with synthetic messages.
- **Prompt changes are tested on real models.** Reading a prompt tells you little: tool language in a no-tools prompt
  made some models type calls as text or answer nothing. Compare old and new on scenarios with a pass/fail check each,
  several runs, on the models in use, and pin the sentences the code depends on with tests.
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
2. Implement, then run build, vet and test. For a behaviour change, also break the code on purpose and see the
   test fail (a mutation check): a test that cannot fail proves nothing.
3. Review and update `PLAN.md`, `README.md`, `AGENTS.md` and `CHANGELOG.md` if the change affects features,
   architecture or conventions.
4. Commit with a Conventional Commit message (`feat(tools): ...`, `fix(agent): ...`, `docs: ...`). No co-author
   trailer.
5. At the end of a phase, move `CHANGELOG.md` entries from `Unreleased` to the new version and tag `v0.N.0`.

## Changelog

`CHANGELOG.md` follows Keep a Changelog 1.1.0 and SemVer. Add entries under `Unreleased` in the same commit as
the change.

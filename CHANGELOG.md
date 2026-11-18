# Changelog

All notable changes to the codebase are documented in this file. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows [Semantic Versioning](https://semver.org/)

## [Unreleased]

### Changed

- The approval dialog shows the arguments of a call as `name: value` lines, in the order the model gave them, with the
  values decoded (`&&`, not `\u0026\u0026`), a value of several lines under its name and a short structure on one
  line, instead of the raw JSON. `tools.FormatArguments` does this for the generic prompt and for MCP tools.

### Changed

- Tool cards are closed by default: one line, with the number of lines returned and the duration at the right end
  (`cancelled` and `denied by you` also go there); a failed call also shows the first line of its error. Opening a
  card (Enter or Space) shows the whole output; the five-line preview is gone.
- Consecutive tool cards have no blank line between them, so a run of calls reads as a list, and the server of an MCP
  tool is muted in its title.
- The top heading of an answer is no longer a coloured badge, and inline code is a colour without a padded background
  (which broke into patches where a line wrapped).

### Added

- Up and Down scroll the transcript a line while the input is empty, and the mouse wheel scrolls it (or the details of
  an open approval dialog). With the mouse reported to the program, selecting text in the terminal needs Shift held
  (Option on macOS). `/help` lists both.

## [0.5.2] - 2026-11-18

### Changed

- `autoApprove` of an MCP server may also be `true` (every tool of the server, those it adds later included) or `false`;
  before, only a list was accepted and `true` made the config fail to load. Anything else is an error that says what
  is accepted.

## [0.5.1] - 2026-11-18

### Added

- MCP servers that die are started again by the next call, at most 3 times per session; a call that was already sent
  when the connection broke fails and is not repeated. The connection is watched, and an idle one is checked every
  30 s.
- `/servers` (state, tools offered and hidden, the estimated cost of the definitions, restarts, log path) and
  `/reconnect [NAME]` (start the named server, or those that failed or lost their connection, afresh; a server that
  was too slow at launch joins this way, and the agent gets its tools for the next prompt).
- The `tools` allow-list (a name the server lacks fails the server and lists what it offers) and the `instructions`
  option (the server's usage notes in the system prompt, framed as notes from the server, cut to 4,000 characters).
- `tools.EstimateTokens`, shared by `/context` and `/servers`; `agent.ServerNote`.

## [0.5.0] - 2026-11-18

### Added

- MCP client: `mcpServers` (`command`/`args`/`env` or `url`/`headers`, `enabled`, `autoApprove`) are started in
  parallel at launch with one 10 s deadline, over stdio or streamable HTTP. The chat opens at once ("connecting…")
  and prompts sent meanwhile wait; each server is reported with how it started and the header shows `mcp 2/3`. A
  server that fails or is too slow stays out and does not stop the others. `-p` waits for the servers.
- MCP tools are named `server__tool` (unique, at most 64 characters, at most 128 per request), show on cards as
  `server → tool`, and ask before every call unless listed in `autoApprove`; the dialog says where the server runs.
  Results map to text (images and audio become placeholders) and go through the 20 KiB cap; a call times out after
  2 minutes.
- Strict `${NAME}` expansion in `command`, `args`, `env`, `url` and `headers` (an unset variable names itself, never a
  value); `https` or loopback URLs without credentials; headers go to the configured host only and a redirect to
  another address is refused; a local server's error output goes to a private log under the state directory.
- `--no-mcp` starts no servers for a run.
- `Agent.SetTools`, `tools.Trusted`, `tools.Titled`, `tools.Prompt.Source`, `ToolStarted.Title`.

### Fixed

- The approval dialog ignores keys for half a second after it opens (Ctrl-C excepted): a "y" typed ahead could answer
  it.
- The dialog no longer makes the screen taller than a small window, and leaves the top row free when it can.
- The "For the session" line shows control characters as escapes, like the details.

### Changed

- `ToolFinished` carries one `Outcome` (`CallOK`, `CallFailed`, `CallCancelled`, `CallDenied`) instead of the
  overlapping `Failed`, `Cancelled` and `Denied` flags, and tool cards keep the same outcome.
- The suggestion, queue and card state of the UI model are grouped into three small structs.
- Internal: `bandLines` and `failureLines` take the entry, the queue height comes from its rows, the dialog choices have
  names, `/context` shares the number formatting, and a few tests were tidied.

## [0.4.1] - 2026-11-18

### Added

- Queued prompts: Enter during a turn adds the prompt to a list above the input (three shown, the rest counted); each
  is sent when the turn before it ends without error. Ctrl-C cancels the turn and drops the queue and says how many
  were dropped; after an error the queue is held and Enter on an empty line sends the first. Up on an empty input
  takes the last queued prompt back. Commands are never queued.
- Expandable tool cards: Tab and Shift+Tab select a card on an empty input, Enter or Space toggle the preview and the
  whole output, Esc or typing returns to the input, and the screen scrolls to keep the card in view.

### Changed

- Enter during a turn queues the prompt; before, it left the text in the input.

## [0.4.0] - 2026-11-18

### Added

- Approvals: a call to a tool that is not read-only asks first. The agent validates the arguments (a JSON object
  matching the tool's schema) and answers an invalid call without asking, then emits `ApprovalRequested` and waits
  for a `Decision` (`Deny`, `Allow`, `AllowSession`) or a cancel. Session grants are kept by tool and grant key until
  the program exits. A denied call tells the model not to retry it.
- Optional `tools.Describer` (summary, details, grant key) and `tools.Validator`; `MustNew` tools validate against
  their schema.
- A centered approval dialog that starts on Deny. Its details are never cut, scroll, and show control characters as
  visible escapes. Denied calls get their own card state.
- `/tools`: which tools run without asking, which are allowed for the session and which ask first.
- `--yes` allows every call; with `-p`, calls that would ask are denied unless `--yes` is given.

### Fixed

- A reply is drawn as markdown while it streams (bold, lists, code); before, the raw `**` and `-` were visible until
  the turn ended. It is redrawn on each spinner tick, not on every token.
- Ctrl-C and Ctrl-D ask before quitting whenever a conversation is in memory: `/clear` no longer lets a quit through
  and `/reset` no longer asks.
- A tool result with invalid UTF-8 near its start is no longer cut to a few bytes.

### Changed

- A new line in the message is Shift+Enter (shown in the help bar and `/help`); Alt+Enter and Ctrl+J still work for
  terminals that cannot tell Shift+Enter from Enter.
- Wrapped lines of a list item sit under the item's text, not under its bullet (glamour does not do this).
- The header reads the model name from the agent, so `Info.Model` is gone and `/model` has one place to update.
- The suggestion list uses the key map (`up`, `down`, `complete`, `dismiss`) like every other binding.
- The welcome text mentions `/` for commands.

## [0.3.4] - 2026-11-18

### Added

- Slash commands: `/help`, `/exit`, `/quit`, `/clear`, `/reset`, `/model [NAME]`, `/effort [LEVEL]`, `/undo`,
  `/retry` and `/context`. A lone unknown `/word` is reported and not sent; commands are refused while a turn runs.
- An inline suggestion list while typing `/` (Up/Down, Tab, Esc), with `/effort` levels completed from a list.
- Info entries in the transcript for command output, including aligned tables.
- Agent: `Reset`, `Undo`, `Rewind`, `SetModel`, `SetReasoningEffort`, `Settings`, `HasConversation` and
  `ContextEstimate` (about 4 bytes per token; encrypted reasoning is not counted).

### Changed

- The TUI takes a `tui.Agent` interface instead of `tui.Sender`.

## [0.3.3] - 2026-11-18

### Added

- Turn semantics (from the muagent study): a `history` type whose older turns keep only their messages; a turn that
  stops after tools ran (cancel, failure, loop guard, step cap) is kept with a closing note and every unanswered
  call answered as interrupted; a failure in the first round saves nothing.
- Response handling: incomplete, failed, refused and empty responses are told apart (an empty reply is asked for
  again once; a cut-off reply keeps its text); a broken stream is retried up to twice with a `Retry` event that
  clears the streamed text; finished output items are used when `response.completed` carries none (some compatible
  servers); descriptive API errors with hints (wrong key, model or base URL, quota, too long); unknown tools are
  answered with the list of available tools.
- At most 8 read-only tool calls at once and a 20 KiB cap on every tool result, in the agent.
- Two prompts: with tools and without (no tool language when there are none). The prompt order puts the environment
  last, and a test pins the sentences the program depends on.
- Everything shown from the model, tools and servers is sanitised (control characters, escape sequences, carriage
  returns, bidi overrides, tag characters).
- Ctrl-C clears typed text; Ctrl-C and Ctrl-D on an empty input need a second press once there is a conversation.
- Settings `reasoning_effort`, `max_turns` and `streaming`, with flags `--reasoning-effort`, `--max-turns`,
  `--streaming` and `--no-streaming`.
- Tests: a scenario-capable fake Responses API, unit tests for history, error descriptions and response
  classification, and mutation checks (each behaviour was broken on purpose to see its test fail).

### Changed

- **Config:** a key that is not a setting is an error naming the key and the file. The environment now has one
  variable, `OPENAI_API_KEY`: `OPENAI_BASE_URL` and `DESK_AGENT_MODEL` are no longer read, and the client always gets
  an explicit base URL, because the OpenAI library would otherwise read `OPENAI_BASE_URL` itself.
- `agent.New` takes `agent.Settings`; `agent.BuildInstructions` takes whether the model has tools; `config.Load`
  returns a `config.File` and `config.Resolve` takes the file, its path, the key and the flags.
- The final reply of a turn is stored as plain text; its reasoning is no longer replayed.

### Fixed

- A turn with no text and no tool call ended silently with an empty answer.

### Changed (plan)

- Studied muagent's slash commands. Added Phase 3.8 (slash commands, tag v0.3.4: dispatch rules, inline suggestion
  list, info entries in the transcript, commands refused while a turn runs, and `/help`, `/exit`, `/quit`, `/clear`,
  `/reset`, `/model`, `/effort`, `/undo`, `/retry`, `/context`). `/tools` moves to 4a, `/servers` and `/reconnect`
  to 4b, and `/compact` and `/summary` stay in Phase 9. Details in `PLAN.md` sections 4.8 and 9.4.

### Changed (plan)

- Studied muagent (a Python agent) and recorded the findings in `PLAN.md` sections 4.7 and 9.3. Added Phase 3.7
  (Robustness, tag v0.3.3): keep stopped turns with a note, response classification and retries, stream retry,
  error hints, central output cap, sanitising, two prompts, Ctrl-C/Ctrl-D protection, strict config (unknown keys
  rejected, only `OPENAI_API_KEY` from the environment, new `reasoning_effort`, `max_turns` and `streaming`
  settings) and a scenario-driven fake API. Phase 9 now uses client-side compaction instead of `responses.compact`
  and prunes the tool work of older turns.
- `AGENTS.md`: removed duplicate stale lines from the layout list; added the mutation-check and prompt-testing
  conventions.

### Changed (plan)

- MCP design revised after reading muagent's MCP client: the server's own read-only hint is ignored (only
  `autoApprove` skips a prompt, so MCP calls run one at a time), strict `${VAR}` expansion, URL and redirect checks,
  private stderr logs, unique stable tool names, restart after a lost connection (3 per session), a `tools`
  allow-list and opt-in `instructions`. The child process inherits the full environment by choice.

### Changed (plan)

- Phase 4 split into 4a (approvals, queued prompts, expandable cards; tag v0.4.0) and 4b (MCP client; tag v0.4.1) and
  elaborated in `PLAN.md` sections 4.5 and 4.6: centered approval modal, a per-server `autoApprove` list, MCP servers
  connecting in the background with a 10 s limit and a fixed tool list, 2-minute calls and a 50 KB result cap,
  Ctrl-C dropping the queue, Tab/Enter to expand cards.

### Changed

- User messages are a shaded block with a brand-coloured bar (previously only a thin bar).
- Headings in answers no longer show their `##` markers.
- A blank row separates the header from the transcript.

### Fixed

- The light theme showed code with the dark theme's colours: glamour registers its custom code colour table once,
  under one global name. Code now uses named chroma styles (`monokai` for dark, `github` for light).
- Answers ending in a code block were followed by a double gap; blank padding rows from glamour are now trimmed so
  every gap is one row.

## [0.3.2] - 2026-11-17

### Added

- UI foundation (from the Crush UI study): a semantic palette with dark and light variants chosen from the
  terminal background, with shaded bands derived from it (the terminal background is never painted).
- Layout: one margin, a shared 2-column gutter, a text width cap of 120, a 1-row gap between items, a brand-coloured
  bar on user messages, and a clamp so no line is ever wider than the terminal.
- A header row (name, directory, git branch; model and context usage on the right) and a welcome screen.
- Tool cards: parsed arguments instead of raw JSON, a spinner while running, `✓` / `×` / cancelled markers,
  duration for slow calls, output on a band cut to 5 lines (one hidden line is shown instead), an `ERROR` badge for
  failures, and syntax-highlighted shell commands (chroma).
- A framed input box with a prompt on the first line only and a height that follows the text, a working/cancelling
  indicator with elapsed time in the transcript, and a bottom help bar built from the key bindings that also drive
  the key handling.
- A glamour style derived from the theme (no document margin, theme colours for headings and inline code).
- `agent.GitBranch`, and `Cancelled` on `ToolFinished` for calls interrupted by a cancelled turn.
- Golden-file tests for the welcome screen, a full conversation at 50, 100 and 160 columns, a running tool, a
  cancelled turn and a multi-line input, plus tests for width limits, themes, argument parsing, the spinner and
  input growth.

### Changed

- `tui.New` and `tui.Run` take an `Info` struct (version, directory, branch, model, context window) instead of
  separate arguments. The one-line status bar is replaced by the header and the help bar.

### Fixed

- Tab characters in code blocks made lines 4 columns too wide (glamour counts a tab as zero cells); tabs are now
  expanded before rendering.

### Changed (plan)

- Studied Crush's UI in depth and recorded the findings and decisions in `PLAN.md` section 9.1. Added Phase 3.6
  (UI foundation: theme adapted to the terminal, layout, header, tool cards, input, help bar, markdown style,
  welcome screen, golden tests) before Phase 4.

## [0.3.1] - 2026-11-17

### Added

- System prompt: embedded template with guidance on working, tool use and communication, an environment block
  (working directory, platform, date, git branch and state) and the project's `AGENTS.md` or `CLAUDE.md`
  (cut at 32 KB). Replaces the one-line default; `agent.BuildInstructions` builds it.
- Loop detection: a turn stops when the same tool step repeats more than 5 times among its last 10.
- Stream idle timeout (2 minutes without data), reported as "model stopped sending data" rather than a cancel.
- `Usage` event and a status-line token count; optional `context_window` config adds the percentage.
- Tests: prompt rendering, project file selection and truncation, git summary, loop detection, idle timeout,
  usage events and the status line. The opt-in real-server tests now use the real system prompt.

### Changed

- `agent.DefaultInstructions` is gone; `main` passes the built instructions. `tui.New` and `tui.Run` take a
  context window argument (0 when unknown).

### Changed (plan)

- Studied Crush and gocode; recorded the findings in `PLAN.md` section 9. Added Phase 3.5 (system prompt, loop
  detection, stream timeout, usage in the status line) and extended Phases 4 to 7 and 9 with the adopted ideas:
  richer approvals with scoped grants and a Deny-first dialog, queued prompts, expandable tool cards,
  read-before-edit and diffs, shell output cap, a typed `ask_user`.

## [0.3.0] - 2026-11-17

### Added

- `internal/tools`: the `Tool` interface, `MustNew` (JSON schema from an `Args` struct via
  `google/jsonschema-go`, strict argument decoding) and `Registry`.
- Tool loop in `internal/agent`: executes the function calls the model makes and returns their output, until
  the model answers. Consecutive read-only calls run concurrently, all others one at a time in model order;
  outputs go back in call order. Failures, unknown tools and bad arguments are returned to the model as text.
  A turn is limited to 25 model calls; cancelling while tools run stops the turn.
- `ToolStarted` and `ToolFinished` events.
- Tool cards in the TUI (running, succeeded, failed, with a short output preview); text the model wrote before
  a call stays above its card.
- Tests: concurrency and ordering with fake tools, error handling, step limit, cancellation, history across
  turns, tool cards. `TestRealAPIToolCall` (opt-in) checks a real call and follow-up turn.

### Changed

- `agent.New` takes a `*tools.Registry` (`&tools.Registry{}` for none). No tools are registered in `main` yet.

## [0.2.0] - 2026-11-17

### Added

- `internal/tui`: Bubble Tea v2 chat UI with a scrollable transcript, multi-line input, token streaming,
  markdown rendering of finished answers (dark or light theme from the terminal background), a status line,
  and window-resize handling.
- Keys: `Enter` sends, `Ctrl-J`/`Alt-Enter` adds a line, `PgUp`/`PgDn` scroll, `Ctrl-C` cancels a running turn
  (a second press quits), `Ctrl-D` quits on empty input.
- `desk-agent` without `-p` now starts the chat UI and keeps conversation history for the session.
- Update-level tests with a fake sender (streaming, markdown, busy/empty Enter, cancel, quit, error, resize,
  theme).

### Changed

- Cancelled or failed turns keep their partial text on screen but stay out of the agent's history.

### Added

- Opt-in real-server test (`-tags=integration`) that checks the model name, `reasoning.encrypted_content` and a
  two-turn history replay, using the default config file and the environment.

## [0.1.0] - 2026-11-17

### Added

- `internal/config`: config file loading (`model`, `base_url`, `api_key`), `${VAR}` expansion, precedence of
  flags over environment over file over the default model, and a warning for a literal `api_key` in a file
  that other users can read.
- `internal/agent`: streaming conversations on the Responses API with `store=false`, local history that
  replays output items (including encrypted reasoning) unchanged, and `TextDelta` / `Done` / `Error` events.
- `desk-agent -p "prompt"`: non-interactive mode with `--model`, `--base-url` and `--config` flags; `Ctrl-C`
  cancels the turn.
- Fake-server tests (no network) for streaming, history replay, failed turns, a stream that never completes
  and cancellation.
- Project scaffold: Go module `github.com/qsrahman/desk-agent`, `desk-agent --version`, smoke tests.
- Documentation: `PLAN.md` (product requirements), `README.md` (usage), `AGENTS.md` (developer guide) with
  code-quality and Go convention rules.

### Changed (plan, before any code existed)

- Config format: `base_url`, `api_key`, remote HTTP MCP servers (`url`, `headers`), per-server `enabled`,
  `${VAR}` expansion; no `approval` key.
- Phases: Phase 3 is the tool loop only (fake tools); the MCP client and the approval prompt moved to Phase 4;
  file tools, `shell`, `todo`/`ask_user` and `web_fetch` are Phases 5 to 8.
- The OpenAI built-in `web_search` tool is not used; web search is decided later.

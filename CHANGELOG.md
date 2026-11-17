# Changelog

All notable changes to the codebase are documented in this file. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows [Semantic Versioning](https://semver.org/)

## [Unreleased]

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

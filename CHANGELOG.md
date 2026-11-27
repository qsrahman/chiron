# Changelog

All notable changes to the codebase are documented in this file. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows [Semantic Versioning](https://semver.org/)

## [Unreleased]

### Fixed

- A server that ended a reply with a passing fault (overloaded, a rate limit, a server error) ended the turn at once.
  The request is now sent again, up to twice, after a pause.
- A request refused as too long inside a reply (an error in the stream, or a failed response) is now shortened and sent
  again, like one refused with an HTTP error.
- More ways of saying "too long" are recognized (such as "too many input tokens"), and a rate limit worded in tokens is
  no longer taken for one.
- Error answers from compatible servers are read: `{"error": "..."}` showed a JSON decoding error instead of the status,
  and `detail`, a plain `message` or plain text showed only the status text.
- Spaces and line ends around the API key are removed. A key that ended in a newline failed as if the server could not
  be reached.

## [0.11.1] - 2026-11-26

### Changed

- **The `autoApprove` key of an MCP server is now `auto_approve`,** like every other key of the config file. The old name is
  refused with a message that says so (`autoApprove was renamed auto_approve`): rename it in each of your `mcpServers`. The
  value is as before: a list of tool names, `true` for every tool, or `false`.

### Fixed

- A tool result over 20 KiB that is not UTF-8 failed the call with a panic instead of being cut.
- A `todo` call whose list cannot be read told the model `invalid arguments: invalid arguments: …`; it now says it once.

### Security

- **A reply could drive the terminal through HTML entities.** Text was cleaned before the Markdown renderer ran, and the
  renderer then decoded references such as `&#27;` or `&#x202e;`: a model, steered for example by a web page, could write
  to the clipboard, move the cursor or reorder text. References to such characters are now removed before rendering.
- Text that is not UTF-8 could come out of the cleaning with a bidirectional override that was not in it before.
- `goldmark` (the Markdown parser under glamour) is upgraded to v1.7.17 for GO-2026-5320, a cross-site scripting flaw in
  its HTML renderer. chiron renders to the terminal, not to HTML.

## [0.11.0] - 2026-11-22

### Fixed

- **A panic in a tool no longer ends the program.** A tool that panics (a bug in an MCP server's SDK, later a built-in
  tool) fails its call with `error: the tool NAME panicked: …`, which the model reads like any tool error, and the turn
  goes on. A panic in the turn itself ends the turn with `internal error: …`: the turn is kept with a note if tools had
  run, and not at all if none had. Before, either one killed chiron and left the terminal in the state of the interface.

### Changed

- Small clean-ups, with no change in what the program does: every TUI path that changes the conversation outside a turn
  ends with one `conversationChanged` (it was two calls in eight places); `RegistryFor` builds its list in a copy, where
  it could have written the todo tool into spare room in the caller's slice; the three token estimates that were
  written as bytes divided by four now share `tools.EstimateText` and one constant with `EstimateTokens`; the cost of
  an MCP server's tool definitions is counted once when it starts and not by rebuilding every tool on each status call
  (completing `/reconnect` asked for it on every key).
- `Agent.run` is split into `run` (the whole turn: budget check, summary, recovery) and `round` (one model call and its
  tools), and the loop guard's bookkeeping lives in a `loopGuard` type with an `observe` method, with its own tests. No
  behaviour changes; the most complex function of the program went from a cognitive complexity of 39 to 19.

- **A model that repeats itself is told, and stopped only if it goes on.** When the same tool step (same calls, same
  results) shows up a third time among the last 10, the next requests carry a notice in their instructions ("you made
  the same tool call with the same result N times: try a different approach, or tell the user what is blocking you") and
  you see a notice; it goes as soon as the model takes a different step and does not outlive the turn. The stop at the
  sixth repeat is unchanged. Measured on the configured model with a tool that never changes its answer: 6 of 6 turns
  ended by themselves with the notice, 1 of 6 without it (5 were stopped); `TestRealAPILoopNotice` repeats it.

### Removed

- **The `max_turns` cap, the `--max-turns` flag and the "Stopped after N model calls" ending.** A turn has no limit on its
  model calls any more. What bounds a turn now: the loop guard (the same tool step a third time among the last 10 earns a
  notice to the model, the sixth stops the turn), the context budget, which keeps every request within `max_context_tokens`,
  and Ctrl-C. A model that keeps trying different things is not stopped, so watch a long unattended `-p` run. A config file
  that still has `max_turns`, or a command line with `--max-turns`, is refused: delete it. `Settings.MaxTurns` is gone.

## [0.10.0] - 2026-11-22

### Added

- **A `todo` tool and a todo panel.** With any other tool, the model can keep a checklist of a task with three or more
  steps (`pending`, `in_progress`, `completed`, `cancelled`; one `in_progress`; the whole list on every call). The tool never
  asks and ignores fields it does not know, a refused list says how to fix it and leaves the old one, and the list is
  added to the instructions of every request while a step is open, so it survives the pruning of old tool results and
  `/compact`. While a step is open a panel above the input shows `todo · 1/5 done` and the open steps; `/todo` prints the
  whole list. The list is saved with the session and comes back on `--resume`; `/reset` clears it. New: `TodoUpdated`
  event, `Agent.RegistryFor`, `Agent.Todos`, `tools.TodoList`. Designed from ergane's todo tool; see PLAN.md section 4.9.

### Changed

- **The usage figure is only at the right edge of the bottom row, under the input box**: the token count and the share of
  the budget (`~9.7k tokens · ~15%`, with a `~` while it is an estimate; it keeps the amber and red colours at 75% and
  90%). In a narrow terminal it shortens to the percentage alone, so the key hints keep some room. The header no longer
  shows the token count, and the word "context" is gone from the bottom row.

- **The system prompt tells the model the terminal cannot render LaTeX** and asks for math in plain Unicode (π √ ∑ ∫ ≤ ≠,
  x², aₙ, a/b), never `$…$`, `\(…\)`, `\[…\]` or backslash commands. It is in the shared part of the prompt, so it
  applies with and without tools, and tests pin it. Measured on the configured model with four maths prompts: without
  tools, 10 of 12 answers had LaTeX before and 0 of 12 after; with MCP tools, 6 of 8 before and 1 of 10 after.

## [0.9.0] - 2026-11-20

### Changed

- The share of the context budget moved from the header to the right edge of the bottom row, under the input box
  (`context 9%`, with a `~` while it is an estimate). It keeps the amber (75%) and red (90%) colours and gives the key
  hints, not itself, the last room in a narrow terminal. The header keeps the token count.

### Fixed

- The header shows the size of the conversation, and its share of `max_context_tokens`, as soon as chiron starts or a
  session is resumed, as an estimate marked `~` (for example `~6.0k tokens (9%)`). Before, it stayed empty until the first
  response, and `/reset`, `/undo`, `/retry`, `/model` and the resume from `/sessions` blanked it again. The first
  response replaces the estimate with the server's own count, and `/compact` and the connecting of MCP servers update it.

### Added

- **Delete a session from the `/sessions` picker.** `d` on a row asks "Delete session ID?", `y` deletes the file and any
  other key keeps it; the list closes when the last session is gone. The session in use cannot be deleted (its file
  would be written again by the next save): `/reset` first. New: `Agent.DeleteSession`, `session.Store.Delete`.

## [0.8.0] - 2026-11-20

### Removed

- **The `context_window` config key.** It only fed the header's percentage and the `window` row of `/context`. The header
  now shows the share of `max_context_tokens`, which is what the shortening and the summaries act on (it always shows,
  since the budget has a default), and `/context` has no `window` row. A config file that still has `context_window` is
  refused as an unknown key: delete the line. `Agent.Settings().MaxContextTokens` now always holds the budget in use.

### Added

- `max_output_tokens` config key: the most tokens the model may generate for one reply, reasoning included. Unset sends no
  cap, as before. The request that writes a summary keeps its own cap of 8,000.

### Changed

- **`--continue` is gone; `--resume` takes an optional ID.** `--resume` alone resumes the latest session of this
  directory (it warns about damaged files it passed over, as `--continue` did), `--resume ID` and `--resume=ID` a named
  one. A second ID, or any other stray argument, is an error.
- **`/sessions` is a picker.** It lists the saved sessions of this directory (the newest 20, with the current one
  marked); the arrow keys and Enter resume the chosen one in place of the conversation on screen, which is already
  saved, and Esc closes the list. `/sessions all` is gone. `Agent.ListSessions` has no `all` argument and
  `Agent.ResumeSession(id)` is new; a session that cannot be loaded changes nothing and the error is shown.

## [0.7.0] - 2026-11-19

### Changed

- **Renamed from desk-agent to chiron.** The binary, the module path (`github.com/qsrahman/chiron`) and the `cmd/`
  folder, the config and state directories (`~/.config/chiron`, `~/.local/state/chiron`) and the `CHIRON_LIVE_*`
  variables of the end-to-end script carry the new name. Nothing is migrated: move an existing config file and the
  `sessions`, `history` and `mcp` entries of the old directories by hand. Entries below this one keep the old name.
- Simplification, no behaviour change except the next point: the state directory function moved from `internal/mcp` to
  `config.StateDir`; the unused `approvalView` and `mcp.Status.Source` are gone; the code that counted turns for session
  files written before turns were stored is gone; `ToolTitle` replaces its private twin; the token estimate of the
  instructions and tools is computed in one place.
- The startup table of MCP servers uses the same line as `/servers` (for example `connected · 1 tool · ~120 tokens of
  definitions`), instead of a second wording.
- `/sessions` and `--continue` no longer read every session file in full: files are ordered by modification time, a
  list reads only the header of a file (the files carry their turn count now) and shows the newest 20, and `--continue`
  stops at the first match. `--continue` prints a warning for each damaged file newer than the session it chose.
- A directory is known by its real path, so `--continue` through a symlink finds the same sessions.
- A resumed conversation shows how each tool call ended (failed, denied, interrupted) and the `server → tool` titles
  once the MCP servers have connected.
- `$VISUAL`/`$EDITOR` is run as a shell command line, so quoted paths with spaces and arguments work.
- A prompt that starts with a space is not added to the prompt history.
- The text sent to the summariser is cut from its start when it is longer than about three quarters of the budget, so the
  request for a summary cannot itself be too long.
- A `-p` run is never saved as a session: it is a script's call, and one file per call would pile up. `-p` with
  `--continue` or `--resume` still reads the session, and does not add to it.

### Fixed

- Saving a session reports a failed write: the error of writing the temporary file was dropped, so a half-written file
  could have replaced the previous one.
- Two programs saving the same session no longer overwrite each other: the second to write notices that the file has
  changed since its own last write, says so, and continues in a new file.
- A `max_context_tokens` that is not more than the instructions and tool definitions alone is reported at the start of
  the turn, instead of every turn stopping after its first tool round.
- `auto_compact` no longer announces "Summarizing earlier messages…" every turn when there is nothing it could cut (a
  single large turn), and a Ctrl-C during an automatic summary is no longer counted as a failed summary.
- The prompt history no longer loses the entries after a line longer than 1 MiB (it is read with a reader, not a
  scanner), and a prompt over 64 KiB is not remembered.
- `/sessions` keeps the first prompt and the real number of turns after `/compact`: the file now carries a title and
  the count of turns a summary stands for.
- `/model` to another model drops the reasoning items of the history, as a resume with another model does, so a
  session file is never labelled with a model that did not write its reasoning.

## [0.6.2] - 2026-11-18

### Added

- Prompt history (Phase 9c): `Up` on an empty input recalls the newest prompt, `Up`/`Down` walk through the history and
  `Down` past the newest empties the input; once a recalled prompt is edited the keys belong to the input again. The
  last 1,000 prompts (commands included, no repeats in a row) are kept in a private file next to the sessions and
  survive a restart; with sessions off they are kept for the run only.
- `Ctrl-R` searches the history (case-insensitive, newest first, `Ctrl-R` again for an older match, `Enter` puts it in
  the input, `Esc` cancels).
- `Ctrl-G` edits the prompt in `$VISUAL` or `$EDITOR` through `tea.ExecProcess`; the saved text comes back into the input.
- `Ctrl-Z` suspends the program (`tea.Suspend`).
- A live scenario for the history, search, editor and restart.

### Changed

- `Up` and `Down` no longer scroll the transcript a line; `PgUp`/`PgDn` and the mouse wheel still do.

## [0.6.1] - 2026-11-18

### Added

- Sessions (Phase 9b): every conversation is saved after each turn (and after `/undo`, `/retry`, `/compact`) in
  `$XDG_STATE_HOME/desk-agent/sessions/`, a private directory of private files written atomically. `--continue` resumes
  the latest session of the directory, `--resume ID` a named one (a unique prefix is enough), `--no-save` and
  `"sessions": false` turn saving off, and `/sessions [all]` lists them. A resumed conversation shows its messages and
  the newest turn's tool cards again and goes on in the same file; reasoning items are dropped when the model or the
  server differs from the one that wrote them; a file whose results have no calls is refused. New package
  `internal/session` and a `Notice` when a save fails.
- A live scenario for sessions in `e2e/run.sh`.

### Fixed

- The test helper of `cmd/desk-agent` now points `XDG_STATE_HOME` at a temporary directory, so tests cannot write
  sessions or logs into the real one.

## [0.6.0] - 2026-11-18

### Added

- Context budget and summaries (Phase 9a): `max_context_tokens` (default 65,536) for the whole request, estimated and
  corrected with the server's `usage.input_tokens`. When a round of tools would push the next request over it, the
  results of earlier rounds are shortened (first 4,000 and last 1,000 characters, with how much is hidden), then
  dropped, and a turn that still cannot fit is kept with a note. A request the server refuses as too long is sent once
  more, shorter (summarised before the first round, shortened results after).
- `/compact [focus]` summarises the oldest turns with the chat model and keeps the newest as they are; `/summary` shows
  the summary; `/context` shows the summary and the budget. The summary is kept apart from the history and goes first in
  every request as notes, not instructions; a bad one is refused and the history stays whole. `"auto_compact": true`
  does it before a turn at 75% of the budget (off by default; paused after 3 failures in a row). A new `Notice` event
  reports what the agent did on its own.
- A live scenario for `/compact` in `e2e/run.sh`, and `TestRealAPICompact` against the configured model.

- The terminal gets the program's state through Bubble Tea's `View`: the window title (`● desk-agent · dir` while it works,
  `▲` while it waits for an approval), the terminal's progress indicator (running, or yellow while it waits), focus
  reporting with a bell and a `✓`/`✗` in the title when a run finishes, or an approval waits, in a window that is not in
  front.
- The input uses the terminal's own cursor (a bar, in the user's cursor colour) instead of drawing one.
- Mouse clicks: a tool card opens and closes, the buttons of the approval dialog answer it (hit-tested with the
  compositor's layer IDs, and with the same guard time as keys), and a click on the help bar jumps to the bottom while
  `↓ N lines below` shows.
- `/copy [code]` puts the last answer, or its last code block, on the clipboard through the terminal (OSC 52), with
  control characters removed.
- The usage figure in the header turns amber at 75% of the context window and red at 90%.

### Changed

- The help names `ctrl+j` as the new-line key until the terminal says it can tell Shift+Enter from Enter
  (`KeyboardEnhancementsMsg`), then `shift+enter`: in a terminal that cannot, Shift+Enter sends the message.

### Added

- `e2e/run.sh`: a live end-to-end check of the real binary in a pseudo-terminal, with a real model and a real stdio MCP
  server, read through a terminal emulator. Thirteen scenarios: startup, markdown and code bands, approvals and cards,
  the queue and cancel, every command, restart after a server dies, scrolling, resizing, quitting, multi-line input,
  long output, a failing server, and `-p` with `--yes` and `--no-mcp`.

- Five more live scenarios that use the real `toolbox` MCP server of the config file: its 7 tools and which ask first,
  `write_file`, `read_file`, `edit_file` and `read_multiple_files` with the files checked on disk, `shell` (the
  approval dialog shows the command decoded, a denied call is not retried), `fetch_url`, `brave_web_search`, and a
  result over the output cap.

### Fixed

- A command that says something while the transcript is not refreshed: an unknown command (`/nosuch`) added its message
  but never showed it.
- The transcript lost its place at the bottom when the window changed height (the suggestion list opens while a
  command is typed), so the output of the command landed below the fold. A window at the bottom now stays there.
- In a narrow terminal the header kept the model, token count and servers and lost the name of the program. It now gives
  up the servers, then the share of the window, then the token count, in that order, and never cuts the name.

### Changed

- A margin of one column on the right as well as the left: nothing touches the last column of the terminal.
- When the transcript is scrolled up, the help bar says how much is below (`↓ 42 lines below`), ahead of the key hints.
- Fenced code blocks are drawn on a shaded band (as wide as the text, with padding), not as bare coloured text; a block
  that is still streaming already shows as one. Lines too long for the band are cut with an ellipsis.
- Inline code has its own colour (teal) instead of amber, which stays the colour of warnings and of work in progress.
- Lines no longer break after a hyphen inside a word: `--config`, `read-only` and `--reasoning-effort` stay whole.
- The header is two rows (the blank row under the rule is gone).

### Changed

- The empty input reads "Ask anything, or /help for commands..." (a shorter form in a narrow terminal, so that it is
  never cut off), the one place that says commands exist.
- The welcome banner is gone: the transcript is empty at start and after `/clear`. (`tui.Info.Version` went with it.)

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

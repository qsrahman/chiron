# chiron

A terminal AI coding agent in Go. It uses the OpenAI Responses API and a Bubble Tea TUI. It works on your machine
through MCP servers you configure, and keeps its own todo list; built-in file, shell and web tools are planned.

> **Status: v0.11.1.** You can chat with the model in a terminal UI or send a single prompt from the command line,
> give it tools by configuring MCP servers (every call asks for your approval unless you allow it), resume and manage
> saved sessions, and watch the model's todo list. The built-in file, shell and web tools and `ask_user` are the next
> phases (see [PLAN.md](PLAN.md)): until then, without MCP servers, the model cannot touch your files. This README is
> updated as each phase lands.

## Requirements

- Go 1.27 or newer
- An OpenAI API key in `OPENAI_API_KEY` (or `api_key` in the config file). Not needed when `base_url` points
  at a local server that does not require one.

## Build and run

```bash
go build -o chiron ./cmd/chiron
export OPENAI_API_KEY=...
./chiron                                              # interactive chat
./chiron -p "Explain what a goroutine is in two sentences"   # one prompt, answer on stdout
```

## Slash commands

Type `/` in the chat to list them; Up/Down choose, Tab completes, Esc closes. They are refused while a turn runs.

| Command | What it does |
| --- | --- |
| `/help` | List the commands and keys |
| `/exit`, `/quit` | Quit |
| `/clear` | Clear the screen; the conversation is kept |
| `/reconnect [NAME]` | Start MCP servers again: the named one, or those that failed or lost their connection |
| `/reset` | Forget the conversation and start over |
| `/model [NAME]` | Show the model and list the server's models, or switch to NAME (Tab completes the names) |
| `/effort [LEVEL]` | Show the reasoning effort, or set it (`default` sends none) |
| `/servers` | List the MCP servers: state, tools offered (and hidden), what the definitions cost, log path |
| `/todo` | Show the model's todo list, finished steps included |
| `/tools` | List the tools and which of them ask before they run |
| `/undo` | Remove the last exchange from the conversation |
| `/retry` | Send the last prompt again |
| `/copy [code]` | Copy the last answer, or its last code block, to the clipboard (through the terminal, which must support OSC 52; control characters are removed first) |
| `/context` | Estimated size of the instructions, tools, summary and history, and how much of the budget is used |
| `/compact [focus]` | Summarize the oldest turns with the model to free context; `focus` says what the summary must keep |
| `/sessions` | Choose one of the saved conversations of this directory and resume it in place of this one, or delete one (`d`, then `y`) |
| `/summary` | Show the summary that stands for the oldest turns |

A lone `/word` that is no command is reported and not sent; a longer line such as `/usr/bin is missing` is a prompt.

## The todo list

When it has any tool (a built-in one, or from an MCP server), the model also gets a `todo` tool for keeping a checklist
of a task with three or more steps. Every call sends the whole list, which replaces the old one; an empty list clears
it. Statuses are `pending`, `in_progress`, `completed` and `cancelled`, and only one step may be `in_progress`: a list
with two, an empty step, an unknown status, more than 30 steps or a step over 200 characters is refused with the reason,
and the old list stays. The tool never asks for approval, since it changes nothing outside chiron.

- **Panel:** while a step is open, a panel above the input shows `todo · 1/5 done` and the open steps (`▸` in progress,
  `○` pending), at most five, with the rest counted. It disappears when every step is done or dropped; `/todo` prints
  the whole list.
- **The model keeps it in view:** while a step is open the list is added to the instructions of every request, because
  the history keeps the tool calls of the newest turn only and `/compact` drops more.
- **Sessions:** the list is saved with the session and comes back with `--resume` and the `/sessions` picker. `/reset`
  clears it; `/undo` and `/retry` leave it as it is, as they leave changed files.

## Queue and tool cards

While the model works you can keep typing: Enter adds the prompt to a list above the input (the next three are
shown, the rest counted), and each is sent when the turn before it ends. **Ctrl-C** cancels the turn and drops the
queue. After a turn that fails on its own the queue is held and a notice says how many prompts are waiting: Enter on
an empty line sends the first. **Up** on an empty input takes the last queued prompt back to edit. Commands are never
queued; they wait for the turn to end.

**Tab** (or Shift+Tab) on an empty input selects a tool card, newest first; **Enter** or **Space** opens it to show its
whole output, or closes it again, and the screen scrolls to keep it in view. **Esc**, or typing, goes back to the
input.

## Approvals

A tool that can change something (anything not marked read-only) asks before every call: a dialog in the middle
of the screen shows what is about to happen, with **Allow** (`y`), **Allow for session** (`s`) and **Deny**
(`n` or Esc). Deny is selected when it opens, so pressing Enter by reflex refuses. The arguments are laid out as `name: value` lines in the order the model gave them (a value of several lines sits under
its name, a short structure stays on one line) and are decoded, so what you read is what the tool receives: `&&` is
shown as `&&`, not as `\u0026\u0026`. The details are never cut
(long ones scroll with Up/Down and PgUp/PgDn) and control characters appear as visible escapes such as `\x1b`
and `\r`, so what you allow is exactly what runs. A session grant covers the same tool with the same key (for
example the same command) until you quit; `/tools` lists what is allowed. A denied call is reported to the model,
which is told not to try again. Ctrl-C with the dialog open cancels the turn. Keys pressed in the first half second
after the dialog opens are ignored (Ctrl-C excepted), so a prompt that appears while you type cannot be answered by
a stray letter.

With `-p` nobody can answer, so calls that would ask are denied (a notice goes to stderr) unless you pass `--yes`,
which allows every call for that run. No built-in tool can change anything yet; they arrive in Phase 5.

## MCP servers

Servers listed under `mcpServers` in the config file give the model their tools. All enabled servers start in
parallel when the program starts, with one 10-second deadline. The chat opens at once and the header says
`connecting…`; a prompt you send meanwhile waits and goes out when the servers have answered, so the model sees their
tools. Each server is then listed with how it started, and the header shows `mcp 2/3`. A server that fails, or is too
slow, is reported and stays out for the session; it does not stop the others. With `-p` the run waits for the servers
and prints failures to stderr. `--no-mcp` starts none.

- **Tools** are named `server__tool` (characters the API does not allow become `_`, at most 64 characters, made
  unique when two would clash) and appear on cards as `server → tool`. A request carries at most 128 tools; a
  built-in tool keeps its name if one clashes.
- **Every MCP call asks first**, because a server's own claim that a tool only reads cannot be trusted. The dialog
  shows `server → tool`, the arguments, and a line saying where the server runs (its command, or the host of its
  URL). Allow for session covers that tool with any arguments. To let a tool run without asking, list it under
  `auto_approve`: a list of tool names as the server gives them (`"auto_approve": ["search"]`), or `true` for every tool
  of that server, those it adds later included (`false` is the default). `/tools` lists those as running without
  asking. MCP calls run one at a time. A call times out after 2 minutes, and Ctrl-C cancels it.
  For example, with the three servers of the [configuration](#configuration) below:

  ```json
  "context7": { "url": "https://mcp.context7.com/mcp", "auto_approve": ["resolve-library-id", "query-docs"] },
  "grep":     { "url": "https://mcp.grep.app", "auto_approve": true },
  "toolbox":  { "command": "npx", "args": ["-y", "@qsrahman/workshop-mcp@latest"] }
  ```

  Calls to `context7`'s `resolve-library-id` and `query-docs` never ask, and neither does any call to `grep`. Every
  other `context7` tool, and every `toolbox` tool, asks first (`"auto_approve": false` says the same as leaving it
  out). A list is the safer form: `true` also trusts tools the server adds later. `/tools` shows what runs without
  asking.
- **Local servers** run with this program's environment plus their own `env`. They can therefore see variables such
  as `OPENAI_API_KEY`: only start servers you trust. Their error output goes to
  `~/.local/state/chiron/mcp/<server>.log` (`$XDG_STATE_HOME` is honoured), a private file that is emptied at each
  launch; it never reaches the screen.
- **Remote servers** need an `https` URL (`http` only for localhost) without a user name or password in it. The
  configured headers go to that host only, and a redirect to another address is refused.
- **`${NAME}`** in `command`, `args`, `env`, `url` and `headers` is replaced from the environment. A variable that is
  not set fails that server (the message names the variable, never a value). Server names may use letters, digits,
  `_` and `-`.
- **A server that dies** is started again by the next call, at most 3 times per session; a call that was already
  sent when the connection broke fails and is not repeated, since it may have done part of its work. The tools
  offered do not change. `/servers` shows `connection lost` and how often a server was started again.
- **`/reconnect [NAME]`** starts servers afresh (and resets their restart count): the named one, or with no name every
  server that failed or lost its connection. This is how a server that was too slow at launch (for example `npx`
  downloading a package on its first run) joins; the tools of a server that comes up are offered from the next
  prompt on. It runs in the background like the first start, and prompts wait for it.
- **`tools`** lists the only tools of a server that are offered (`"tools": ["search"]`); a name the server does not
  have fails that server and shows what it does offer, so a typo cannot silently offer nothing. `/servers` counts
  the hidden tools. An empty list offers none.
- **`instructions: true`** adds the server's own usage notes to the system prompt, under a heading that says they were
  written by the server and are information, not orders (cut to 4,000 characters). Without it they are not used.
- **Results:** text parts are joined, structured content is used when there is no text, images and audio become a
  short placeholder such as `[image: image/png, 12 KB]`, and an error result is a failed tool call. Like every tool
  result it is cut at 20 KiB.

## What the model is told

Every session starts with a built-in system prompt (guidance on how to work and communicate), the project's
`AGENTS.md` (or, failing that, a `CLAUDE.md`) from the working directory if it has one, cut at 32 KB, and last an
environment block: working directory, platform, today's date and the git branch and state. The prompt talks about
tools only when the model has some: with none (the case without MCP servers) it says plainly that it has none,
because tool language in such a prompt makes some models type tool calls as text or answer nothing.

## When things go wrong

- **A stream that breaks** (it ends with no final response, or the connection drops) is sent again, up to twice,
  with a notice; the text of the failed attempt is dropped from the screen. In `-p` mode that text is already on
  stdout, so after a retry the answer can start over (the notice goes to stderr).
- **A server that reports a passing fault** inside a reply (overloaded, a rate limit, a server error) is asked again,
  up to twice, after 2 and then 4 seconds, in the same way. Other faults end the turn with the server's reason.
- **Error answers from compatible servers** are read in their various shapes (`error` as text, `detail`, `errors`, a
  plain `message` or plain text), so the reason is shown and a request refused as too long is recognized.
- **An empty reply** is asked for again once. A refusal, a failed or incomplete response, or an API error ends the
  turn with a message; the usual causes come with a hint (wrong key, wrong model or base URL, quota, a conversation
  that is too long). A reply that was cut off after some text keeps that text in the conversation.
- **A turn that stops after tools ran** (you pressed Ctrl-C, the server failed, a limit was reached) is kept with a
  closing note such as `[Stopped after 2 model calls: interrupted by the user]`, and every call that did not finish is
  answered as interrupted. The model therefore knows what was done, which matters once tools can change things. A
  failure before any tool ran leaves the conversation as it was.
- **Limits:** there is no cap on the number of model calls for one prompt (Ctrl-C cancels a turn). A model that
  repeats the same tool call with the same result 3 times in 10 steps is told so in its instructions (you see a notice)
  and can change course, and one that goes on until 6 is stopped; a model that keeps trying different things is not, so
  watch a long unattended `-p` run; a stream that goes silent for 2 minutes is abandoned; at most 8
  read-only tool calls run at once; a tool result is cut at 20 KiB with a note.
- **Context budget:** `max_context_tokens` (default 65,536) is the size a whole request may have (instructions, tool
  definitions and history, not the reply). It is an estimate (about 4 bytes a token), corrected with the count the
  server sends back. Keep it well below the model's real window. When a round of tool calls would push the next
  request over it, the results of earlier rounds are shortened first (a result over 8,000 characters keeps its first
  4,000 and last 1,000 characters and says how much is hidden), then dropped; only if even the newest results do not
  fit, the turn stops with a note. If the server refuses a request as too long, at once or inside the reply, it is sent once more, shorter.
- **Summaries:** `/compact` asks the model to write notes about the oldest turns and replaces them with those notes,
  keeping the newest turns as they are. The notes go first in every request, marked as notes and not instructions;
  there is only ever one summary (the next one builds on it). A summary that is empty, has no headings, is longer than
  what it replaces or was cut off is refused and nothing changes. `/undo` and `/retry` work as before. With
  `"auto_compact": true` this also happens before a turn when the request would pass 75% of the budget; it is off by
  default because a summary is an extra model call that rewrites the history. After 3 failed automatic summaries in a
  row they pause until `/compact` works. `/reset` forgets the summary too.
- **Older turns** keep only their messages. The reasoning, tool calls and tool results of every turn but the newest
  are dropped from what is sent, which keeps requests small; the model can call a tool again if it needs a result.
- **Text from the model, tools or servers** is cleaned before it is shown: control characters, escape sequences
  (clipboard writes, cursor movement), carriage returns and Unicode text-direction overrides never reach the terminal.

## Using the chat

Type a message and press `Enter`. The answer streams in and is rendered as markdown when it finishes; while the
agent works you see a spinner and the elapsed time. The input box grows with your text (up to 8 lines), and the
conversation keeps its history for the whole session. Terminals narrower than 40 columns are not supported.

| Key | Action |
|---|---|
| `Enter` | Send the message (ignored while an answer is streaming; your draft stays) |
| `Ctrl-J` or `Shift-Enter` | New line in the message. `Shift-Enter` is a key of its own only in a terminal that supports it (kitty, Ghostty, WezTerm, iTerm2 and others); in any other it is plain Enter. The help bar shows `shift+enter` once the terminal has said it can, and `ctrl+j` until then (`Alt-Enter` works too) |
| `PgUp` / `PgDn` | Scroll the transcript a page (when you have scrolled up, the help bar says `↓ N lines below`) |
| `Up` / `Down` | Recall earlier prompts: `Up` on an empty input shows the newest, `Up` and `Down` then walk through the history, and `Down` past the newest empties the input again. Once you edit a recalled prompt the keys belong to the input. With a prompt queued, `Up` takes that back first. |
| `Ctrl-R` | Search the prompt history: type to filter (newest match first), `Ctrl-R` again for an older one, `Enter` puts the match in the input (it is not sent), `Esc` cancels |
| `Ctrl-G` | Edit the prompt in your editor (`$VISUAL`, else `$EDITOR`, run as a shell command line like git does, so `code --wait` and a quoted path with spaces work). What the editor saves comes back into the input and is not sent. |
| `Ctrl-Z` | Suspend the program; `fg` brings it back |
| Mouse wheel | Scroll the transcript (or the details of an approval dialog that is open). The terminal's own text selection then needs `Shift` held (`Option` on macOS); `/copy` needs no selection |
| Left click | On a tool card: open or close it. On a button of an approval dialog: answer it. On the help bar while `↓ N lines below` shows: jump to the bottom |
| `Ctrl-C` | While an answer is streaming: cancel it (press again to quit). When idle: clears what you typed; on an empty input it asks you to press it again, since the conversation is not saved, and quits at once if there is no conversation yet. |
| `Ctrl-D` | On an empty input: quit, with the same second-press protection as `Ctrl-C` (the two count together) |

The terminal gets what it can use of the program's state: the window or tab title names the directory and starts with `●`
while the program works and `▲` while it waits for an approval; a terminal with a progress indicator (Windows Terminal,
iTerm2, Ghostty and others) shows it running, or yellow while it waits for you; and if the terminal reports focus, a bell
and a `✓` or `✗` in the title tell you when a run that finished or an approval that waits happened in a window you were
not looking at. The text cursor is the terminal's own (a bar, in your cursor colour). The share of the context budget
(`max_context_tokens`) is at the right edge of the bottom row, under the input box (`~9.7k tokens · ~15%`), and
turns amber at 75% and red at 90%; in a narrow terminal it shrinks to the percentage alone (`15%`). The header has no
usage figure.

The top row shows the working directory and git branch on the left and the model, and after the first answer the
size of the conversation in tokens, on the right. Below the transcript are the input box and a one-line key
help. The colours adapt to your terminal: the UI asks the terminal for its background and picks a dark or light
palette, deriving the shaded areas from it, so it fits any terminal theme.

When the model calls a tool, a card appears in the transcript: a spinner while it runs, then `✓` or `×`, the
tool name, its main argument and the others as `key=value`. Cards are **closed by default**: one line each, with
what the call came to at the right end (`12 lines`, how long it took if it was slow, `cancelled` or `denied by you`).
A run of calls reads as a list, with no blank lines between the cards. Open a card (see above) to see the whole
output on a shaded band. A failure shows an `ERROR` badge and the first line of the error even while closed. For an
MCP tool the server is muted: `docs → search`. Independent read-only calls run at the same time; anything that could
change something runs alone, in the order the model asked.

A cancelled or failed answer stays on screen but is not added to the conversation history, so the next
message continues from the last complete answer.

## Command-line options

```
chiron [options]
```

| Option | Default | Meaning |
|---|---|---|
| `-p "prompt"` | (chat UI) | Send one prompt, print the answer to stdout and exit. `Ctrl-C` cancels. Never saved. |
| `--config path` | `~/.config/chiron/config.json` | Config file. A missing explicit file is an error; a missing default one is fine. |
| `--model name` | file, else `gpt-5.4-mini` | Model to use. |
| `--base-url url` | file, else `https://api.openai.com/v1/` | API base URL for OpenAI-compatible servers. |
| `--reasoning-effort level` | file, else none sent | `none`, `minimal`, `low`, `medium`, `high`, `xhigh` or `max`; `default` sends none even if the file sets one. Models that do not reason reject the setting, so it is only sent when you set it. |
| `--streaming`, `--no-streaming` | streaming | Stream the reply as it arrives, or send one request and show the whole reply at once (for servers whose streaming is broken). |
| `--resume [id]` | | Resume the latest saved conversation of this directory, or the one named `id` (or the only one whose name starts with it). `--resume id` and `--resume=id` both work. With `-p` the session is continued but not added to. |
| `--no-save` | | Do not save this conversation. |
| `--no-mcp` | | Start none of the MCP servers of the config file. |
| `--yes` | | Allow every tool call without asking. With `-p`, calls that would ask are denied unless you pass this. |
| `--version` | | Print the version and exit. |

Single or double dash both work (`-model` is `--model`). A stray argument is an error (exit status 2). There is no
`--api-key` flag, on purpose: keys given on the command line leak through shell history and process lists.

Examples:

```bash
chiron                                          # chat in the current directory
chiron -p "Summarise the README in one line"    # one answer on stdout
chiron --model gpt-5.4 --reasoning-effort high  # a stronger model for this run
chiron --base-url http://localhost:11434/v1 --model qwen3 --no-streaming   # a local server
chiron --resume                                 # latest conversation of this directory
chiron --resume 2026-11-24                      # the one whose name starts with this
chiron -p "Continue" --resume --yes             # continue it, non-interactively, allowing every tool call
chiron --config ./work.json --no-mcp --no-save  # another config, no MCP servers, nothing saved
chiron --reasoning-effort default               # ignore the file's reasoning_effort
```

## Configuration

One file, `~/.config/chiron/config.json` (or `$XDG_CONFIG_HOME/chiron/config.json`; `--config` names another).
Every key is optional. A complete example:

```json
{
  "model": "gpt-5.4-mini",
  "base_url": "http://localhost:11434/v1",
  "api_key": "${OPENAI_API_KEY}",
  "reasoning_effort": "low",
  "streaming": true,
  "max_output_tokens": 8192,
  "max_context_tokens": 65536,
  "auto_compact": false,
  "sessions": true,
  "mcpServers": {
    "context7": {
      "url": "https://mcp.context7.com/mcp",
      "headers": { "CONTEXT7_API_KEY": "${CONTEXT7_API_KEY}" },
      "auto_approve": ["resolve-library-id", "query-docs"],
      "enabled": true
    },
    "grep": { "url": "https://mcp.grep.app", "auto_approve": true },
    "toolbox": {
      "command": "npx",
      "args": ["-y", "@qsrahman/workshop-mcp@latest"],
      "env": { "BRAVE_API_KEY": "${BRAVE_API_KEY}" },
      "tools": ["web_search"],
      "instructions": true
    }
  }
}
```

### Top-level keys

| Key | Type | Default | Meaning |
|---|---|---|---|
| `model` | string | `gpt-5.4-mini` | Model name. Flag: `--model`. |
| `base_url` | string | `https://api.openai.com/v1/` | API base URL; the server must implement the OpenAI Responses API. Flag: `--base-url`. |
| `api_key` | string | none | API key. `OPENAI_API_KEY` in the environment beats it. Spaces and line ends around either are removed. Use `"${OPENAI_API_KEY}"` rather than a literal; a literal key in a file others can read draws a warning. Not needed for a local server that wants none. |
| `reasoning_effort` | string | none sent | `none`, `minimal`, `low`, `medium`, `high`, `xhigh` or `max`. Flag: `--reasoning-effort`. |
| `streaming` | bool | `true` | Stream replies. Flags: `--streaming`, `--no-streaming`. |
| `max_output_tokens` | integer | `0` (no cap) | Most the model may generate for one reply, reasoning included; the server decides when unset. A reply that hits it is cut off and shown with the reason `max_output_tokens`. The summary request has its own cap (8,000). Negative is an error. |
| `max_context_tokens` | integer | `65536` | The conversation budget: when it is exceeded, old tool output is shortened (see "Context budget"). Negative is an error. |
| `auto_compact` | bool | `false` | Summarise old turns by itself when the budget is still exceeded. `/compact` does it on demand. |
| `sessions` | bool | `true` | Save chat conversations (see [Sessions](#sessions)). `false` saves nothing, like `--no-save`. |
| `mcpServers` | object | none | MCP servers by name; see below. |

Examples of the smaller settings:

```json
{ "model": "gpt-5.4", "reasoning_effort": "high" }
```

```json
{ "base_url": "http://localhost:11434/v1", "model": "qwen3", "streaming": false, "max_context_tokens": 32768 }
```

```json
{ "max_output_tokens": 4096, "auto_compact": true, "sessions": false }
```

### `mcpServers` entries

The key is the server's name (letters, digits, `_` and `-`). A server is either a **local process** (`command`) or a
**remote one** (`url`): giving both, or neither, is an error. How they behave is described in
[MCP servers](#mcp-servers).

| Key | Type | Default | Meaning |
|---|---|---|---|
| `command` | string | | Program to run (local server). |
| `args` | string list | none | Its arguments. Only with `command`. |
| `env` | object | none | Extra environment variables for it (added to this program's own). Only with `command`. |
| `url` | string | | Streamable HTTP endpoint (remote server): `https`, or `http` for localhost only; no user name or password. |
| `headers` | object | none | Headers sent to that host only. Only with `url`. |
| `enabled` | bool | `true` | `false` keeps the entry but does not start it. |
| `auto_approve` | `true`, `false` or list | `false` | Tools that run without asking: a list of the server's tool names, or `true` for all of them, those added later included. |
| `tools` | string list | all | Only these tools are offered. A name the server does not have fails that server; an empty list offers none. |
| `instructions` | bool | `false` | Add the server's own usage notes to the system prompt. |

Examples:

```json
"fs":     { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."] },
"search": { "url": "https://mcp.example.com/mcp", "headers": { "Authorization": "Bearer ${SEARCH_TOKEN}" } },
"docs":   { "url": "http://localhost:8080/mcp", "auto_approve": ["search"], "tools": ["search", "fetch"] },
"later":  { "command": "my-server", "enabled": false }
```

### Rules

- **Precedence:** command-line flags, then the file, then the defaults. The environment has **one** variable,
  `OPENAI_API_KEY`, which beats `api_key` in the file. There is deliberately no variable for the base URL or the model:
  one for the base URL would let a project's `.envrc` send your key and every prompt somewhere else, and the common
  names are exported by other programs. The base URL is always passed to the client explicitly, so the OpenAI library's
  own reading of `OPENAI_BASE_URL` cannot redirect it either. (The library still reads `OPENAI_ORG_ID`,
  `OPENAI_PROJECT_ID` and `OPENAI_CUSTOM_HEADERS`; those cannot change where the key goes.) Only this user-level file
  is read, never one from the working directory.
- **A key that is not a setting is an error** that names the key and the file, so a typo such as `max_turn` is not
  silently ignored. The keys `context_window`, `max_turns` and `autoApprove` (now `auto_approve`) no longer exist and
  are refused the same way.
- **`"${NAME}"`** is replaced from the environment in `model`, `base_url` and `api_key`, and in an MCP server's
  `command`, `args`, `env`, `url` and `headers` when the server starts (a variable that is not set fails that server;
  the message names the variable, never a value). If you put a key in the file, keep it private:
  `chmod 600 ~/.config/chiron/config.json`.
- The status line shows how much of the context budget the conversation uses (for example `25.5k tokens · 39%` at the
  right edge under the input box, of the default 65,536). Before the first response it is an estimate, marked `~`.
  There is no key for the model's real context window.
- Web search is not built in. If you want it, add a search-capable MCP server under `mcpServers`.

## Notes

`-p` starts a fresh conversation each time unless you pass `--resume` (it then continues that session
without adding to it). A `-p` run is never saved (see [Sessions](#sessions)).

## Sessions

Every chat conversation (not a `-p` run) is saved after each turn, and after `/undo`, `/retry` and `/compact`, in
`$XDG_STATE_HOME/chiron/sessions/` (`~/.local/state/chiron/sessions/`), one JSON file each, named by its start
time (`20261118-153012-k3f9`). The directory is private (`0700`), so are the files (`0600`), and a file is replaced
atomically: a failed write leaves the previous one. A conversation that never got an answer writes nothing, and
`/reset` starts a new file and keeps the old one. The file holds the model's todo list too.

- **Resume** with `--resume` (the latest of the current directory) or `--resume ID`. The messages and the tool calls
  of the newest turn are shown again, the model sees the whole saved history, and the conversation goes on in the
  same file. Inside the chat, `/sessions` opens a list of the sessions of this directory (the newest 20): choose one
  with the arrow keys and Enter, and it replaces the conversation on screen, which is already saved. `d` deletes the
  selected session after a `y` to confirm; the session you are in cannot be deleted (`/reset` first). A directory is
  known by its real path, so a symlink to it finds the same sessions. A bare `--resume` takes the session written last
  and warns about damaged files it had to pass over, since one of them may have been the one you meant.
- **Two programs, one session:** if another chiron has written the file since this one did, this one does not
  overwrite it: it says so and goes on in a new file.
- **Reasoning items** are dropped when you resume with another model or server than the one that wrote them, and when
  you switch with `/model`; they only make sense to the model that produced them. Everything else is kept.
- **Opt out** with `"sessions": false` in the config file or `--no-save` for one run. Resuming still works with
  saving off. The files hold your prompts, the answers and tool results, and the working directory; they are not
  pruned, so delete old ones by hand.
- **Prompt history** (`Up`, `Ctrl-R`) is kept next to them, in `$XDG_STATE_HOME/chiron/history` (private, the last 1,000
  prompts, one JSON string a line, no repeats in a row, and a prompt that starts with a space is not remembered, as in a
  shell, for what should leave no trace), and follows the same switch: with sessions off, prompts are
  remembered for the current run only.

## Safety

Today every MCP tool call asks for approval (see [Approvals](#approvals)) unless the server's tool is in its
`auto_approve` list, and a server you start can do whatever its program can do, with the environment of chiron: review
what you configure. The todo tool never asks, since it changes nothing outside chiron.

Planned with the built-in tools: every file write, file edit and shell command asks for approval, with an option to
always allow a tool for the current session. File tools will only reach paths inside the working directory. Shell
commands will not be sandboxed, so review them before approving.

## Development

See [AGENTS.md](AGENTS.md).

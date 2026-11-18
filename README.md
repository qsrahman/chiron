# desk-agent

A terminal AI coding agent in Go. It uses the OpenAI Responses API and a Bubble Tea TUI, and works on your
machine through tools (files, shell, web, todo, questions) and optional MCP servers.

> **Status: Phase 4b.2 (v0.5.1).** You can chat with the model in a terminal UI or send a single prompt from the
> command line, and give it tools by configuring MCP servers. No built-in tools ship yet, so without MCP servers the
> model still cannot touch your files. Built-in tools arrive in the next phases; see [PLAN.md](PLAN.md). This README
> is updated as each phase lands.

## Requirements

- Go 1.27 or newer
- An OpenAI API key in `OPENAI_API_KEY` (or `api_key` in the config file). Not needed when `base_url` points
  at a local server that does not require one.

## Build and run

```bash
go build -o desk-agent ./cmd/desk-agent
export OPENAI_API_KEY=...
./desk-agent                                              # interactive chat
./desk-agent -p "Explain what a goroutine is in two sentences"   # one prompt, answer on stdout
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
| `/model [NAME]` | Show the model, or switch to NAME |
| `/effort [LEVEL]` | Show the reasoning effort, or set it (`default` sends none) |
| `/servers` | List the MCP servers: state, tools offered (and hidden), what the definitions cost, log path |
| `/tools` | List the tools and which of them ask before they run |
| `/undo` | Remove the last exchange from the conversation |
| `/retry` | Send the last prompt again |
| `/context` | Estimated size of the instructions, tools and history |

A lone `/word` that is no command is reported and not sent; a longer line such as `/usr/bin is missing` is a prompt.

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
(`n` or Esc). Deny is selected when it opens, so pressing Enter by reflex refuses. The details are never cut
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
  `autoApprove`: a list of tool names as the server gives them (`"autoApprove": ["search"]`), or `true` for every tool
  of that server, those it adds later included (`false` is the default). `/tools` lists those as running without
  asking. MCP calls run one at a time. A call times out after 2 minutes, and Ctrl-C cancels it.
  For example, with the three servers of the [configuration](#configuration) below:

  ```json
  "context7": { "url": "https://mcp.context7.com/mcp", "autoApprove": ["resolve-library-id", "query-docs"] },
  "grep":     { "url": "https://mcp.grep.app", "autoApprove": true },
  "toolbox":  { "command": "npx", "args": ["-y", "@qsrahman/workshop-mcp@latest"] }
  ```

  Calls to `context7`'s `resolve-library-id` and `query-docs` never ask, and neither does any call to `grep`. Every
  other `context7` tool, and every `toolbox` tool, asks first (`"autoApprove": false` says the same as leaving it
  out). A list is the safer form: `true` also trusts tools the server adds later. `/tools` shows what runs without
  asking.
- **Local servers** run with this program's environment plus their own `env`. They can therefore see variables such
  as `OPENAI_API_KEY`: only start servers you trust. Their error output goes to
  `~/.local/state/desk-agent/mcp/<server>.log` (`$XDG_STATE_HOME` is honoured), a private file that is emptied at each
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
tools only when the model has some: with none (the case until tools arrive) it says plainly that it has none,
because tool language in such a prompt makes some models type tool calls as text or answer nothing.

## When things go wrong

- **A stream that breaks** (it ends with no final response, or the connection drops) is sent again, up to twice,
  with a notice; the text of the failed attempt is dropped from the screen. In `-p` mode that text is already on
  stdout, so after a retry the answer can start over (the notice goes to stderr).
- **An empty reply** is asked for again once. A refusal, a failed or incomplete response, or an API error ends the
  turn with a message; the usual causes come with a hint (wrong key, wrong model or base URL, quota, a conversation
  that is too long). A reply that was cut off after some text keeps that text in the conversation.
- **A turn that stops after tools ran** (you pressed Ctrl-C, the server failed, a limit was reached) is kept with a
  closing note such as `[Stopped after 2 model calls: interrupted by the user]`, and every call that did not finish is
  answered as interrupted. The model therefore knows what was done, which matters once tools can change things. A
  failure before any tool ran leaves the conversation as it was.
- **Limits:** at most `max_turns` model calls (25) for one prompt; a turn that repeats the same tool call with the
  same result more than 5 times in 10 steps is stopped; a stream that goes silent for 2 minutes is abandoned; at most 8
  read-only tool calls run at once; a tool result is cut at 20 KiB with a note.
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
| `Shift-Enter` | New line in the message (`Alt-Enter` and `Ctrl-J` also work, for terminals that cannot tell Shift-Enter from Enter) |
| `PgUp` / `PgDn` | Scroll the transcript a page |
| `Up` / `Down` | Scroll the transcript a line, while the input is empty (with a prompt queued, `Up` takes it back instead) |
| Mouse wheel | Scroll the transcript (or the details of an approval dialog that is open). The terminal's own text selection then needs `Shift` held (`Option` on macOS) |
| `Ctrl-C` | While an answer is streaming: cancel it (press again to quit). When idle: clears what you typed; on an empty input it asks you to press it again, since the conversation is not saved, and quits at once if there is no conversation yet. |
| `Ctrl-D` | On an empty input: quit, with the same second-press protection as `Ctrl-C` (the two count together) |

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

Flags:

| Flag | Meaning |
|---|---|
| `-p "prompt"` | Send one prompt, stream the answer to stdout and exit. `Ctrl-C` cancels. Without `-p`, the chat UI starts. |
| `--model name` | Model to use. |
| `--base-url url` | API base URL for OpenAI-compatible servers. |
| `--reasoning-effort level` | `none`, `minimal`, `low`, `medium`, `high`, `xhigh` or `max`; `default` sends none even if the file sets one. Models that do not reason reject the setting, so it is only sent when you set it. |
| `--max-turns n` | Most model calls for one prompt (default 25). |
| `--streaming`, `--no-streaming` | Stream the reply as it arrives (the default), or send one request and show the whole reply at once, for servers whose streaming is broken. |
| `--config path` | Config file (default below). A missing explicit file is an error. |
| `--version` | Print the version. |

There is no `--api-key` flag, on purpose: keys given on the command line leak through shell history and
process lists.

## Configuration

One file, `~/.config/desk-agent/config.json` (or `$XDG_CONFIG_HOME/desk-agent/config.json`):

```json
{
  "model": "gpt-5.4-mini",
  "base_url": "http://localhost:11434/v1",
  "api_key": "${OPENAI_API_KEY}",
  "context_window": 400000,
  "reasoning_effort": "low",
  "max_turns": 25,
  "streaming": true,
  "mcpServers": {
    "context7": {
      "url": "https://mcp.context7.com/mcp",
      "headers": { "CONTEXT7_API_KEY": "${CONTEXT7_API_KEY}" },
      "autoApprove": ["resolve-library-id", "query-docs"],
      "enabled": true
    },
    "grep": { "url": "https://mcp.grep.app", "autoApprove": true },
    "toolbox": {
      "command": "npx",
      "args": ["-y", "@qsrahman/workshop-mcp@latest"],
      "env": { "BRAVE_API_KEY": "${BRAVE_API_KEY}" },
      "enabled": true
    }
  }
}
```

- **Precedence:** command-line flags, then the file, then the defaults (the default model is `gpt-5.4-mini`).
  The environment has **one** variable, `OPENAI_API_KEY`, which beats `api_key` in the file. There is deliberately
  no variable for the base URL or the model: one for the base URL would let a project's `.envrc` send your key and
  every prompt somewhere else, and the common names are exported by other programs. The base URL is always passed
  to the client explicitly, so the OpenAI library's own reading of `OPENAI_BASE_URL` cannot redirect it either. (The
  library still reads `OPENAI_ORG_ID`, `OPENAI_PROJECT_ID` and `OPENAI_CUSTOM_HEADERS`; those cannot change where
  the key goes.) Only this user-level file is read, never one from the working directory.
- **A key that is not a setting is an error** that names the key and the file, so a typo such as `max_turn` is not
  silently ignored. `base_url` and `api_key` are optional.
- `reasoning_effort` (unset sends nothing), `max_turns` and `streaming` correspond to the flags above.
- `context_window` is optional. With it, the status line shows how much of the context window the conversation
  uses (for example `25.5k tokens (6%)`); without it only the token count appears.
- `mcpServers` is described in [MCP servers](#mcp-servers). A typo in an entry is an error, like any other key.
- `"${NAME}"` in any string value is replaced from the environment, so secrets need not live in the file.
  If you do put a key in the file, keep it private: `chmod 600 ~/.config/desk-agent/config.json`.
- An MCP server is either a local process (`command`, `args`, `env`) or a remote one (`url`, `headers`), plus
  `enabled` (default true) and `autoApprove`. Giving both `command` and `url`, or neither, is an error.
- Custom `base_url` servers must implement the OpenAI Responses API; some OpenAI-side features may not work
  there.
- Web search is not built in. If you want it, add a search-capable MCP server under `mcpServers`.

## Notes

`-p` starts a fresh conversation each time; only the interactive mode keeps history between messages.

## Safety

Planned behaviour: every file write, file edit and shell command asks for approval, with an option to always
allow a tool for the current session. File tools only reach paths inside the working directory. Shell commands
are not sandboxed, so review them before approving.

## Development

See [AGENTS.md](AGENTS.md).

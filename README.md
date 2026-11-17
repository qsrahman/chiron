# desk-agent

A terminal AI coding agent in Go. It uses the OpenAI Responses API and a Bubble Tea TUI, and works on your
machine through tools (files, shell, web, todo, questions) and optional MCP servers.

> **Status: Phase 3 (v0.3.0).** You can chat with the model in a terminal UI or send a single prompt from the
> command line. The machinery for tool calls is in place (calls run in parallel when safe, and show up as cards
> in the chat), but no tools ship yet, so the model still cannot touch your files. Tools and MCP arrive in the
> next phases; see [PLAN.md](PLAN.md). This README is updated as each phase lands.

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

## Using the chat

Type a message and press `Enter`. The answer streams in and is rendered as markdown when it finishes. The
conversation keeps its history for the whole session.

| Key | Action |
|---|---|
| `Enter` | Send the message (ignored while an answer is streaming) |
| `Ctrl-J` or `Alt-Enter` | New line in the message |
| `PgUp` / `PgDn` | Scroll the transcript |
| `Ctrl-C` | While an answer is streaming: cancel it. Press again to quit. When idle: quit. |
| `Ctrl-D` | Quit when the input is empty |

When the model calls a tool, a card appears in the transcript: `…` while it runs, then `✓` or `✗` with the
first lines of the result. Independent read-only calls run at the same time; anything that could change
something runs alone, in the order the model asked.

A cancelled or failed answer stays on screen but is not added to the conversation history, so the next
message continues from the last complete answer.

Flags:

| Flag | Meaning |
|---|---|
| `-p "prompt"` | Send one prompt, stream the answer to stdout and exit. `Ctrl-C` cancels. Without `-p`, the chat UI starts. |
| `--model name` | Model to use. |
| `--base-url url` | API base URL for OpenAI-compatible servers. |
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
  "mcpServers": {
    "context7": {
      "url": "https://mcp.context7.com/mcp",
      "headers": { "CONTEXT7_API_KEY": "${CONTEXT7_API_KEY}" },
      "enabled": true
    },
    "toolbox": {
      "command": "npx",
      "args": ["-y", "@qsrahman/workshop-mcp@latest"],
      "env": { "BRAVE_API_KEY": "${BRAVE_API_KEY}" },
      "enabled": true
    }
  }
}
```

- `model`, `base_url`, `api_key`: command-line flags override environment variables (`DESK_AGENT_MODEL`,
  `OPENAI_BASE_URL`, `OPENAI_API_KEY`), which override the file. `base_url` and `api_key` are optional. The
  default model is `gpt-5.4-mini`.
- `mcpServers` is accepted but ignored until MCP support lands (Phase 4).
- `"${NAME}"` in any string value is replaced from the environment, so secrets need not live in the file.
  If you do put a key in the file, keep it private: `chmod 600 ~/.config/desk-agent/config.json`.
- An MCP server is either a local process (`command`, `args`, `env`) or a remote one (`url`, `headers`).
  `enabled` defaults to true.
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

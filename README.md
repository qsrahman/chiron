# desk-agent

A terminal AI coding agent in Go. It uses the OpenAI Responses API and a Bubble Tea TUI, and works on your
machine through tools (files, shell, web, todo, questions) and optional MCP servers.

> **Status: Phase 0 (scaffold).** Only `desk-agent --version` exists. Features below are planned; see
> [PLAN.md](PLAN.md) for the phase table. This README is updated as each phase lands.

## Requirements

- Go 1.27 or newer
- An OpenAI API key in `OPENAI_API_KEY` (from Phase 1)

## Build and run

```bash
go build -o desk-agent ./cmd/desk-agent
./desk-agent --version
```

## Configuration (from Phase 1)

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
  `OPENAI_BASE_URL`, `OPENAI_API_KEY`), which override the file. `base_url` and `api_key` are optional.
- `"${NAME}"` in any string value is replaced from the environment, so secrets need not live in the file.
  If you do put a key in the file, keep it private: `chmod 600 ~/.config/desk-agent/config.json`.
- An MCP server is either a local process (`command`, `args`, `env`) or a remote one (`url`, `headers`).
  `enabled` defaults to true.
- Custom `base_url` servers must implement the OpenAI Responses API; some OpenAI-side features may not work
  there.
- Web search is not built in. If you want it, add a search-capable MCP server under `mcpServers`.

## Planned usage

```bash
desk-agent                    # interactive TUI
desk-agent -p "explain main"  # non-interactive, prints the answer
```

## Safety

Planned behaviour: every file write, file edit and shell command asks for approval, with an option to always
allow a tool for the current session. File tools only reach paths inside the working directory. Shell commands
are not sandboxed, so review them before approving.

## Development

See [AGENTS.md](AGENTS.md).

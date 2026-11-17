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
  "approval": "ask",
  "mcpServers": {
    "name": { "command": "npx", "args": ["-y", "pkg"], "env": {} }
  }
}
```

Precedence: command-line flags, then environment variables (`DESK_AGENT_MODEL`, `OPENAI_API_KEY`), then the file.

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

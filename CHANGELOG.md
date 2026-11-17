# Changelog

All notable changes to the codebase are documented in this file. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows [Semantic Versioning](https://semver.org/)

## [Unreleased]

### Added

- Opt-in real-API test (`-tags=integration`) that checks the model name, `reasoning.encrypted_content` and a
  two-turn history replay.

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

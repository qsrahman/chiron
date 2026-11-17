# Changelog

All notable changes to the codebase are documented in this file. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows [Semantic Versioning](https://semver.org/)

## [Unreleased]

### Changed

- Config format in the docs: added `base_url`, `api_key`, remote HTTP MCP servers (`url`, `headers`), the
  per-server `enabled` flag and `${VAR}` expansion; removed `approval`. MCP over HTTP is now in scope.

- MCP client moved from Phase 8 to Phase 4, together with the approval prompt it depends on. File tools,
  `shell`, `todo`/`ask_user` and `web_fetch` are now Phases 5 to 8.
- Phase 3 is now the tool loop only, tested with fake tools; `read_file` and `list_dir` moved to Phase 4
  together with `write_file`, `edit_file` and the approval prompt.
- Dropped the OpenAI built-in `web_search` tool from the plan; web search is decided later. Phase 7 is now
  `web_fetch` only.

### Added

- Project scaffold: Go module `github.com/qsrahman/desk-agent`, `desk-agent --version` entry point and smoke test.
- Documentation: `PLAN.md` (product requirements), `README.md` (usage), `AGENTS.md` (developer guide).

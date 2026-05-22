# Changelog

All notable changes to prompto are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning
follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-05-22

First public release.

### Added

- Lightweight coding agent optimised for smaller local models.
- Providers: Anthropic Messages API, OpenAI Chat Completions (covers
  llama.cpp, LM Studio, Ollama's `/v1` shim, vLLM, OpenRouter).
- Tools: `bash`, `read`, `write`, `edit`, `replace_lines`, `glob`,
  `grep`, `webfetch`, `websearch`, `task` (subagent spawn),
  `todowrite`, `plan_exit`.
- Bubbletea TUI with model picker, plan mode, permission prompts,
  inline diffs, todo panel, and slash commands (`/help`, `/model`,
  `/agent`, `/compact`, `/clear`, `/init`, `/plan`, `/mode`, `/new`,
  `/resume`, `/sessions`, `/undo`, `/todo`, `/env`, `/review`,
  `/context`, `/quit`).
- SQLite-backed session/message/todo persistence with schema
  migrations.
- Configurable conversation compaction and context-window sizing.
- Permission system with project-scoped rulesets and plan-mode
  approvals.
- Project + global config layering with credential-override
  protection.
- `--version` flag.
- One-line installers for Linux, macOS, and Windows.
- GitHub Actions: CI on push/PR, tag-triggered release builds with
  SHA-256 checksums.

[Unreleased]: https://github.com/marcomoesman/prompto/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/marcomoesman/prompto/releases/tag/v0.1.0

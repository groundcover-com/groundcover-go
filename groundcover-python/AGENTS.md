# Agent & contributor rules

This repository contains the groundcover Python runtime SDK. The following
rules are **mandatory** and several of them are enforced by CI.

> **Using the SDK (not contributing to it)?** This file is contributor rules.
> To *instrument a service* with this SDK, see
> [`docs/llm-instrumentation-guide.md`](docs/llm-instrumentation-guide.md).

## Git authorship (enforced)

- **AI agents must never author commits.** The `commit-author-check` CI job fails
  any commit whose author or committer name/email looks like an AI identity
  (for example `cursor`, `copilot`, `bot`, `claude`, `gpt`, `devin`, ...).
- Always commit using a real human `user.name` / `user.email`.
- Do **not** amend or force-push shared branches. Open a PR instead.
- One commit per logical change, with a descriptive message.

## Engineering standards (enforced by `make ci`)

- Python floor is 3.9 (CPython); see `README.md`.
- Core library is **stdlib only**. Heavy/optional dependencies are extras
  (`groundcover[prometheus]`) imported lazily and never enter the core
  dependency list.
- All tooling runs through **uv** (`uv sync`, `uv run pytest`, `uv build`);
  do not use pip directly.
- No import-time side effects and no module-level mutable globals, except the
  single documented module-level default client (Sentry style) in
  `_client_global.py` and the contextvar carrying the request scope in
  `_scope.py`.
- All exported symbols are documented (docstrings).
- `ruff check`, `ruff format --check`, and `pytest` must pass: run `make ci`.
- Dependency versions are pinned via `uv.lock`.

## Scope

- v1 is **error tracking** only. See the architecture spec in the Go SDK
  repository for the full roadmap.
- No external code contributions until supply-chain vetting exists; see
  `CONTRIBUTING.md` in the Go SDK repository. Issues only.

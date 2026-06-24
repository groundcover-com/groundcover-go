# Agent & contributor rules

This repository contains the groundcover Go runtime SDK. The following rules are
**mandatory** and several of them are enforced by CI.

## Git authorship (enforced)

- **AI agents must never author commits.** The `commit-author-check` CI job fails
  any commit whose author or committer name/email looks like an AI identity
  (for example `cursor`, `copilot`, `bot`, `claude`, `gpt`, `devin`, ...).
- Always commit using a real human `user.name` / `user.email`.
- Do **not** amend or force-push shared branches. Open a PR instead.
- One commit per logical change, with a descriptive message.

## Engineering standards (enforced by `make ci`)

- Go floor is the two most recent majors (today `go 1.25`); see `README.md`.
- Core library is **stdlib only**. Heavy/optional dependencies live in nested
  modules (`prometheus/`, `contrib/...`) and never enter the core `go.sum`.
- No `init()` functions and no package-level mutable globals, except the single
  documented package-level default client (Sentry style) in `client_global.go`.
- All exported symbols are documented.
- `gofmt`, `go vet`, `golangci-lint` and `go test -race` must pass: run `make ci`.
- No `replace` / `exclude` in the core `go.mod`.
- Dependencies are vendored where present and verified via `go.sum`.

## Scope

- v1 is **error tracking** only. See the architecture spec for the full roadmap.
- No external code contributions until supply-chain vetting exists; see
  `CONTRIBUTING.md`. Issues only.

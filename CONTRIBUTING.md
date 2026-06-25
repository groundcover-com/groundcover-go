# Contributing

Thanks for your interest in the groundcover Go SDK.

## External contributions

We are **not accepting external code contributions** yet. Until a supply-chain
vetting process is in place, pull requests from outside the organization will be
closed. Please open an **issue** instead — bug reports, reproductions, and
feature requests are very welcome.

## Internal development

- Use a real human git identity. AI agents must never author commits; this is
  enforced by the `commit-author-check` CI job (see `AGENTS.md`).
- Run `make ci` locally before opening a PR. It runs build, `go vet`,
  `golangci-lint`, and the race-enabled test suite across the supported Go
  majors.
- Keep the core library dependency-free (stdlib only). Optional integrations go
  in nested modules.
- One commit per logical change; write a descriptive message.
- Do not introduce `replace`/`exclude` directives into the core `go.mod`.

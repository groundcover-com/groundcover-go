# Releasing

The repository is a Go **multi-module** workspace: the core module at the root
plus nested modules that depend on it (`contrib/gin`, `prometheus`, `examples`).
During development those nested modules resolve the core via a local `replace`:

```text
require github.com/groundcover-com/groundcover-go v0.0.0
replace github.com/groundcover-com/groundcover-go => ../   (or ../../)
```

> **Important:** a `replace` directive in a *dependency* module is ignored by
> consumers. If a customer runs `go get github.com/groundcover-com/groundcover-go/contrib/gin`
> while that module still `require`s core `v0.0.0`, the resolve fails (there is no
> published `v0.0.0`). The `replace` is **for local development only**.

## Release steps

Tag the modules bottom-up so each nested module can require a real, published
version of the core.

1. **Tag the core module first.**
   ```bash
   git tag v0.1.0
   git push origin v0.1.0
   ```

2. **Bump each nested module to require the tagged core, and drop (or keep only
   for dev) the `replace`.** For `contrib/gin`, `prometheus`, and `examples`:
   ```bash
   cd contrib/gin
   go mod edit -require=github.com/groundcover-com/groundcover-go@v0.1.0
   go mod edit -dropreplace=github.com/groundcover-com/groundcover-go
   go mod tidy
   ```
   Repeat for `prometheus/` and `examples/` (the latter also requires
   `contrib/gin`, so bump/drop that replace too).

3. **Tag the nested modules** with their module-path-prefixed tags:
   ```bash
   git tag contrib/gin/v0.1.0
   git tag prometheus/v0.1.0
   git push origin contrib/gin/v0.1.0 prometheus/v0.1.0
   ```

4. **Verify go-gettability from a clean checkout** (outside this repo):
   ```bash
   go get github.com/groundcover-com/groundcover-go@v0.1.0
   go get github.com/groundcover-com/groundcover-go/contrib/gin@v0.1.0
   go get github.com/groundcover-com/groundcover-go/prometheus@v0.1.0
   ```

## Pre-publish checklist

- [ ] No nested module `require`s core `v0.0.0` (the dev placeholder).
- [ ] `replace` directives onto the core are removed (or intentionally dev-only).
- [ ] `make ci` and `make modules` are green.
- [ ] The live `examples/roundtrip` E2E passes against staging.
- [ ] `CHANGELOG`/release notes updated; compatibility table in `README.md` current.

> The `examples/` module is illustrative and is not required to be published, but
> it must still build; keep its `replace` until you choose to publish it.

# Releasing

The repository is a Go **multi-module** workspace: the core module at the root
plus nested modules that depend on it (`contrib/*`, `prometheus`, `examples`).
Each nested module `require`s the **published** core version and keeps a local
`replace` so day-to-day development and CI build against the in-tree core:

```text
require github.com/groundcover-com/groundcover-go v0.1.0
replace github.com/groundcover-com/groundcover-go => ../   (or ../../)
```

> **Why both:** a `replace` directive in a *dependency* module is ignored by
> consumers, while the `require` version is honored. So an external
> `go get github.com/groundcover-com/groundcover-go/contrib/gin` resolves core
> `v0.1.0` from the tag (go-gettable), and an in-repo `go build` resolves core
> from the checkout via the `replace` (branch-local validation). The `require`
> must therefore name a **real, published** core tag — never the `v0.0.0`
> placeholder, which has no source and breaks external resolution.

## Release steps

Tag the modules bottom-up so each nested module requires a real, published
version of the core.

1. **Tag the core module first.**
   ```bash
   git tag v0.1.0
   git push origin v0.1.0
   ```

2. **Point each nested module at the tagged core.** The modules already
   `require` the core version; bump it when cutting a new core release. The
   local `replace` is retained for development and is harmless in published tags
   (consumers ignore it), so it does **not** need to be dropped:
   ```bash
   cd contrib/gin
   go mod edit -require=github.com/groundcover-com/groundcover-go@v0.1.0
   go mod tidy
   ```
   Repeat for every other `contrib/*` module, `prometheus/`, and `examples/`.
   To publish a `replace`-free tag for
   cleanliness, add `go mod edit -dropreplace=github.com/groundcover-com/groundcover-go`
   before tidying — optional, since the `replace` has no effect on consumers.

3. **Tag the nested modules** with their module-path-prefixed tags (one per
   `contrib/*` module plus `prometheus`):
   ```bash
   git tag contrib/gin/v0.1.0
   git tag contrib/echo/v0.1.0   # ... and the other contrib modules
   git tag prometheus/v0.1.0
   git push origin --tags
   ```

4. **Verify go-gettability from a clean checkout** (outside this repo):
   ```bash
   go get github.com/groundcover-com/groundcover-go@v0.1.0
   go get github.com/groundcover-com/groundcover-go/contrib/gin@v0.1.0
   go get github.com/groundcover-com/groundcover-go/prometheus@v0.1.0
   ```

## Pre-publish checklist

- [ ] Every nested module `require`s a real, published core tag (never `v0.0.0`).
- [ ] `replace` directives onto the core are intentionally dev-only (ignored by consumers).
- [ ] `make ci` and `make modules` are green.
- [ ] The live `examples/roundtrip` and `examples/framework-roundtrip` E2Es pass against staging.
- [ ] `CHANGELOG`/release notes updated; compatibility table in `README.md` current.

> The `examples/` module is illustrative and is not required to be published, but
> it must still build; keep its `replace` until you choose to publish it.

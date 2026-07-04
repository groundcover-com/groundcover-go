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

Use a single version variable for the whole release so the core tag, the
nested modules' `require` lines, and the nested-module tags can never drift
apart:

```bash
V=v0.1.2   # the release being cut
```

1. **Tag the core module first.**
   ```bash
   git tag "$V"
   git push origin "$V"
   ```

2. **Point each nested module at the tagged core.** The modules already
   `require` the core version; pin them to the release being cut (`$V`) so the
   published nested-module tags resolve against the matching core tag. The
   local `replace` is retained for development and is harmless in published tags
   (consumers ignore it), so it does **not** need to be dropped:
   ```bash
   cd contrib/gin
   go mod edit -require=github.com/groundcover-com/groundcover-go@"$V"
   go mod tidy
   ```
   Repeat for every other `contrib/*` module, `prometheus/`, and `examples/`,
   then commit the go.mod/go.sum updates **before** tagging the nested modules.
   To publish a `replace`-free tag for
   cleanliness, add `go mod edit -dropreplace=github.com/groundcover-com/groundcover-go`
   before tidying — optional, since the `replace` has no effect on consumers.

3. **Tag the nested modules** with their module-path-prefixed tags (one per
   `contrib/*` module plus `prometheus`), on the commit that contains the
   updated `require` lines:
   ```bash
   for m in contrib/gin contrib/echo contrib/fiber contrib/fasthttp \
            contrib/iris contrib/negroni contrib/grpc prometheus; do
     git tag "$m/$V"
   done
   # Push the release tags explicitly (never `--tags`, which would also
   # publish any unrelated or stale local tags):
   git push origin "contrib/gin/$V" "contrib/echo/$V" "contrib/fiber/$V" \
     "contrib/fasthttp/$V" "contrib/iris/$V" "contrib/negroni/$V" \
     "contrib/grpc/$V" "prometheus/$V"
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

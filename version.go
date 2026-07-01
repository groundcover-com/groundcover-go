package groundcover

import (
	_ "embed"
	"runtime/debug"
	"strings"
)

//go:embed go.mod
var goMod string

const (
	sdkName     = "groundcover-go" // pragma: allowlist secret
	sdkLanguage = "go"

	// devVersion is returned by Version when the module has no published tag
	// in its build info (for example when running tests or `go run` inside
	// this repo).
	devVersion = "dev"
)

// Version returns the SDK version reported in telemetry (telemetry.sdk.version)
// and the User-Agent header. It is resolved from the module's build metadata so
// it matches the published git tag without a manual bump on each release.
//
// When the SDK is consumed as a tagged module dependency, Version returns the
// tag (for example "0.1.1"). When running directly from source
// (go test / go run inside this repo) build metadata has no tag and Version
// returns "dev". The resolution is cheap and safe to call from hot paths;
// callers that want to avoid repeated work may cache the result themselves.
func Version() string {
	modulePath := modulePathFromGoMod()
	if modulePath == "" {
		return devVersion
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return devVersion
	}
	for _, dep := range bi.Deps {
		if dep.Path == modulePath {
			if v := normalizeModuleVersion(dep.Version); v != "" {
				return v
			}
		}
	}
	if bi.Main.Path == modulePath {
		if v := normalizeModuleVersion(bi.Main.Version); v != "" {
			return v
		}
	}
	return devVersion
}

// modulePathFromGoMod extracts the module path from the embedded go.mod.
// It tolerates the whitespace and comment forms accepted by the go.mod grammar
// (space or tab separation, trailing `// ...` comments).
func modulePathFromGoMod() string {
	for line := range strings.SplitSeq(goMod, "\n") {
		line = strings.TrimSpace(stripLineComment(line))
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "module" {
			continue
		}
		// A double-quoted module path is permitted by the go.mod grammar.
		return strings.Trim(fields[1], `"`)
	}
	return ""
}

// stripLineComment removes a `// ...` trailing comment from a go.mod line.
func stripLineComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

func normalizeModuleVersion(v string) string {
	if v == "" || v == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

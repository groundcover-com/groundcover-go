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
)

// Version is the SDK version reported in telemetry (telemetry.sdk.version) and
// the User-Agent header. It is resolved from the module's build metadata so it
// matches the published git tag without a manual bump on each release.
//
// When the SDK is consumed as a tagged module dependency, Version is the tag
// (for example "0.1.1"). When running directly from source (go test / go run
// inside this repo), build metadata has no tag and Version is "dev".
//
//nolint:gochecknoglobals // init-once from build metadata; exported for callers and telemetry
var Version = resolveVersion()

func resolveVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	modulePath := modulePathFromGoMod()
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
	return "dev"
}

func modulePathFromGoMod() string {
	for line := range strings.SplitSeq(goMod, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func normalizeModuleVersion(v string) string {
	if v == "" || v == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

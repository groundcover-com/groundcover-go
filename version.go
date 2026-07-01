package groundcover

import (
	"reflect"
	"runtime/debug"
	"strings"
)

const (
	sdkName     = "groundcover-go" // pragma: allowlist secret
	sdkLanguage = "go"

	// devVersion is returned by Version when the module has no published tag
	// in its build info (for example when running tests or `go run` inside
	// this repo).
	devVersion = "dev"
)

// pkgMarker is an unexported empty type used to discover this package's
// import path at runtime via reflection. Because version.go lives in the
// module's root package, that import path equals the module path, so the
// value can be looked up directly in runtime/debug.BuildInfo without
// embedding or parsing go.mod.
type pkgMarker struct{}

// modulePath returns the module path of this SDK. It is derived from the
// package that pkgMarker is declared in, so it stays correct even if the
// module is renamed or forked (the fork's build info would report the fork's
// module path, which matches the fork's package path).
func modulePath() string {
	return reflect.TypeOf(pkgMarker{}).PkgPath()
}

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
	mp := modulePath()
	if mp == "" {
		return devVersion
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return devVersion
	}
	for _, dep := range bi.Deps {
		if dep.Path == mp {
			if v := normalizeModuleVersion(dep.Version); v != "" {
				return v
			}
		}
	}
	if bi.Main.Path == mp {
		if v := normalizeModuleVersion(bi.Main.Version); v != "" {
			return v
		}
	}
	return devVersion
}

func normalizeModuleVersion(v string) string {
	if v == "" || v == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

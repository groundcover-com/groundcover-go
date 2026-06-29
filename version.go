package groundcover

import (
	"runtime/debug"
	"strings"
)

const (
	sdkName     = "groundcover-go"
	sdkLanguage = "go"
	modulePath  = "github.com/groundcover-com/groundcover-go"
)

// Version is the SDK version reported in telemetry (telemetry.sdk.version) and
// the User-Agent header. It is resolved automatically from the module's build
// metadata so it stays in sync with the published git tag without any manual
// update on each release.
//
// When running directly from source (go test / go run inside this repo) the
// module has no tag in build info and Version is "dev".
var Version = func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	// Running as a dependency: the SDK appears in bi.Deps.
	for _, dep := range bi.Deps {
		if dep.Path == modulePath {
			return strings.TrimPrefix(dep.Version, "v")
		}
	}
	// Running as the main module (e.g. tagged release binary):
	if bi.Main.Path == modulePath && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}()

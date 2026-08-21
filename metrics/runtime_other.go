//go:build !linux

package metrics

import (
	"io/fs"
	"strings"
)

// rootFS returns nil on non-Linux platforms: the process metrics are sourced
// from /proc and /sys/fs/cgroup, which do not exist here.
func rootFS() fs.FS {
	return nil
}

// writeProcessMetrics is a no-op on non-Linux platforms. The process_* and
// cgroup metrics are omitted rather than emitted with wrong values.
func (s *runtimeSource) writeProcessMetrics(_ *strings.Builder) {}

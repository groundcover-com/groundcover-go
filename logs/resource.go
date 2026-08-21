package logs

import (
	"os"
	"runtime"
	"strconv"
)

type resource struct {
	service string
	env     string
	cluster string
	source  string
	attrs   map[string]string
}

func detectResource(cfg Config) resource {
	service := firstNonEmpty(cfg.ServiceName, os.Getenv("OTEL_SERVICE_NAME"), os.Getenv("GC_SERVICE_NAME"))
	env := firstNonEmpty(cfg.Env, os.Getenv("GC_ENV"), os.Getenv("DEPLOYMENT_ENVIRONMENT"))
	cluster := firstNonEmpty(cfg.Cluster, os.Getenv("GC_CLUSTER"), os.Getenv("CLUSTER_NAME"), os.Getenv("K8S_CLUSTER_NAME"))
	hostname, _ := os.Hostname()
	attrs := map[string]string{
		"telemetry.sdk.name":         "groundcover-go",
		"telemetry.sdk.language":     "go",
		"process.runtime.name":       "go",
		"process.runtime.version":    runtime.Version(),
		"os.type":                    runtime.GOOS,
		"host.arch":                  runtime.GOARCH,
		"host.num_cpu":               strconv.Itoa(runtime.NumCPU()),
		"process.runtime.gomaxprocs": strconv.Itoa(runtime.GOMAXPROCS(0)),
	}
	if hostname != "" {
		attrs["host.name"] = hostname
	}
	return resource{service: service, env: env, cluster: cluster, source: cfg.Source, attrs: attrs}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

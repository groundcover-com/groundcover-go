package groundcover

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"
)

// OTel resource attribute keys used across resource detection and wire encoding.
const (
	attrServiceName  = "service.name"
	attrServiceVer   = "service.version"
	attrDeployEnv    = "deployment.environment.name"
	attrK8sNamespace = "k8s.namespace.name"
	attrK8sCluster   = "k8s.cluster.name"
)

// resource holds the detected resource/spine attributes, read once at init.
type resource struct {
	serviceName string
	env         string
	release     string
	namespace   string
	cluster     string

	// attrs are the OTel resource attributes carried with each event.
	attrs map[string]string
	// mainModule is the main module path used to classify in-app frames.
	mainModule string
	// startTime is the SDK initialization time, reported as session_start_time.
	startTime time.Time
}

// firstNonEmpty returns the first non-empty argument.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// detectResource reads configuration, environment, and runtime information into
// a resource. Explicit Config fields take precedence over environment values.
func detectResource(cfg Config) resource {
	serviceName := firstNonEmpty(cfg.ServiceName, os.Getenv("OTEL_SERVICE_NAME"), os.Getenv("GC_SERVICE_NAME"))
	env := firstNonEmpty(cfg.Env, os.Getenv("GC_ENV"), os.Getenv("DEPLOYMENT_ENVIRONMENT"))
	release := firstNonEmpty(cfg.Release, os.Getenv("GC_RELEASE"))

	namespace := firstNonEmpty(os.Getenv("POD_NAMESPACE"), os.Getenv("NAMESPACE"), os.Getenv("K8S_NAMESPACE"))
	cluster := firstNonEmpty(os.Getenv("GC_CLUSTER"), os.Getenv("CLUSTER_NAME"), os.Getenv("K8S_CLUSTER_NAME"))
	podUID := os.Getenv("POD_UID")
	podName := firstNonEmpty(os.Getenv("POD_NAME"), os.Getenv("HOSTNAME"))
	nodeName := os.Getenv("NODE_NAME")

	hostname, _ := os.Hostname()

	mainModule := ""
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Path != "" {
		mainModule = bi.Main.Path
	}

	attrs := map[string]string{
		"telemetry.sdk.name":         sdkName,
		"telemetry.sdk.language":     sdkLanguage,
		"telemetry.sdk.version":      Version(),
		"process.runtime.name":       "go",
		"process.runtime.version":    runtime.Version(),
		"os.type":                    runtime.GOOS,
		"host.arch":                  runtime.GOARCH,
		"host.num_cpu":               strconv.Itoa(runtime.NumCPU()),
		"process.runtime.gomaxprocs": strconv.Itoa(runtime.GOMAXPROCS(0)),
	}
	putIfSet := func(k, v string) {
		if v != "" {
			attrs[k] = v
		}
	}
	putIfSet(attrServiceName, serviceName)
	putIfSet(attrServiceVer, release)
	putIfSet(attrDeployEnv, env)
	putIfSet("host.name", hostname)
	putIfSet("k8s.pod.uid", podUID)
	putIfSet("k8s.pod.name", podName)
	putIfSet(attrK8sNamespace, namespace)
	putIfSet("k8s.node.name", nodeName)
	putIfSet(attrK8sCluster, cluster)

	return resource{
		serviceName: serviceName,
		env:         env,
		release:     release,
		namespace:   namespace,
		cluster:     cluster,
		attrs:       attrs,
		mainModule:  mainModule,
		startTime:   time.Now(),
	}
}

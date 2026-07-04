"""Resource detection: service identity and runtime/host/k8s attributes."""

from __future__ import annotations

import dataclasses
import os
import platform
import socket
import sys
import time
from typing import Dict

from ._config import Config
from ._version import SDK_LANGUAGE, SDK_NAME, VERSION

# OTel resource attribute keys used across resource detection and wire encoding.
ATTR_SERVICE_NAME = "service.name"
ATTR_SERVICE_VER = "service.version"
ATTR_DEPLOY_ENV = "deployment.environment.name"
ATTR_K8S_NAMESPACE = "k8s.namespace.name"
ATTR_K8S_CLUSTER = "k8s.cluster.name"


@dataclasses.dataclass
class Resource:
    """The detected resource/spine attributes, read once at init."""

    service_name: str = ""
    env: str = ""
    release: str = ""
    namespace: str = ""
    cluster: str = ""

    attrs: Dict[str, str] = dataclasses.field(default_factory=dict)
    """The OTel resource attributes carried with each event."""
    in_app_root: str = ""
    """The application root path used to classify in-app frames."""
    start_time_ns: int = 0
    """The SDK initialization time, reported as session_start_time."""


def first_non_empty(*vals: str) -> str:
    """Return the first non-empty argument."""
    for v in vals:
        if v:
            return v
    return ""


def _detect_app_root() -> str:
    """Best-effort detection of the application root: the directory of the
    ``__main__`` module's file, falling back to the working directory. This is
    the Python analog of the Go SDK reading the main module path from build
    info."""
    main = sys.modules.get("__main__")
    main_file = getattr(main, "__file__", None)
    if main_file:
        try:
            return os.path.dirname(os.path.abspath(main_file))
        except Exception:
            pass
    try:
        return os.getcwd()
    except Exception:
        return ""


def detect_resource(cfg: Config) -> Resource:
    """Read configuration, environment, and runtime information into a
    Resource. Explicit Config fields take precedence over environment
    values."""
    env_get = os.environ.get

    service_name = first_non_empty(
        cfg.service_name, env_get("OTEL_SERVICE_NAME", ""), env_get("GC_SERVICE_NAME", "")
    )
    env = first_non_empty(cfg.env, env_get("GC_ENV", ""), env_get("DEPLOYMENT_ENVIRONMENT", ""))
    release = first_non_empty(cfg.release, env_get("GC_RELEASE", ""))

    namespace = first_non_empty(
        env_get("POD_NAMESPACE", ""), env_get("NAMESPACE", ""), env_get("K8S_NAMESPACE", "")
    )
    cluster = first_non_empty(
        env_get("GC_CLUSTER", ""), env_get("CLUSTER_NAME", ""), env_get("K8S_CLUSTER_NAME", "")
    )
    pod_uid = env_get("POD_UID", "")
    pod_name = first_non_empty(env_get("POD_NAME", ""), env_get("HOSTNAME", ""))
    node_name = env_get("NODE_NAME", "")

    try:
        hostname = socket.gethostname()
    except Exception:
        hostname = ""

    in_app_root = cfg.in_app_root or _detect_app_root()

    attrs: Dict[str, str] = {
        "telemetry.sdk.name": SDK_NAME,
        "telemetry.sdk.language": SDK_LANGUAGE,
        "telemetry.sdk.version": VERSION,
        "process.runtime.name": platform.python_implementation().lower(),
        "process.runtime.version": platform.python_version(),
        "os.type": sys.platform,
        "host.arch": platform.machine(),
        "host.num_cpu": str(os.cpu_count() or 0),
    }

    def put_if_set(k: str, v: str) -> None:
        if v:
            attrs[k] = v

    put_if_set(ATTR_SERVICE_NAME, service_name)
    put_if_set(ATTR_SERVICE_VER, release)
    put_if_set(ATTR_DEPLOY_ENV, env)
    put_if_set("host.name", hostname)
    put_if_set("k8s.pod.uid", pod_uid)
    put_if_set("k8s.pod.name", pod_name)
    put_if_set(ATTR_K8S_NAMESPACE, namespace)
    put_if_set("k8s.node.name", node_name)
    put_if_set(ATTR_K8S_CLUSTER, cluster)

    return Resource(
        service_name=service_name,
        env=env,
        release=release,
        namespace=namespace,
        cluster=cluster,
        attrs=attrs,
        in_app_root=in_app_root,
        start_time_ns=time.time_ns(),
    )

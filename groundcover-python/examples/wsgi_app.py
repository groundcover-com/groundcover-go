"""A WSGI server wrapped with the groundcover middleware.

    uv run examples/wsgi_app.py

Then: curl http://localhost:8000/         (healthy)
      curl http://localhost:8000/panic    (captured + 500)
"""

from __future__ import annotations

import os
from wsgiref.simple_server import make_server

import groundcover
from groundcover.wsgi import GroundcoverMiddleware


def app(environ, start_response):
    path = environ.get("PATH_INFO", "/")
    if path == "/panic":
        groundcover.set_user(groundcover.User(id="u-1"))  # visible on the captured event
        raise RuntimeError("handler exploded")
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [b"ok\n"]


def main() -> None:
    groundcover.init(
        dsn=os.environ.get("GC_DSN", "https://example.invalid"),
        ingestion_key=os.environ.get("GC_INGESTION_KEY", ""),
        service_name="example-wsgi",
        debug=True,
    )
    try:
        with make_server("", 8000, GroundcoverMiddleware(app)) as httpd:
            print("serving on :8000")
            httpd.serve_forever()
    finally:
        groundcover.close(timeout=5.0)


if __name__ == "__main__":
    main()

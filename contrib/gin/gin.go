// Package gin provides Gin middleware that recovers panics, captures them
// (and optionally errors collected on the Gin context) through the core SDK,
// and seeds a fresh request scope. It is a separate module so the gin
// dependency never enters the core SDK's go.sum.
package gin

import (
	"errors"
	"net/http"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
)

// Options configures the middleware. The zero value is valid and captures
// panics only, mirroring Sentry's Gin integration: the panic is captured as an
// unhandled error and re-raised so Gin's own recovery (or the server) handles
// the response exactly as it would without the middleware.
type Options struct {
	// CaptureContextErrors turns ON capturing errors collected on the Gin
	// context via c.Error(...) (c.Errors) as handled errors. Off by default:
	// only panics are captured unless this is set. Errors on requests that
	// ended with a 4xx response (binding failures, validation errors, and
	// other client errors) are never captured: they are request outcomes,
	// not application faults.
	CaptureContextErrors bool

	// DisableRepanic turns OFF re-raising the panic after capture, so the
	// middleware swallows it instead. Leave this off when a recovery
	// middleware (for example gin.Recovery via gin.Default) is installed:
	// re-raising lets it turn the panic into a 500 as usual. With
	// DisableRepanic set and no status written before the panic, Gin
	// finalizes the response as an empty 200.
	DisableRepanic bool
}

// New returns Gin middleware that reports to the package-level default client
// configured with the core SDK's Init. Panics are captured as unhandled errors
// and re-raised (unless Options.DisableRepanic is set); panics with
// http.ErrAbortHandler and broken-pipe/connection-reset panics (which Gin's
// own recovery also special-cases) are never captured. Errors collected on
// the Gin context are captured as handled errors when
// Options.CaptureContextErrors is set.
func New(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Seed a fresh, isolated scope for this request. The scope is mutable and
		// shared, and we re-read c.Request.Context() at capture time, so a
		// handler's SetUser/WithScope is reflected in the captured error.
		c.Request = c.Request.WithContext(gc.WithIsolatedScope(c.Request.Context()))

		defer func() {
			if rec := recover(); rec != nil {
				if !isAbortPanic(rec) && !isBrokenPipePanic(rec) {
					gc.CaptureRecovered(c.Request.Context(), rec, panicAttributes(c, !opts.DisableRepanic))
				}
				if !opts.DisableRepanic {
					panic(rec) // re-raise to Gin's recovery / the server
				}
			}
		}()

		c.Next()

		if opts.CaptureContextErrors && isServerError(c) {
			for _, e := range c.Errors {
				gc.CaptureError(c.Request.Context(), e.Err, requestAttributes(c))
			}
		}
	}
}

// isServerError reports whether the collected errors represent application
// faults worth capturing. Requests that ended with a 4xx response are normal
// request outcomes (bad input, auth failures) and are skipped. Gin's response
// is already written when the middleware resumes, so the written status is
// authoritative.
func isServerError(c *gin.Context) bool {
	status := c.Writer.Status()
	return status < http.StatusBadRequest || status >= http.StatusInternalServerError
}

// isAbortPanic reports whether the recovered value is net/http's deliberate
// abort sentinel (http.ErrAbortHandler): a request outcome, not a fault.
func isAbortPanic(rec any) bool {
	err, ok := rec.(error)
	return ok && errors.Is(err, http.ErrAbortHandler)
}

// isBrokenPipePanic reports whether the recovered value is a broken-pipe or
// connection-reset network error: the client went away mid-response. Gin's own
// recovery special-cases these (it aborts without writing a 500); they are
// connection conditions, not application faults, and are not captured.
// errors.Is against the syscall errnos handles any wrapping; the text match is
// a fallback for error chains that don't unwrap to an errno (mirrors Gin's own
// string-based detection).
func isBrokenPipePanic(rec any) bool {
	err, ok := rec.(error)
	if !ok {
		return false
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset by peer")
}

func requestAttributes(c *gin.Context) gc.Option {
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Request.Method,
		"url.path":                  c.Request.URL.Path,
		"http.route":                c.FullPath(),
		"http.response.status_code": c.Writer.Status(),
	})
}

// panicAttributes is requestAttributes for the panic path. When the panic is
// re-raised (willRepanic) and the handler wrote nothing, the status the client
// receives is the 500 the recovery layer produces, not Gin's in-flight default
// (200), so that is what is reported. On the swallow path (DisableRepanic) the
// response really is finalized as-is, so the in-flight status is kept.
func panicAttributes(c *gin.Context, willRepanic bool) gc.Option {
	status := c.Writer.Status()
	if willRepanic && !c.Writer.Written() {
		status = http.StatusInternalServerError
	}
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Request.Method,
		"url.path":                  c.Request.URL.Path,
		"http.route":                c.FullPath(),
		"http.response.status_code": status,
	})
}

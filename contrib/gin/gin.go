// Package gin provides Gin middleware that recovers panics, captures them
// (and optionally errors collected on the Gin context) through the core SDK,
// and seeds a fresh request scope. It is a separate module so the gin
// dependency never enters the core SDK's go.sum.
package gin

import (
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
	// only panics are captured unless this is set.
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
// and re-raised (unless Options.DisableRepanic is set); errors collected on
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
				gc.CaptureRecovered(c.Request.Context(), rec, requestAttributes(c))
				if !opts.DisableRepanic {
					panic(rec) // re-raise to Gin's recovery / the server
				}
			}
		}()

		c.Next()

		if opts.CaptureContextErrors {
			for _, e := range c.Errors {
				gc.CaptureError(c.Request.Context(), e.Err, requestAttributes(c))
			}
		}
	}
}

func requestAttributes(c *gin.Context) gc.Option {
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Request.Method,
		"url.path":                  c.Request.URL.Path,
		"http.route":                c.FullPath(),
		"http.response.status_code": c.Writer.Status(),
	})
}

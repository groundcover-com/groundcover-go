// Package gin provides Gin middleware that recovers panics, captures them (and
// any errors collected on the Gin context) through groundcover, and seeds a
// fresh request scope. It is a separate module so the gin dependency never
// enters the core SDK's go.sum.
package gin

import (
	"github.com/gin-gonic/gin"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
)

// Options configures the middleware. The zero value is valid and enables the
// recommended defaults: panics are recovered, captured, and re-raised, and
// errors collected on the Gin context are captured as handled errors.
type Options struct {
	// IgnoreContextErrors turns OFF capturing errors collected on the Gin
	// context via c.Error(...) (c.Errors). Panics recovered by the middleware
	// are always captured regardless of this setting.
	IgnoreContextErrors bool
}

// New returns Gin middleware that reports to the package-level default client
// configured with the core SDK's Init. Panics are captured as unhandled errors
// and re-raised (so Gin's own recovery, if installed, still runs); collected
// c.Errors are captured as handled errors unless Options.IgnoreContextErrors
// is set.
func New(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Seed a fresh, isolated scope for this request. The scope is mutable and
		// shared, and we re-read c.Request.Context() at capture time, so a
		// handler's SetUser/WithScope is reflected in the captured error.
		c.Request = c.Request.WithContext(gc.WithIsolatedScope(c.Request.Context()))

		defer func() {
			if rec := recover(); rec != nil {
				gc.CaptureRecovered(c.Request.Context(), rec, requestAttributes(c))
				panic(rec) // re-raise to Gin's recovery / the server
			}
		}()

		c.Next()

		if !opts.IgnoreContextErrors {
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

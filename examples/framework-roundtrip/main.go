// Command framework-roundtrip is the live per-framework end-to-end verifier
// for the SDK. For every supported integration (net/http, Gin,
// Echo, Fiber, fasthttp, Iris, Negroni, gRPC) it wires the real middleware,
// drives one failing request through it carrying a unique needle (gc.test_id),
// then queries the events API until every event is found and verifies its
// content (handled flag, framework tag, request attributes). It exits 0 on
// success and 1 on failure.
//
// It uses the same environment as examples/roundtrip:
//
//	GC_DSN            base ingestion origin (BYOC ingestion endpoint)
//	GC_INGESTION_KEY  write key (RUM-type)
//	GC_API_KEY        read key for the events query API
//	GC_API_URL        read API base (see examples/roundtrip)
//	GC_BACKEND_ID     optional; sent as X-Backend-Id when set
//
//	go run .
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v2"
	"github.com/kataras/iris/v12"
	"github.com/labstack/echo/v4"
	"github.com/urfave/negroni/v3"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gc "github.com/groundcover-com/groundcover-go"
	gcecho "github.com/groundcover-com/groundcover-go/contrib/echo"
	gcfasthttp "github.com/groundcover-com/groundcover-go/contrib/fasthttp"
	gcfiber "github.com/groundcover-com/groundcover-go/contrib/fiber"
	gcgin "github.com/groundcover-com/groundcover-go/contrib/gin"
	gcgrpc "github.com/groundcover-com/groundcover-go/contrib/grpc"
	gciris "github.com/groundcover-com/groundcover-go/contrib/iris"
	gcnegroni "github.com/groundcover-com/groundcover-go/contrib/negroni"
	"github.com/groundcover-com/groundcover-go/examples/internal/e2e"
	gchttp "github.com/groundcover-com/groundcover-go/nethttp"
)

const flushTimeout = 10 * time.Second

// scenario drives one failing request through a single framework integration.
type scenario struct {
	framework string
	// handled is the expected error_handled flag: true for captured handler
	// errors, false for recovered panics.
	handled bool
	// extraKey/extraWant assert one framework-specific attribute round-tripped.
	extraKey  string
	extraWant string
	trigger   func(testID string) error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "framework-roundtrip: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("framework-roundtrip: PASS")
}

func run() error {
	env, err := e2e.LoadEnv()
	if err != nil {
		return err
	}

	if err := gc.Init(gc.Config{
		DSN:          env.DSN,
		IngestionKey: env.IngestionKey,
		ServiceName:  "groundcover-go-framework-roundtrip",
		Env:          "examples",
		// Release is the application's version (releaseId / service.version);
		// the SDK version travels separately in telemetry.sdk.version.
		Release:       "1.0.0",
		FlushInterval: time.Second,
	}); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	defer func() { _ = gc.CloseTimeout(flushTimeout) }()

	scenarios := allScenarios()
	needles := make(map[string]string, len(scenarios)) // framework -> testID

	for _, s := range scenarios {
		testID := e2e.NewID()
		needles[s.framework] = testID
		fmt.Printf("framework-roundtrip: %s gc.test_id=%s\n", s.framework, testID)
		if err := s.trigger(testID); err != nil {
			return fmt.Errorf("%s: trigger: %w", s.framework, err)
		}
	}

	if err := gc.FlushTimeout(flushTimeout); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	fmt.Printf("framework-roundtrip: submitted %d events, polling for read-back...\n", len(scenarios))

	for _, s := range scenarios {
		testID := needles[s.framework]
		raw, err := e2e.PollForNeedle(env, testID)
		if err != nil {
			return fmt.Errorf("%s: %w", s.framework, err)
		}
		if err := verifyEvent(raw, s, testID); err != nil {
			return fmt.Errorf("%s: read-back content mismatch: %w\nevent: %s", s.framework, err, e2e.PrettyJSON(raw))
		}
		fmt.Printf("framework-roundtrip: %s verified\n", s.framework)
	}
	return nil
}

// verifyEvent checks the stored event carries the needle, the expected handled
// flag, the framework tag, and one framework-specific request attribute.
func verifyEvent(raw []byte, s scenario, testID string) error {
	e, err := e2e.DecodeStoredEvent(raw)
	if err != nil {
		return err
	}
	if e.Type != "exception" || e.Category != "rum" {
		return fmt.Errorf("type/category = %q/%q, want exception/rum", e.Type, e.Category)
	}
	checks := map[string]string{
		"error_metadata.gc.test_id":   testID,
		"error_metadata.gc.framework": s.framework,
		"error_handled":               strconv.FormatBool(s.handled),
		s.extraKey:                    s.extraWant,
	}
	for k, want := range checks {
		if got := e.StringAttributes[k]; got != want {
			return fmt.Errorf("string_attributes[%q] = %q, want %q", k, got, want)
		}
	}
	return nil
}

// markScope tags the seeded request scope with the needle and framework name so
// the middleware's capture carries them. Mutating the scope (rather than
// passing per-call options) also proves the middleware seeded a live, shared
// scope for the request.
func markScope(ctx context.Context, framework, testID string) {
	gc.WithScope(ctx, func(sc *gc.Scope) {
		sc.SetAttributes(gc.Attributes{
			"gc.test_id":   testID,
			"gc.framework": framework,
		})
	})
}

func allScenarios() []scenario {
	httpMethod := "error_metadata.http.request.method"
	return []scenario{
		{framework: "nethttp", handled: false, extraKey: httpMethod, extraWant: http.MethodGet, trigger: triggerNetHTTP},
		{framework: "gin", handled: true, extraKey: httpMethod, extraWant: http.MethodGet, trigger: triggerGin},
		{framework: "echo", handled: true, extraKey: httpMethod, extraWant: http.MethodGet, trigger: triggerEcho},
		{framework: "fiber", handled: true, extraKey: httpMethod, extraWant: http.MethodGet, trigger: triggerFiber},
		{framework: "fasthttp", handled: false, extraKey: httpMethod, extraWant: http.MethodGet, trigger: triggerFastHTTP},
		{framework: "iris", handled: true, extraKey: httpMethod, extraWant: http.MethodGet, trigger: triggerIris},
		{framework: "negroni", handled: false, extraKey: httpMethod, extraWant: http.MethodGet, trigger: triggerNegroni},
		{framework: "grpc", handled: true, extraKey: "error_metadata.rpc.system", extraWant: "grpc", trigger: triggerGRPC},
	}
}

func triggerNetHTTP(testID string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/checkout", func(http.ResponseWriter, *http.Request) {
		panic("framework-roundtrip nethttp panic " + testID)
	})
	handler := gchttp.Middleware(withScopeMark(mux, "nethttp", testID))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil)
	swallowPanic(func() { handler.ServeHTTP(httptest.NewRecorder(), req) })
	return nil
}

func triggerGin(testID string) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gcgin.New(gcgin.Options{CaptureContextErrors: true}))
	r.GET("/checkout", func(c *gin.Context) {
		markScope(c.Request.Context(), "gin", testID)
		_ = c.Error(errors.New("framework-roundtrip gin error " + testID))
		c.Status(http.StatusInternalServerError)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	return nil
}

func triggerEcho(testID string) error {
	e := echo.New()
	e.Use(gcecho.New(gcecho.Options{CaptureHandlerErrors: true}))
	e.GET("/checkout", func(c echo.Context) error {
		markScope(c.Request().Context(), "echo", testID)
		return errors.New("framework-roundtrip echo error " + testID)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil)
	e.ServeHTTP(httptest.NewRecorder(), req)
	return nil
}

func triggerFiber(testID string) error {
	app := fiber.New()
	app.Use(gcfiber.New(gcfiber.Options{CaptureHandlerErrors: true}))
	app.Get("/checkout", func(c *fiber.Ctx) error {
		markScope(c.UserContext(), "fiber", testID)
		return errors.New("framework-roundtrip fiber error " + testID)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil)
	resp, err := app.Test(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func triggerFastHTTP(testID string) error {
	handler := gcfasthttp.New(func(ctx *fasthttp.RequestCtx) {
		markScope(gcfasthttp.ScopeContext(ctx), "fasthttp", testID)
		panic("framework-roundtrip fasthttp panic " + testID)
	}, gcfasthttp.Options{})

	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(http.MethodGet)
	ctx.Request.SetRequestURI("http://example.com/checkout")
	swallowPanic(func() { handler(&ctx) })
	return nil
}

func triggerIris(testID string) error {
	app := iris.New()
	app.Use(gciris.New(gciris.Options{CaptureContextErrors: true}))
	app.Get("/checkout", func(ctx iris.Context) {
		markScope(ctx.Request().Context(), "iris", testID)
		ctx.StopWithError(http.StatusInternalServerError, errors.New("framework-roundtrip iris error "+testID))
	})
	if err := app.Build(); err != nil {
		return err
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/checkout", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)
	return nil
}

func triggerNegroni(testID string) error {
	n := negroni.New()
	n.Use(gcnegroni.New(gcnegroni.Options{}))
	n.UseHandler(withScopeMark(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("framework-roundtrip negroni panic " + testID)
	}), "negroni", testID))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil)
	swallowPanic(func() { n.ServeHTTP(httptest.NewRecorder(), req) })
	return nil
}

func triggerGRPC(testID string) error {
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{CaptureRPCErrors: true})
	handler := grpc.UnaryHandler(func(ctx context.Context, _ any) (any, error) {
		markScope(ctx, "grpc", testID)
		return nil, status.Error(codes.Internal, "framework-roundtrip grpc error "+testID)
	})

	_, err := interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/checkout.Service/Checkout"}, handler)
	if err == nil {
		return errors.New("expected the handler error to propagate")
	}
	return nil
}

// withScopeMark wraps next so the request scope is tagged before the handler
// runs, mirroring what real applications do with per-request enrichment.
func withScopeMark(next http.Handler, framework, testID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		markScope(r.Context(), framework, testID)
		next.ServeHTTP(w, r)
	})
}

// swallowPanic runs fn and discards the re-raised panic, standing in for the
// recovery a real server (net/http, a recovery middleware) provides.
func swallowPanic(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

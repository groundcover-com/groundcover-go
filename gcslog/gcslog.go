// Package gcslog bridges the standard library's log/slog to groundcover: a
// slog.Handler that forwards records to the SDK's log signal, so they are
// delivered directly to groundcover with no agent or file tailer.
//
// It is the first of the logging-library bridges (slog, logrus, zap, ...).
// Bridges are thin adapters over the core emission API (EmitLog); they own
// only the translation from the library's record model to a level, message,
// and flat attribute bag. slog is part of the standard library, so this bridge
// lives in the core module; bridges with third-party dependencies live in
// nested modules under contrib/.
//
// Usage:
//
//	_ = groundcover.Init(groundcover.Config{DSN: dsn, IngestionKey: key, EnableLogs: true})
//	logger := slog.New(gcslog.NewHandler(gcslog.Options{}))
//	logger.Info("order created", "order_id", "o-9")
//
// To keep writing to stdout as well, combine handlers with a fan-out such as
// slog.NewMultiHandler (Go 1.26+) or any multi-handler implementation.
package gcslog

import (
	"context"
	"log/slog"

	groundcover "github.com/groundcover-com/groundcover-go"
	"github.com/groundcover-com/groundcover-go/internal/safeguard"
	gclogs "github.com/groundcover-com/groundcover-go/logs"
)

// Options configures a Handler. The zero value forwards info-and-above records
// through the package-level groundcover client.
type Options struct {
	// Client is the standalone logs emitter to use. Pass a *logs.Client or
	// groundcover.Client.LogsClient(). When nil, the package-level aggregate
	// client configured via groundcover.Init is used.
	Client gclogs.Emitter
	// Level is the minimum record level forwarded to groundcover. When nil it
	// defaults to slog.LevelInfo. Records below it are dropped by Enabled
	// before any allocation.
	Level slog.Leveler
}

// NewHandler returns a slog.Handler that forwards records to groundcover's log
// signal. Pass a standalone *logs.Client in Options.Client, or leave it nil to
// use the aggregate SDK initialized with Config.EnableLogs. The handler never
// blocks on I/O and never panics; delivery inherits the client's batching,
// retry, and overflow behavior.
func NewHandler(opts Options) slog.Handler {
	return &handler{opts: opts}
}

// handler implements slog.Handler. It is immutable: WithAttrs and WithGroup
// return copies, per the slog.Handler contract.
type handler struct {
	opts Options
	// attrs are pre-flattened attributes accumulated via WithAttrs, with group
	// prefixes already applied.
	attrs gclogs.Attributes
	// prefix is the dotted group path accumulated via WithGroup, including a
	// trailing dot when non-empty (e.g. "req.db.").
	prefix string
}

// Enabled reports whether records at the given level are forwarded.
func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		_ = safeguard.Do(func() { minLevel = h.opts.Level.Level() }, nil)
	}
	return level >= minLevel
}

// Handle forwards a record to groundcover. It always returns nil: delivery is
// asynchronous and failures surface through the SDK's self-metrics, never as
// logging errors.
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make(gclogs.Attributes, len(h.attrs)+r.NumAttrs())
	for k, v := range h.attrs {
		attrs[k] = v
	}
	r.Attrs(func(a slog.Attr) bool {
		flatten(attrs, h.prefix, a)
		return true
	})

	_ = safeguard.Do(func() {
		if h.opts.Client != nil {
			h.opts.Client.EmitLog(ctx, toLevel(r.Level), r.Message, attrs)
		} else {
			groundcover.EmitLog(ctx, groundcover.Level(toLevel(r.Level)), r.Message, groundcover.Attributes(attrs))
		}
	}, nil)
	return nil
}

// WithAttrs returns a copy of the handler with the attributes pre-flattened
// under the current group prefix.
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	next := h.clone()
	for _, a := range attrs {
		flatten(next.attrs, next.prefix, a)
	}
	return next
}

// WithGroup returns a copy of the handler with the group name appended to the
// dotted key prefix.
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := h.clone()
	next.prefix += name + "."
	return next
}

func (h *handler) clone() *handler {
	attrs := make(gclogs.Attributes, len(h.attrs)+4)
	for k, v := range h.attrs {
		attrs[k] = v
	}
	return &handler{opts: h.opts, attrs: attrs, prefix: h.prefix}
}

// flatten resolves a slog.Attr into dotted-key entries of out. Groups recurse
// with their name appended to the prefix; an empty-named group is inlined, and
// empty non-group attrs are ignored, both per slog conventions.
func flatten(out gclogs.Attributes, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		groupPrefix := prefix
		if a.Key != "" {
			groupPrefix += a.Key + "."
		}
		for _, ga := range v.Group() {
			flatten(out, groupPrefix, ga)
		}
		return
	}
	if a.Key == "" {
		return
	}
	out[prefix+a.Key] = v.Any()
}

// toLevel maps a slog level to a groundcover severity. Levels between the
// named slog constants map to the nearest lower severity; slog has no fatal,
// so anything at slog.LevelError+4 or above is treated as fatal.
func toLevel(l slog.Level) gclogs.Level {
	switch {
	case l < slog.LevelInfo:
		return gclogs.LevelDebug
	case l < slog.LevelWarn:
		return gclogs.LevelInfo
	case l < slog.LevelError:
		return gclogs.LevelWarning
	case l < slog.LevelError+4:
		return gclogs.LevelError
	default:
		return gclogs.LevelFatal
	}
}

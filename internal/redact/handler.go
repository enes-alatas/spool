package redact

import (
	"context"
	"fmt"
	"log/slog"
)

// Handler wraps next so no log line it writes can carry a known secret
// value: the message, every attribute, and the attributes carried by
// With/WithGroup all pass through the redactor first.
//
// Wrapping the handler rather than trusting call sites is the point. The leak
// #146 found was in an error string nobody wrote by hand — a transport error
// that happened to quote the URL it had failed on, token and all — so the
// defence has to sit where every line goes, not where a careful author puts
// it.
func Handler(next slog.Handler, r *Redactor) slog.Handler {
	return &handler{next: next, r: r}
}

type handler struct {
	next slog.Handler
	r    *Redactor
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.Text(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.attr(a)
	}
	return &handler{next: h.next.WithAttrs(clean), r: h.r}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(name), r: h.r}
}

// attr redacts one attribute, recursing into groups. A LogValuer is resolved
// first: the value the handler would print is the value that has to be clean,
// and a lazily-computed one is no exception.
func (h *handler) attr(a slog.Attr) slog.Attr {
	a.Key = h.r.Text(a.Key)
	a.Value = h.value(a.Value.Resolve())
	return a
}

func (h *handler) value(v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(h.r.Text(v.String()))
	case slog.KindGroup:
		attrs := v.Group()
		clean := make([]slog.Attr, len(attrs))
		for i, a := range attrs {
			clean[i] = h.attr(a)
		}
		return slog.GroupValue(clean...)
	case slog.KindAny:
		// An error or any other value the handler will format itself: the
		// secret is in the text it renders to, not in a string field this
		// code can reach. Render it, and substitute a string only when
		// redaction actually changed something — so ordinary values keep
		// their type, their formatting and their %+v detail, and only a
		// value that was about to print a secret is flattened.
		text := fmt.Sprintf("%+v", v.Any())
		if clean := h.r.Text(text); clean != text {
			return slog.StringValue(clean)
		}
		return v
	default:
		// Numbers, bools, times, durations: no room for a secret in them.
		return v
	}
}

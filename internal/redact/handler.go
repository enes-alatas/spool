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
func Handler(next slog.Handler, redactor *Redactor) slog.Handler {
	return &handler{next: next, redactor: redactor}
}

type handler struct {
	next     slog.Handler
	redactor *Redactor
}

func (logHandler *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return logHandler.next.Enabled(ctx, level)
}

func (logHandler *handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, logHandler.redactor.Text(rec.Message), rec.PC)
	rec.Attrs(func(attr slog.Attr) bool {
		out.AddAttrs(logHandler.attr(attr))
		return true
	})
	return logHandler.next.Handle(ctx, out)
}

func (logHandler *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		clean[i] = logHandler.attr(attr)
	}
	return &handler{next: logHandler.next.WithAttrs(clean), redactor: logHandler.redactor}
}

func (logHandler *handler) WithGroup(name string) slog.Handler {
	return &handler{next: logHandler.next.WithGroup(name), redactor: logHandler.redactor}
}

// attr redacts one attribute, recursing into groups. A LogValuer is resolved
// first: the value the handler would print is the value that has to be clean,
// and a lazily-computed one is no exception.
func (logHandler *handler) attr(attr slog.Attr) slog.Attr {
	attr.Key = logHandler.redactor.Text(attr.Key)
	attr.Value = logHandler.value(attr.Value.Resolve())
	return attr
}

func (logHandler *handler) value(value slog.Value) slog.Value {
	switch value.Kind() {
	case slog.KindString:
		return slog.StringValue(logHandler.redactor.Text(value.String()))
	case slog.KindGroup:
		attrs := value.Group()
		clean := make([]slog.Attr, len(attrs))
		for i, attr := range attrs {
			clean[i] = logHandler.attr(attr)
		}
		return slog.GroupValue(clean...)
	case slog.KindAny:
		// An error or any other value the handler will format itself: the
		// secret is in the text it renders to, not in a string field this
		// code can reach. Render it, and substitute a string only when
		// redaction actually changed something — so ordinary values keep
		// their type, their formatting and their %+v detail, and only a
		// value that was about to print a secret is flattened.
		text := fmt.Sprintf("%+v", value.Any())
		if clean := logHandler.redactor.Text(text); clean != text {
			return slog.StringValue(clean)
		}
		return value
	default:
		// Numbers, bools, times, durations: no room for a secret in them.
		return value
	}
}

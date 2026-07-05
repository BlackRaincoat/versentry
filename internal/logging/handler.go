package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
)

// TextHandler writes log lines as:
//
//	2006-01-02 15:04:05 INFO message key=value
//
// Attribute values are quoted only when they contain spaces or special characters.
type TextHandler struct {
	w     io.Writer
	level slog.Level
	mu    *sync.Mutex
	attrs []slog.Attr
	group string
}

// NewHandler returns a slog.Handler with the compact text format.
func NewHandler(w io.Writer, level slog.Level) *TextHandler {
	return &TextHandler{
		w:     w,
		level: level,
		mu:    &sync.Mutex{},
	}
}

// New creates a logger that writes compact text lines to w.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewHandler(w, level))
}

func (h *TextHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *TextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := *h
	nh.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &nh
}

func (h *TextHandler) WithGroup(name string) slog.Handler {
	nh := *h
	if h.group != "" {
		name = h.group + "." + name
	}
	nh.group = name
	return &nh
}

func (h *TextHandler) Handle(_ context.Context, r slog.Record) error {
	buf := make([]byte, 0, 256)
	if !r.Time.IsZero() {
		buf = append(buf, r.Time.Format("2006-01-02 15:04:05")...)
		buf = append(buf, ' ')
	}
	buf = append(buf, r.Level.String()...)
	buf = append(buf, ' ')
	buf = append(buf, r.Message...)

	for _, a := range h.attrs {
		buf = appendAttr(buf, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		buf = appendAttr(buf, h.group, a)
		return true
	})
	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf)
	return err
}

func appendAttr(buf []byte, group string, a slog.Attr) []byte {
	if a.Equal(slog.Attr{}) {
		return buf
	}
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		prefix := a.Key
		if group != "" {
			prefix = group + "." + prefix
		}
		for _, ga := range a.Value.Group() {
			buf = appendAttr(buf, prefix, ga)
		}
		return buf
	}

	key := a.Key
	if group != "" {
		key = group + "." + key
	}

	buf = append(buf, ' ')
	buf = append(buf, key...)
	buf = append(buf, '=')
	return appendValue(buf, a.Value)
}

func appendValue(buf []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		return appendUnquoted(buf, v.String())
	case slog.KindInt64:
		return strconv.AppendInt(buf, v.Int64(), 10)
	case slog.KindUint64:
		return strconv.AppendUint(buf, v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.AppendFloat(buf, v.Float64(), 'f', -1, 64)
	case slog.KindBool:
		return strconv.AppendBool(buf, v.Bool())
	case slog.KindDuration:
		return append(buf, v.Duration().String()...)
	case slog.KindTime:
		return append(buf, v.Time().Format("2006-01-02 15:04:05")...)
	default:
		return appendUnquoted(buf, v.String())
	}
}

func appendUnquoted(buf []byte, s string) []byte {
	if needsQuote(s) {
		return fmt.Appendf(buf, "%q", s)
	}
	return append(buf, s...)
}

func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r == ' ' || r == '=' || r == '"' || r < ' ' {
			return true
		}
	}
	return false
}

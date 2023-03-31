// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package slogtext implements a [slog.Handler] that is the same
// as [slog.TextHandler] except that it formats structured values
// as JSON instead of with `fmt.Sprintf("%+v")`, meaning that
// most log lines look the same, but data for structured values
// is retained in the log. See [Handler.Handle] for details.
package slogtext

import (
	"context"
	"encoding"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"unicode"
	"unicode/utf8"
)

func NewHandlerWithOpts(w io.Writer, opts slog.HandlerOptions) *Handler {
	return &Handler{
		w:    w,
		opts: opts,
	}
}

func NewHandler(w io.Writer) *Handler {
	return NewHandlerWithOpts(w, slog.HandlerOptions{})
}

// Enabled reports whether the handler handles records at the given level.
// The handler ignores records whose level is lower.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return h.enabled(level)
}

// WithAttrs returns a new Handler whose attributes consists
// of h's attributes followed by attrs.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.withAttrs(attrs)
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return h.withGroup(name)
}

// Handle formats its argument Record as a single line of space-separated
// key=value items.
//
// If the Record's time is zero, the time is omitted.
// Otherwise, the key is "time"
// and the value is output in RFC3339 format with millisecond precision.
//
// If the Record's level is zero, the level is omitted.
// Otherwise, the key is "level"
// and the value of [Level.String] is output.
//
// If the AddSource option is set and source information is available,
// the key is "source" and the value is output as FILE:LINE.
//
// The message's key is "msg".
//
// To modify these or other attributes, or remove them from the output, use
// [HandlerOptions.ReplaceAttr].
//
// If a value implements [encoding.TextMarshaler], the result of MarshalText is
// written.
//
// For values with a directly supported kind (all [slog.Kind] kinds except
// KindAny), the value is formatted as with [fmt.Sprint], with keys and values
// quoted with [strconv.Quote] if they contain Unicode space
// characters, non-printing characters, '"' or '='.
//
// For other value kinds, the key is formatted as above, and the value is
// formatted with [json.Marshal]. This is the key difference between this
// handler and [slog.TextHandler].
//
// Keys inside groups consist of components (keys or group names) separated by
// dots. No further escaping is performed.
// Thus there is no way to determine from the key "a.b.c" whether there
// are two groups "a" and "b" and a key "c", or a single group "a.b" and a key "c",
// or single group "a" and a key "b.c".
// If it is necessary to reconstruct the group structure of a key
// even in the presence of dots inside components, use
// [HandlerOptions.ReplaceAttr] to encode that information in the key.
//
// Each call to Handle results in a single serialized call to
// io.Writer.Write.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	return h.handle(r)
}

func appendTextValue(s *handleState, v slog.Value) error {
	switch v.Kind() {
	case slog.KindString:
		s.appendString(v.String())
	case slog.KindTime:
		s.appendTime(v.Time())
	case slog.KindAny, slog.KindLogValuer:
		x := v.Any()
		if tm, ok := x.(encoding.TextMarshaler); ok {
			data, err := tm.MarshalText()
			if err != nil {
				return err
			}
			// TODO: avoid the conversion to string.
			s.appendString(string(data))
			return nil
		}
		if bs, ok := byteSlice(x); ok {
			// As of Go 1.19, this only allocates for strings longer than 32 bytes.
			s.buf.WriteString(strconv.Quote(string(bs)))
			return nil
		}
		data, err := appendJSONMarshal(x, *s.buf)
		if err != nil {
			return err
		}
		*s.buf = data
	case slog.KindInt64:
		*s.buf = strconv.AppendInt(*s.buf, v.Int64(), 10)
	case slog.KindUint64:
		*s.buf = strconv.AppendUint(*s.buf, v.Uint64(), 10)
	case slog.KindFloat64:
		*s.buf = strconv.AppendFloat(*s.buf, v.Float64(), 'g', -1, 64)
	case slog.KindBool:
		*s.buf = strconv.AppendBool(*s.buf, v.Bool())
	case slog.KindDuration:
		*s.buf = append(*s.buf, v.Duration().String()...)
	case slog.KindGroup:
		*s.buf = fmt.Append(*s.buf, v.Group())
	default:
		panic(fmt.Sprintf("bad kind: %s", v.Kind()))
	}
	return nil
}

// byteSlice returns its argument as a []byte if the argument's
// underlying type is []byte, along with a second return value of true.
// Otherwise it returns nil, false.
func byteSlice(a any) ([]byte, bool) {
	if bs, ok := a.([]byte); ok {
		return bs, true
	}
	// Like Printf's %s, we allow both the slice type and the byte element type to be named.
	t := reflect.TypeOf(a)
	if t != nil && t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return reflect.ValueOf(a).Bytes(), true
	}
	return nil, false
}

func needsQuoting(s string) bool {
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			// Quote anything except a backslash that would need quoting in a
			// JSON string, as well as space and '='
			if b != '\\' && (b == ' ' || b == '=' || !safeSet[b]) {
				return true
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return true
		}
		i += size
	}
	return false
}

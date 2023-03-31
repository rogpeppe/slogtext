// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slogtext

import (
	"context"
	"log"
	"log/slog"
	"runtime"
	"time"

	"github.com/rogpeppe/slogtext/internal"
)

// handlerWriter is an io.Writer that calls a Handler.
// It is used to link the default log.Logger to the default slog.Logger.
type handlerWriter struct {
	h         Handler
	level     slog.Level
	capturePC bool
}

func (w *handlerWriter) Write(buf []byte) (int, error) {
	if !w.h.Enabled(context.Background(), w.level) {
		return 0, nil
	}
	var pc uintptr
	if !internal.IgnorePC && w.capturePC {
		// skip [runtime.Callers, w.Write, Logger.Output, log.Print]
		var pcs [1]uintptr
		runtime.Callers(4, pcs[:])
		pc = pcs[0]
	}

	// Remove final newline.
	origLen := len(buf) // Report that the entire buf was written.
	if len(buf) > 0 && buf[len(buf)-1] == '\n' {
		buf = buf[:len(buf)-1]
	}
	r := slog.NewRecord(time.Now(), w.level, string(buf), pc)
	return origLen, w.h.Handle(context.Background(), r)
}

// A Logger records structured information about each call to its
// Log, Debug, Info, Warn, and Error methods.
// For each call, it creates a Record and passes it to a Handler.
//
// To create a new Logger, call [New] or a Logger method
// that begins "With".
type Logger struct {
	handler Handler // for structured logging
}

func (l *Logger) clone() *Logger {
	c := *l
	return &c
}

// Handler returns l's Handler.
func (l *Logger) Handler() Handler { return l.handler }

// With returns a new Logger that includes the given arguments, converted to
// Attrs as in [Logger.Log] and resolved.
// The Attrs will be added to each output from the Logger.
// The new Logger shares the old Logger's context.
// The new Logger's handler is the result of calling WithAttrs on the receiver's
// handler.
func (l *Logger) With(args ...any) *Logger {
	var (
		attr  slog.Attr
		attrs []slog.Attr
	)
	for len(args) > 0 {
		attr, args = argsToAttr(args)
		attrs = append(attrs, attr)
	}
	c := l.clone()
	c.handler = l.handler.WithAttrs(attrs)
	return c
}

// WithGroup returns a new Logger that starts a group. The keys of all
// attributes added to the Logger will be qualified by the given name.
// (How that qualification happens depends on the [Handler.WithGroup]
// method of the Logger's Handler.)
// The new Logger shares the old Logger's context.
//
// The new Logger's handler is the result of calling WithGroup on the receiver's
// handler.
func (l *Logger) WithGroup(name string) *Logger {
	c := l.clone()
	c.handler = l.handler.WithGroup(name)
	return c

}

// New creates a new Logger with the given non-nil Handler and a nil context.
func New(h Handler) *Logger {
	if h == nil {
		panic("nil Handler")
	}
	return &Logger{handler: h}
}

// Enabled reports whether l emits log records at the given context and level.
func (l *Logger) Enabled(ctx context.Context, level slog.Level) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	return l.Handler().Enabled(ctx, level)
}

// NewLogLogger returns a new log.Logger such that each call to its Output method
// dispatches a Record to the specified handler. The logger acts as a bridge from
// the older log API to newer structured logging handlers.
func NewLogLogger(h Handler, level slog.Level) *log.Logger {
	return log.New(&handlerWriter{h, level, true}, "", 0)
}

// Log emits a log record with the current time and the given level and message.
// The Record's Attrs consist of the Logger's attributes followed by
// the Attrs specified by args.
//
// The attribute arguments are processed as follows:
//   - If an argument is an Attr, it is used as is.
//   - If an argument is a string and this is not the last argument,
//     the following argument is treated as the value and the two are combined
//     into an Attr.
//   - Otherwise, the argument is treated as a value with key "!BADKEY".
func (l *Logger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	l.log(ctx, level, msg, args...)
}

// LogAttrs is a more efficient version of [Logger.Log] that accepts only Attrs.
func (l *Logger) LogAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	l.logAttrs(ctx, level, msg, attrs...)
}

// Debug logs at LevelDebug.
func (l *Logger) Debug(msg string, args ...any) {
	l.log(nil, slog.LevelDebug, msg, args...)
}

// DebugCtx logs at LevelDebug with the given context.
func (l *Logger) DebugCtx(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelDebug, msg, args...)
}

// Info logs at LevelInfo.
func (l *Logger) Info(msg string, args ...any) {
	l.log(nil, slog.LevelInfo, msg, args...)
}

// InfoCtx logs at LevelInfo with the given context.
func (l *Logger) InfoCtx(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelInfo, msg, args...)
}

// Warn logs at LevelWarn.
func (l *Logger) Warn(msg string, args ...any) {
	l.log(nil, slog.LevelWarn, msg, args...)
}

// WarnCtx logs at LevelWarn with the given context.
func (l *Logger) WarnCtx(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelWarn, msg, args...)
}

// Error logs at LevelError.
func (l *Logger) Error(msg string, args ...any) {
	l.log(nil, slog.LevelError, msg, args...)
}

// ErrorCtx logs at LevelError with the given context.
func (l *Logger) ErrorCtx(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelError, msg, args...)
}

// log is the low-level logging method for methods that take ...any.
// It must always be called directly by an exported logging method
// or function, because it uses a fixed call depth to obtain the pc.
func (l *Logger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if !l.Enabled(ctx, level) {
		return
	}
	var pc uintptr
	if !internal.IgnorePC {
		var pcs [1]uintptr
		// skip [runtime.Callers, this function, this function's caller]
		runtime.Callers(3, pcs[:])
		pc = pcs[0]
	}
	r := slog.NewRecord(time.Now(), level, msg, pc)
	r.Add(args...)
	if ctx == nil {
		ctx = context.Background()
	}
	_ = l.Handler().Handle(ctx, r)
}

// logAttrs is like [Logger.log], but for methods that take ...Attr.
func (l *Logger) logAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	if !l.Enabled(ctx, level) {
		return
	}
	var pc uintptr
	if !internal.IgnorePC {
		var pcs [1]uintptr
		// skip [runtime.Callers, this function, this function's caller]
		runtime.Callers(3, pcs[:])
		pc = pcs[0]
	}
	r := slog.NewRecord(time.Now(), level, msg, pc)
	r.AddAttrs(attrs...)
	if ctx == nil {
		ctx = context.Background()
	}
	_ = l.Handler().Handle(ctx, r)
}

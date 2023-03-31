// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slogtext

import (
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"
)

// TextHandler is a Handler that writes Records to an io.Writer as a
// sequence of key=value pairs separated by spaces and followed by a newline.
type TextHandler struct {
	opts              slog.HandlerOptions
	preformattedAttrs []byte
	groupPrefix       string   // for text: prefix of groups opened in preformatting
	groups            []string // all groups started from WithGroup
	nOpenGroups       int      // the number of groups opened in preformattedAttrs
	mu                sync.Mutex
	w                 io.Writer
}

func (h *TextHandler) clone() *TextHandler {
	// We can't use assignment because we can't copy the mutex.
	return &TextHandler{
		opts:              h.opts,
		preformattedAttrs: slices.Clip(h.preformattedAttrs),
		groupPrefix:       h.groupPrefix,
		groups:            slices.Clip(h.groups),
		nOpenGroups:       h.nOpenGroups,
		w:                 h.w,
	}
}

// enabled reports whether l is greater than or equal to the
// minimum level.
func (h *TextHandler) enabled(l slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return l >= minLevel
}

func (h *TextHandler) withAttrs(as []slog.Attr) *TextHandler {
	h2 := h.clone()
	// Pre-format the attributes as an optimization.
	prefix := newBuffer()
	defer prefix.Free()
	prefix.WriteString(h.groupPrefix)
	state := h2.newHandleState((*buffer)(&h2.preformattedAttrs), false, "", prefix)
	defer state.free()
	state.openGroups()
	for _, a := range as {
		state.appendAttr(a)
	}
	// Remember the new prefix for later keys.
	h2.groupPrefix = state.prefix.String()
	// Remember how many opened groups are in preformattedAttrs,
	// so we don't open them again when we handle a Record.
	h2.nOpenGroups = len(h2.groups)
	return h2
}

func (h *TextHandler) withGroup(name string) *TextHandler {
	if name == "" {
		return h
	}
	h2 := h.clone()
	h2.groups = append(h2.groups, name)
	return h2
}

func (h *TextHandler) handle(r slog.Record) error {
	state := h.newHandleState(newBuffer(), true, "", nil)
	defer state.free()
	// Built-in attributes. They are not in a group.
	stateGroups := state.groups
	state.groups = nil // So ReplaceAttrs sees no groups instead of the pre groups.
	rep := h.opts.ReplaceAttr
	// time
	if !r.Time.IsZero() {
		key := slog.TimeKey
		val := r.Time.Round(0) // strip monotonic to match Attr behavior
		if rep == nil {
			state.appendKey(key)
			state.appendTime(val)
		} else {
			state.appendAttr(slog.Time(key, val))
		}
	}
	// level
	key := slog.LevelKey
	val := r.Level
	if rep == nil {
		state.appendKey(key)
		state.appendString(val.String())
	} else {
		state.appendAttr(slog.Any(key, val))
	}
	// source
	if h.opts.AddSource {
		frame := recordFrame(r)
		if frame.File != "" {
			key := slog.SourceKey
			if rep == nil {
				state.appendKey(key)
				state.appendSource(frame.File, frame.Line)
			} else {
				buf := newBuffer()
				buf.WriteString(frame.File) // TODO: escape?
				buf.WriteByte(':')
				buf.WritePosInt(frame.Line)
				s := buf.String()
				buf.Free()
				state.appendAttr(slog.String(key, s))
			}
		}
	}
	key = slog.MessageKey
	msg := r.Message
	if rep == nil {
		state.appendKey(key)
		state.appendString(msg)
	} else {
		state.appendAttr(slog.String(key, msg))
	}
	state.groups = stateGroups // Restore groups passed to ReplaceAttrs.
	state.appendNonBuiltIns(r)
	state.buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(*state.buf)
	return err
}

func recordFrame(r slog.Record) runtime.Frame {
	fs := runtime.CallersFrames([]uintptr{r.PC})
	f, _ := fs.Next()
	return f
}

func (s *handleState) appendNonBuiltIns(r slog.Record) {
	// preformatted Attrs
	if len(s.h.preformattedAttrs) > 0 {
		if len(*s.buf) > 0 {
			s.buf.WriteString(" ")
		}
		s.buf.Write(s.h.preformattedAttrs)
	}
	// Attrs in Record -- unlike the built-in ones, they are in groups started
	// from WithGroup.
	s.prefix = newBuffer()
	defer s.prefix.Free()
	s.prefix.WriteString(s.h.groupPrefix)
	s.openGroups()
	r.Attrs(func(a slog.Attr) {
		s.appendAttr(a)
	})
}

// handleState holds state for a single call to TextHandler.handle.
// The initial value of sep determines whether to emit a separator
// before the next key, after which it stays true.
type handleState struct {
	h       *TextHandler
	buf     *buffer
	freeBuf bool      // should buf be freed?
	prefix  *buffer   // for text: key prefix
	groups  *[]string // pool-allocated slice of active groups, for ReplaceAttr
}

var groupPool = sync.Pool{New: func() any {
	s := make([]string, 0, 10)
	return &s
}}

func (h *TextHandler) newHandleState(buf *buffer, freeBuf bool, sep string, prefix *buffer) handleState {
	s := handleState{
		h:       h,
		buf:     buf,
		freeBuf: freeBuf,
		prefix:  prefix,
	}
	if h.opts.ReplaceAttr != nil {
		s.groups = groupPool.Get().(*[]string)
		*s.groups = append(*s.groups, h.groups[:h.nOpenGroups]...)
	}
	return s
}

func (s *handleState) free() {
	if s.freeBuf {
		s.buf.Free()
	}
	if gs := s.groups; gs != nil {
		*gs = (*gs)[:0]
		groupPool.Put(gs)
	}
}

func (s *handleState) openGroups() {
	for _, n := range s.h.groups[s.h.nOpenGroups:] {
		s.openGroup(n)
	}
}

// Separator for group names and keys.
const keyComponentSep = '.'

// openGroup starts a new group of attributes
// with the given name.
func (s *handleState) openGroup(name string) {
	s.prefix.WriteString(name)
	s.prefix.WriteByte(keyComponentSep)
	// Collect group names for ReplaceAttr.
	if s.groups != nil {
		*s.groups = append(*s.groups, name)
	}

}

// closeGroup ends the group with the given name.
func (s *handleState) closeGroup(name string) {
	(*s.prefix) = (*s.prefix)[:len(*s.prefix)-len(name)-1 /* for keyComponentSep */]
	if s.groups != nil {
		*s.groups = (*s.groups)[:len(*s.groups)-1]
	}
}

// appendAttr appends the Attr's key and value using app.
// It handles replacement and checking for an empty key.
// after replacement).
func (s *handleState) appendAttr(a slog.Attr) {
	v := a.Value
	// Elide a non-group with an empty key.
	if a.Key == "" && v.Kind() != slog.KindGroup {
		return
	}
	if rep := s.h.opts.ReplaceAttr; rep != nil && v.Kind() != slog.KindGroup {
		var gs []string
		if s.groups != nil {
			gs = *s.groups
		}
		a = rep(gs, slog.Attr{a.Key, v})
		if a.Key == "" {
			return
		}
		// Although all attributes in the Record are already resolved,
		// This one came from the user, so it may not have been.
		v = a.Value.Resolve()
	}
	if v.Kind() == slog.KindGroup {
		attrs := v.Group()
		// Output only non-empty groups.
		if len(attrs) > 0 {
			// Inline a group with an empty key.
			if a.Key != "" {
				s.openGroup(a.Key)
			}
			for _, aa := range attrs {
				s.appendAttr(aa)
			}
			if a.Key != "" {
				s.closeGroup(a.Key)
			}
		}
	} else {
		s.appendKey(a.Key)
		s.appendValue(v)
	}
}

func (s *handleState) appendError(err error) {
	s.appendString(fmt.Sprintf("!ERROR:%v", err))
}

func (s *handleState) appendKey(key string) {
	if len(*s.buf) > 0 {
		s.buf.WriteString(" ")
	}
	if s.prefix != nil {
		// TODO: optimize by avoiding allocation.
		s.appendString(string(*s.prefix) + key)
	} else {
		s.appendString(key)
	}
	s.buf.WriteByte('=')
}

func (s *handleState) appendSource(file string, line int) {
	if needsQuoting(file) {
		s.appendString(file + ":" + strconv.Itoa(line))
	} else {
		// common case: no quoting needed.
		s.appendString(file)
		s.buf.WriteByte(':')
		s.buf.WritePosInt(line)
	}
}

func (s *handleState) appendString(str string) {
	if needsQuoting(str) {
		*s.buf = strconv.AppendQuote(*s.buf, str)
	} else {
		s.buf.WriteString(str)
	}
}

func (s *handleState) appendValue(v slog.Value) {
	if err := appendTextValue(s, v); err != nil {
		s.appendError(err)
	}
}

func (s *handleState) appendTime(t time.Time) {
	writeTimeRFC3339Millis(s.buf, t)
}

// This takes half the time of Time.AppendFormat.
func writeTimeRFC3339Millis(buf *buffer, t time.Time) {
	year, month, day := t.Date()
	buf.WritePosIntWidth(year, 4)
	buf.WriteByte('-')
	buf.WritePosIntWidth(int(month), 2)
	buf.WriteByte('-')
	buf.WritePosIntWidth(day, 2)
	buf.WriteByte('T')
	hour, min, sec := t.Clock()
	buf.WritePosIntWidth(hour, 2)
	buf.WriteByte(':')
	buf.WritePosIntWidth(min, 2)
	buf.WriteByte(':')
	buf.WritePosIntWidth(sec, 2)
	ns := t.Nanosecond()
	buf.WriteByte('.')
	buf.WritePosIntWidth(ns/1e6, 3)
	_, offsetSeconds := t.Zone()
	if offsetSeconds == 0 {
		buf.WriteByte('Z')
	} else {
		offsetMinutes := offsetSeconds / 60
		if offsetMinutes < 0 {
			buf.WriteByte('-')
			offsetMinutes = -offsetMinutes
		} else {
			buf.WriteByte('+')
		}
		buf.WritePosIntWidth(offsetMinutes/60, 2)
		buf.WriteByte(':')
		buf.WritePosIntWidth(offsetMinutes%60, 2)
	}
}

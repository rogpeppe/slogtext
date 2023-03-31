package slogtext

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/exp/slog"
	"strings"
	"testing"
)

func TestHandler1(t *testing.T) {
	ctx := context.Background()

	// ReplaceAttr functions

	// remove all Attrs
	removeAll := func(_ []string, a slog.Attr) slog.Attr { return slog.Attr{} }

	attrs := []slog.Attr{slog.String("a", "one"), slog.Int("b", 2), slog.Any("", "ignore me")}
	preAttrs := []slog.Attr{slog.Int("pre", 3), slog.String("x", "y")}

	for _, test := range []struct {
		name     string
		replace  func([]string, slog.Attr) slog.Attr
		with     func(slog.Handler) slog.Handler
		preAttrs []slog.Attr
		attrs    []slog.Attr
		wantText string
	}{
		{
			name:     "basic",
			attrs:    attrs,
			wantText: "time=2000-01-02T03:04:05.000Z level=INFO msg=message a=one b=2",
		},
		{
			name:     "cap keys",
			replace:  upperCaseKey,
			attrs:    attrs,
			wantText: "TIME=2000-01-02T03:04:05.000Z LEVEL=INFO MSG=message A=one B=2",
		},
		{
			name:     "remove all",
			replace:  removeAll,
			attrs:    attrs,
			wantText: "",
		},
		{
			name:     "preformatted",
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs) },
			preAttrs: preAttrs,
			attrs:    attrs,
			wantText: "time=2000-01-02T03:04:05.000Z level=INFO msg=message pre=3 x=y a=one b=2",
		},
		{
			name:     "preformatted cap keys",
			replace:  upperCaseKey,
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs) },
			preAttrs: preAttrs,
			attrs:    attrs,
			wantText: "TIME=2000-01-02T03:04:05.000Z LEVEL=INFO MSG=message PRE=3 X=y A=one B=2",
		},
		{
			name:     "preformatted remove all",
			replace:  removeAll,
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs) },
			preAttrs: preAttrs,
			attrs:    attrs,
			wantText: "",
		},
		{
			name:     "remove built-in",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey, slog.MessageKey),
			attrs:    attrs,
			wantText: "a=one b=2",
		},
		{
			name:     "preformatted remove built-in",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey, slog.MessageKey),
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs) },
			attrs:    attrs,
			wantText: "pre=3 x=y a=one b=2",
		},
		{
			name:    "groups",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.Int("a", 1),
				slog.Group("g",
					slog.Int("b", 2),
					slog.Group("h", slog.Int("c", 3)),
					slog.Int("d", 4)),
				slog.Int("e", 5),
			},
			wantText: "msg=message a=1 g.b=2 g.h.c=3 g.d=4 e=5",
		},
		{
			name:     "empty group",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			attrs:    []slog.Attr{slog.Group("g"), slog.Group("h", slog.Int("a", 1))},
			wantText: "msg=message h.a=1",
		},
		{
			name:    "escapes",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.String("a b", "x\t\n\000y"),
				slog.Group(" b.c=\"\\x2E\t",
					slog.String("d=e", "f.g\""),
					slog.Int("m.d", 1)), // dot is not escaped
			},
			wantText: `msg=message "a b"="x\t\n\x00y" " b.c=\"\\x2E\t.d=e"="f.g\"" " b.c=\"\\x2E\t.m.d"=1`,
		},
		{
			name:    "LogValuer",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.Int("a", 1),
				slog.Any("name", logValueName{"Ren", "Hoek"}),
				slog.Int("b", 2),
			},
			wantText: "msg=message a=1 name.first=Ren name.last=Hoek b=2",
		},
		{
			name:     "with-group",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs).WithGroup("s") },
			attrs:    attrs,
			wantText: "msg=message pre=3 x=y s.a=one s.b=2",
		},
		{
			name:    "preformatted with-groups",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			with: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.Int("p1", 1)}).
					WithGroup("s1").
					WithAttrs([]slog.Attr{slog.Int("p2", 2)}).
					WithGroup("s2")
			},
			attrs:    attrs,
			wantText: "msg=message p1=1 s1.p2=2 s1.s2.a=one s1.s2.b=2",
		},
		{
			name:    "two with-groups",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			with: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.Int("p1", 1)}).
					WithGroup("s1").
					WithGroup("s2")
			},
			attrs:    attrs,
			wantText: "msg=message p1=1 s1.s2.a=one s1.s2.b=2",
		},
		{
			name:     "GroupValue as Attr value",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			attrs:    []slog.Attr{{"v", slog.AnyValue(slog.IntValue(3))}},
			wantText: "msg=message v=3",
		},
		{
			name:     "byte slice",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			attrs:    []slog.Attr{slog.Any("bs", []byte{1, 2, 3, 4})},
			wantText: `msg=message bs="\x01\x02\x03\x04"`,
		},
		{
			name:     "json.RawMessage",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			attrs:    []slog.Attr{slog.Any("bs", json.RawMessage([]byte("1234")))},
			wantText: `msg=message bs="1234"`,
		},
		{
			name:    "inline group",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.Int("a", 1),
				slog.Group("", slog.Int("b", 2), slog.Int("c", 3)),
				slog.Int("d", 4),
			},
			wantText: `msg=message a=1 b=2 c=3 d=4`,
		},
	} {
		r := slog.NewRecord(testTime, slog.LevelInfo, "message", 1)
		r.AddAttrs(test.attrs...)
		var buf bytes.Buffer
		opts := slog.HandlerOptions{ReplaceAttr: test.replace}
		t.Run(test.name, func(t *testing.T) {
			h := slog.Handler(NewHandlerWithOpts(&buf, opts))
			if test.with != nil {
				h = test.with(h)
			}
			buf.Reset()
			if err := h.Handle(ctx, r); err != nil {
				t.Fatal(err)
			}
			got := strings.TrimSuffix(buf.String(), "\n")
			if got != test.wantText {
				t.Errorf("\ngot  %s\nwant %s\n", got, test.wantText)
			}
		})
	}
}

// removeKeys returns a function suitable for HandlerOptions.ReplaceAttr
// that removes all Attrs with the given keys.
func removeKeys(keys ...string) func([]string, slog.Attr) slog.Attr {
	return func(_ []string, a slog.Attr) slog.Attr {
		for _, k := range keys {
			if a.Key == k {
				return slog.Attr{}
			}
		}
		return a
	}
}

func upperCaseKey(_ []string, a slog.Attr) slog.Attr {
	a.Key = strings.ToUpper(a.Key)
	return a
}

type logValueName struct {
	first, last string
}

func (n logValueName) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("first", n.first),
		slog.String("last", n.last))
}

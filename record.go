// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slogtext

import "log/slog"

const badKey = "!BADKEY"

// argsToAttr turns a prefix of the nonempty args slice into an Attr
// and returns the unconsumed portion of the slice.
// If args[0] is an Attr, it returns it, resolved.
// If args[0] is a string, it treats the first two elements as
// a key-value pair.
// Otherwise, it treats args[0] as a value with a missing key.
func argsToAttr(args []any) (slog.Attr, []any) {
	switch x := args[0].(type) {
	case string:
		if len(args) == 1 {
			return slog.String(badKey, x), nil
		}
		a := slog.Any(x, args[1])
		a.Value = a.Value.Resolve()
		return a, args[2:]

	case slog.Attr:
		x.Value = x.Value.Resolve()
		return x, args[1:]

	default:
		return slog.Any(badKey, x), args[1:]
	}
}

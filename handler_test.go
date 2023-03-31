package slogtext

import (
	"log/slog"
	"strings"
)

func upperCaseKey(_ []string, a slog.Attr) slog.Attr {
	a.Key = strings.ToUpper(a.Key)
	return a
}

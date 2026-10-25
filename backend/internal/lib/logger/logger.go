// Package logger builds LockLens' structured loggers (log/slog). Logs never
// go to stdout, which belongs to reports.
package logger

import (
	"io"
	"log/slog"
)

// New returns a logger writing to w: JSON lines when format is "json", text
// otherwise.
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// Level parses a level name (debug, info, warn, error); anything else is info.
func Level(name string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(name)); err != nil {
		return slog.LevelInfo
	}
	return l
}

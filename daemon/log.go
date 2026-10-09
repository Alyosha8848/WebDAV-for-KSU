package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Levelled logging with an in-memory ring buffer and live subscribers.
//
// The App's log tab needs five levels (trace/debug/info/warn/error) without
// shelling out to `logcat`, so every component writes through this logger and
// the control API reads it back.
// ---------------------------------------------------------------------------

const (
	LevelTrace = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = []string{"trace", "debug", "info", "warn", "error"}

// ParseLevel maps a level name to its numeric value. Unknown names fall back to info.
func ParseLevel(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	case "info", "":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	}
	return LevelInfo
}

// LevelName renders a numeric level back to its canonical name.
func LevelName(l int) string {
	if l < 0 {
		l = 0
	}
	if l >= len(levelNames) {
		l = len(levelNames) - 1
	}
	return levelNames[l]
}

// LogEntry is one line in the log, as exposed over the API.
type LogEntry struct {
	TS    string `json:"ts"`
	Level string `json:"level"`
	Src   string `json:"src"`
	Msg   string `json:"msg"`
}

// msTime keeps millisecond precision (seconds are too coarse when the App tails
// the log every two seconds).
const msTime = "2006-01-02T15:04:05.000-07:00"

// Logger is a small levelled logger: a ring buffer for the API, an optional
// rotating file, an optional stderr mirror, and live subscribers.
type Logger struct {
	mu        sync.Mutex
	level     int
	ring      []LogEntry
	ringCap   int
	file      *os.File
	filePath  string
	fileBytes int64
	maxBytes  int64
	mirror    bool
	subs      map[int]chan LogEntry
	nextSub   int
}

// NewLogger creates a logger with a ring buffer of ringCap entries.
func NewLogger(ringCap int) *Logger {
	if ringCap <= 0 {
		ringCap = 2000
	}
	return &Logger{
		level:    LevelInfo,
		ringCap:  ringCap,
		maxBytes: 1 << 20, // 1 MiB, then a single .1 rotation
		mirror:   true,
		subs:     make(map[int]chan LogEntry),
	}
}

// SetLevel changes the minimum level that is recorded.
func (l *Logger) SetLevel(level int) {
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
}

// Level returns the current minimum level.
func (l *Logger) Level() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level
}

// OpenFile starts mirroring log lines into path (creating parent directories).
func (l *Logger) OpenFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	l.mu.Lock()
	if l.file != nil {
		l.file.Close()
	}
	l.file = f
	l.filePath = path
	if st != nil {
		l.fileBytes = st.Size()
	}
	l.mu.Unlock()
	return nil
}

// SetMirror controls whether lines are also written to stderr. On Android the
// daemon's stderr is captured by the module's boot log, which is the only way to
// debug a crash that happens before the control socket exists.
func (l *Logger) SetMirror(on bool) {
	l.mu.Lock()
	l.mirror = on
	l.mu.Unlock()
}

func (l *Logger) log(level int, src, msg string) {
	now := time.Now()
	entry := LogEntry{TS: now.Format(msTime), Level: LevelName(level), Src: src, Msg: msg}

	l.mu.Lock()
	if level < l.level {
		l.mu.Unlock()
		return
	}
	l.ring = append(l.ring, entry)
	if len(l.ring) > l.ringCap {
		// Copy the tail so the backing array can be released.
		tail := make([]LogEntry, l.ringCap)
		copy(tail, l.ring[len(l.ring)-l.ringCap:])
		l.ring = tail
	}
	subs := make([]chan LogEntry, 0, len(l.subs))
	for _, ch := range l.subs {
		subs = append(subs, ch)
	}
	mirror := l.mirror
	line := fmt.Sprintf("%s %-5s [%s] %s\n", entry.TS, strings.ToUpper(entry.Level), entry.Src, entry.Msg)
	if l.file != nil {
		if l.fileBytes > l.maxBytes {
			l.rotateLocked()
		}
		if n, err := l.file.WriteString(line); err == nil {
			l.fileBytes += int64(n)
		}
	}
	l.mu.Unlock()

	if mirror {
		_, _ = io.WriteString(os.Stderr, line)
	}
	for _, ch := range subs {
		select {
		case ch <- entry:
		default: // never block a request path on a slow subscriber
		}
	}
}

// rotateLocked renames the log file to <path>.1 and starts a new one.
// Caller must hold l.mu.
func (l *Logger) rotateLocked() {
	if l.file == nil {
		return
	}
	name := l.file.Name()
	l.file.Close()
	l.file = nil
	_ = os.Rename(name, name+".1")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	l.file = f
	l.fileBytes = 0
}

// Tracef logs at trace level (most verbose).
func (l *Logger) Tracef(src, format string, args ...any) {
	l.log(LevelTrace, src, fmt.Sprintf(format, args...))
}

// Debugf logs at debug level.
func (l *Logger) Debugf(src, format string, args ...any) {
	l.log(LevelDebug, src, fmt.Sprintf(format, args...))
}

// Infof logs at info level.
func (l *Logger) Infof(src, format string, args ...any) {
	l.log(LevelInfo, src, fmt.Sprintf(format, args...))
}

// Warnf logs at warn level.
func (l *Logger) Warnf(src, format string, args ...any) {
	l.log(LevelWarn, src, fmt.Sprintf(format, args...))
}

// Errorf logs at error level.
func (l *Logger) Errorf(src, format string, args ...any) {
	l.log(LevelError, src, fmt.Sprintf(format, args...))
}

// Entries returns up to limit entries at or above minLevel, newest last.
// If after is non-zero only entries strictly newer are returned.
func (l *Logger) Entries(minLevel int, limit int, after time.Time) []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, 0, 64)
	for _, e := range l.ring {
		if ParseLevel(e.Level) < minLevel {
			continue
		}
		if !after.IsZero() {
			ts, err := time.Parse(msTime, e.TS)
			if err == nil && !ts.After(after) {
				continue
			}
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Subscribe returns a channel of live entries plus an unsubscribe function.
// Slow consumers drop entries rather than stalling request handling.
func (l *Logger) Subscribe() (<-chan LogEntry, func()) {
	ch := make(chan LogEntry, 256)
	l.mu.Lock()
	id := l.nextSub
	l.nextSub++
	l.subs[id] = ch
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		if c, ok := l.subs[id]; ok {
			delete(l.subs, id)
			close(c)
		}
		l.mu.Unlock()
	}
}

// classifyExternalLevel guesses a level from a child process log line so that
// dufs/tailscaled output is filterable in the App instead of all landing on info.
func classifyExternalLevel(line string) int {
	u := strings.ToUpper(line)
	switch {
	case strings.Contains(u, "ERROR") || strings.Contains(u, "FATAL") || strings.Contains(u, "PANIC"):
		return LevelError
	case strings.Contains(u, "WARN"):
		return LevelWarn
	case strings.Contains(u, "DEBUG") || strings.Contains(u, "TRACE"):
		return LevelDebug
	}
	return LevelInfo
}

// sortedLevelNames is handy for validation error messages.
func sortedLevelNames() string {
	names := append([]string(nil), levelNames...)
	sort.Strings(names)
	return strings.Join(names, ", ")
}

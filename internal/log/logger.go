package log

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

var levelNames = map[Level]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
}

func (l Level) String() string {
	if name, ok := levelNames[l]; ok {
		return name
	}
	return "?"
}

type Logger struct {
	level     Level
	debugFile *os.File
	mu        sync.Mutex
}

var global = &Logger{level: WARN}

func Init() {
	level := WARN
	switch strings.ToLower(os.Getenv("MALDER_LOG_LEVEL")) {
	case "debug":
		level = DEBUG
	case "info":
		level = INFO
	case "warn", "warning":
		level = WARN
	case "error":
		level = ERROR
	}

	debugPath := os.Getenv("MALDER_DEBUG_FILE")
	if debugPath == "" {
		debugPath = "malder_debug.log"
	}

	var debugFile *os.File
	if level == DEBUG {
		f, err := os.OpenFile(debugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			debugFile = f
		}
	}

	global.mu.Lock()
	global.level = level
	global.debugFile = debugFile
	global.mu.Unlock()
}

func (l *Logger) log(level Level, format string, args ...any) {
	if level < l.level {
		return
	}
	msg := fmt.Sprintf("[%s] %s", level, fmt.Sprintf(format, args...))
	if level == DEBUG {
		l.mu.Lock()
		w := l.debugFile
		l.mu.Unlock()
		if w != nil {
			fmt.Fprintf(w, "%s %s\n", time.Now().Format(time.RFC3339), msg)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().Format("15:04:05.000"), msg)
}

func Debug(format string, args ...any) { global.log(DEBUG, format, args...) }
func Info(format string, args ...any)  { global.log(INFO, format, args...) }
func Warn(format string, args ...any)  { global.log(WARN, format, args...) }
func Error(format string, args ...any) { global.log(ERROR, format, args...) }

// Recover catches panics in goroutines, logs the error and stack trace.
// Usage: defer log.Recover("goroutine name")
func Recover(context string) {
	if r := recover(); r != nil {
		Error("PANIC [%s]: %v\n%s", context, r, debug.Stack())
	}
}

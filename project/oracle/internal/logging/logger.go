package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Module    string    `json:"module"`
	Data      any       `json:"data,omitempty"`
}

type Logger struct {
	mu     sync.Mutex
	level  LogLevel
	output io.Writer
	module string
}

func NewLogger(level LogLevel, output io.Writer, module string) *Logger {
	if output == nil {
		output = os.Stdout
	}
	return &Logger{
		level:  level,
		output: output,
		module: module,
	}
}

func (l *Logger) log(level LogLevel, msg string, data any) {
	if level < l.level {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level.String(),
		Message:   msg,
		Module:    l.module,
		Data:      data,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	bytes, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling log entry: %v\n", err)
		return
	}

	l.output.Write(append(bytes, '\n'))
}

func (l *Logger) Debug(msg string, data ...any) {
	var logData any
	if len(data) > 0 {
		logData = data[0]
	}
	l.log(DEBUG, msg, logData)
}

func (l *Logger) Info(msg string, data ...any) {
	var logData any
	if len(data) > 0 {
		logData = data[0]
	}
	l.log(INFO, msg, logData)
}

func (l *Logger) Warn(msg string, data ...any) {
	var logData any
	if len(data) > 0 {
		logData = data[0]
	}
	l.log(WARN, msg, logData)
}

func (l *Logger) Error(msg string, data ...any) {
	var logData any
	if len(data) > 0 {
		logData = data[0]
	}
	l.log(ERROR, msg, logData)
}

func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

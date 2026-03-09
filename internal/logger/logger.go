package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

// Logger escribe logs a archivo y consola
type Logger struct {
	file    *os.File
	console *log.Logger
	fileLog *log.Logger
}

// New crea un nuevo logger
func New(path string) *Logger {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("No se pudo abrir log file: %v", err)
	}

	return &Logger{
		file:    f,
		console: log.New(os.Stdout, "", 0),
		fileLog: log.New(f, "", 0),
	}
}

func (l *Logger) format(level, msg string) string {
	return fmt.Sprintf("[%s] [%s] %s", time.Now().Format("2006-01-02 15:04:05"), level, msg)
}

func (l *Logger) Info(msg string) {
	line := l.format("INFO ", msg)
	l.console.Println(line)
	l.fileLog.Println(line)
}

func (l *Logger) Warn(msg string) {
	line := l.format("WARN ", msg)
	l.console.Println(line)
	l.fileLog.Println(line)
}

func (l *Logger) Error(msg string) {
	line := l.format("ERROR", msg)
	l.console.Println(line)
	l.fileLog.Println(line)
}

func (l *Logger) Request(userID int64, username, text string) {
	line := l.format("REQ  ", fmt.Sprintf("User=%d (@%s) Msg=%q", userID, username, truncate(text, 80)))
	l.console.Println(line)
	l.fileLog.Println(line)
}

func (l *Logger) Admin(adminUser, action string) {
	line := l.format("ADMIN", fmt.Sprintf("By=%s Action=%s", adminUser, action))
	l.console.Println(line)
	l.fileLog.Println(line)
}

func (l *Logger) Auth(chatID int64, key, result string) {
	line := l.format("AUTH ", fmt.Sprintf("ChatID=%d Key=%s Result=%s", chatID, truncate(key, 20), result))
	l.console.Println(line)
	l.fileLog.Println(line)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
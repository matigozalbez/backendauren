package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Type      string `json:"type"`
	Message   string `json:"message"`
}

var (
	logMutex sync.Mutex
)

func WriteLog(level, logType, message string) error {
	logMutex.Lock()
	defer logMutex.Unlock()

	logDir := "logs"

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	logPath := filepath.Join(logDir, "app.json.log")

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Type:      logType,
		Message:   message,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(
		logPath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(append(data, '\n'))
	return err
}
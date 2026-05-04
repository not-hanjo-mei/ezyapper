package web

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
)

// LogsHandler returns an http.HandlerFunc for GET /logs that renders the
// log viewer page. Query param ?lines=N controls line count.
func LogsHandler(logFilePath string, cfgStore *atomic.Value, ts *TemplateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		cfg, ok := cfgStore.Load().(*config.Config)
		if !ok {
			http.Error(w, "Internal configuration error", http.StatusInternalServerError)
			return
		}
		lines := cfg.Web.LogDefaultLines
		if linesStr := r.URL.Query().Get("lines"); linesStr != "" {
			if parsed, err := strconv.Atoi(linesStr); err == nil && parsed > 0 {
				lines = parsed
				if lines > cfg.Web.LogMaxLines {
					logger.Warnf("[logs] requested lines=%d exceeds max=%d, capping", lines, cfg.Web.LogMaxLines)
					lines = cfg.Web.LogMaxLines
				}
			}
		}

		logContent, totalLines, err := readLogTail(logFilePath, lines, cfg.Web.LogMaxReadBytes)
		var stats, displayContent string
		if err != nil {
			displayContent = err.Error()
			stats = "Log file not available"
		} else {
			displayContent = logContent
			stats = "Showing last " + strconv.Itoa(lines) + " lines (of " + strconv.Itoa(totalLines) + ")"
		}

		data := map[string]any{
			"Lines":   lines,
			"Content": displayContent,
			"Stats":   stats,
		}

		renderStandardPage(w, r, ts, "logs", data)
	}
}

func readLogTail(filePath string, n int, maxReadSize int) (string, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, errFileNotFound
		}
		return "", 0, errReadFailed
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", 0, errReadFailed
	}

	fileSize := info.Size()
	if fileSize == 0 {
		return "", 0, nil
	}

	readOffset := fileSize - int64(maxReadSize)
	if readOffset < 0 {
		readOffset = 0
	}

	buf := make([]byte, fileSize-readOffset)
	_, err = file.ReadAt(buf, readOffset)
	if err != nil && err != io.EOF {
		return "", 0, errReadFailed
	}

	content := string(buf)
	allLines := strings.Split(content, "\n")
	totalLines := len(allLines)

	if len(allLines) > n {
		allLines = allLines[len(allLines)-n:]
	}

	return strings.Join(allLines, "\n"), totalLines, nil
}

var (
	errFileNotFound = &logError{"Log file not found"}
	errReadFailed   = &logError{"Unable to read logs"}
)

type logError struct {
	msg string
}

func (e *logError) Error() string { return e.msg }

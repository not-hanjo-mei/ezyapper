package web

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	defaultLogLines = 200
	maxLogLines     = 5000
	minLogLines     = 10
	maxReadSize     = 1 * 1024 * 1024
)

// LogsHandler returns an http.HandlerFunc for GET /logs that renders the
// log viewer page. Query param ?lines=N controls line count (default 200,
// max 5000, min 10). Reads from the end of the file, capping to the last 1MB.
func LogsHandler(logFilePath string, ts *TemplateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		csrfToken := CSRFTokenFromContext(ctx)

		lines := defaultLogLines
		if linesStr := r.URL.Query().Get("lines"); linesStr != "" {
			if parsed, err := strconv.Atoi(linesStr); err == nil && parsed > 0 {
				lines = parsed
				if lines > maxLogLines {
					lines = maxLogLines
				}
			}
		}

		logContent, totalLines, err := readLogTail(logFilePath, lines)
		var stats, displayContent string
		if err != nil {
			displayContent = err.Error()
			stats = "Log file not available"
		} else {
			displayContent = logContent
			stats = "Showing last " + strconv.Itoa(lines) + " lines (of " + strconv.Itoa(totalLines) + ")"
		}

		navItems := []NavItem{
			{Label: "Dashboard", Href: "/", Icon: "dashboard"},
			{Label: "Configuration", Href: "/config", Icon: "settings"},
			{Label: "Memories", Href: "/memories", Icon: "memory"},
			{Label: "Profiles", Href: "/profiles", Icon: "person"},
			{Label: "Channels", Href: "/channels", Icon: "forum"},
			{Label: "Plugins", Href: "/plugins", Icon: "extension"},
			{Label: "Logs", Href: "/logs", Icon: "description", Active: true},
		}

		data := map[string]any{
			"Lines":   lines,
			"Content": displayContent,
			"Stats":   stats,
		}

		RenderPage(w, ts, "logs", &PageData{
			Title:     "Logs",
			ActiveNav: "logs",
			CSRFToken: csrfToken,
			Data:      data,
			NavItems:  navItems,
		})
	}
}

func readLogTail(filePath string, n int) (string, int, error) {
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

	readOffset := fileSize - maxReadSize
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

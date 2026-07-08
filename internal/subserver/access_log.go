package subserver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

const (
	accessLogQueueSize  = 1024
	accessLogTimeLayout = "2006-01-02T15:04:05.000Z0700"
)

// AccessLogger writes subscription endpoint request summaries to a separate file.
type AccessLogger struct {
	writer *asyncAccessLogWriter
}

// asyncAccessLogWriter handles asynchronous writing to the access log file.
type asyncAccessLogWriter struct {
	noCopy     noCopy
	path       string
	file       *os.File
	queue      chan []byte
	done       chan struct{}
	wg         sync.WaitGroup
	mu         sync.Mutex
	closed     bool
	fileClosed bool
	drops      atomic.Uint64
	warned     atomic.Bool
}

// NewAccessLogger creates an access logger. An empty path disables logging.
func NewAccessLogger(path string) (*AccessLogger, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return &AccessLogger{}, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create access log directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open access log file: %w", err)
	}

	writer := newAsyncAccessLogWriter(path, file)
	return &AccessLogger{writer: writer}, nil
}

// Enabled reports whether access logging is active.
func (l *AccessLogger) Enabled() bool {
	return l != nil && l.writer != nil
}

// Log writes one subscription request record as a space-separated line,
// including fetch statistics if available.
func (l *AccessLogger) Log(r *http.Request, statusCode int, clientIP string, success, total int) {
	if l == nil || l.writer == nil {
		return
	}

	var b strings.Builder
	// Write standard HTTP request information
	appendAccessLogPart(&b, time.Now().UTC().Format(accessLogTimeLayout))
	appendAccessLogPart(&b, r.Method)
	appendAccessLogPart(&b, r.URL.RequestURI())
	appendAccessLogPart(&b, strconv.Itoa(statusCode))

	// Write upstream fetch statistics: total sources / successful fetches
	if total > 0 {
		appendAccessLogPart(&b, fmt.Sprintf("%d/%d", total, success))
	} else {
		appendAccessLogPart(&b, "-")
	}

	// Write client metadata
	appendAccessLogPart(&b, sanitizeAccessLogValue(clientIP))
	appendAccessLogPart(&b, sanitizeAccessLogValue(r.Header.Get("X-Hwid")))
	appendAccessLogPart(&b, sanitizeAccessLogValue(r.Header.Get("X-Device-Os")))
	appendAccessLogPart(&b, sanitizeAccessLogValue(r.Header.Get("X-Ver-Os")))
	appendAccessLogPart(&b, sanitizeAccessLogValue(r.Header.Get("X-Device-Model")))
	appendAccessLogPart(&b, sanitizeAccessLogValue(r.Header.Get("User-Agent")))

	b.WriteByte('\n')

	l.writer.Write([]byte(b.String()))
}

// appendAccessLogPart joins parts with a space, wrapping values containing spaces in quotes.
func appendAccessLogPart(b *strings.Builder, value string) {
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	if strings.Contains(value, " ") {
		b.WriteByte('"')
		b.WriteString(value)
		b.WriteByte('"')
	} else {
		b.WriteString(value)
	}
}

// Close flushes pending records and closes the access log file.
func (l *AccessLogger) Close() error {
	return l.CloseWithContext(context.Background())
}

// CloseWithContext flushes pending records with a deadline and closes the file.
func (l *AccessLogger) CloseWithContext(ctx context.Context) error {
	if l == nil || l.writer == nil {
		return nil
	}
	return l.writer.Sync(ctx)
}

func newAsyncAccessLogWriter(path string, file *os.File) *asyncAccessLogWriter {
	writer := &asyncAccessLogWriter{
		path:  path,
		file:  file,
		queue: make(chan []byte, accessLogQueueSize),
		done:  make(chan struct{}),
	}
	writer.wg.Add(1)
	go writer.run()
	return writer
}

// Write enqueues log data to be written asynchronously.
func (w *asyncAccessLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return len(p), nil
	}

	data := append([]byte(nil), p...)
	select {
	case w.queue <- data:
	default:
		w.drops.Add(1)
	}

	return len(p), nil
}

// Sync flushes the queue, waits for completion, and closes the file.
func (w *asyncAccessLogWriter) Sync(ctx context.Context) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	close(w.queue)
	w.mu.Unlock()

	select {
	case <-ctx.Done():
		w.mu.Lock()
		if w.file != nil && !w.fileClosed {
			_ = w.file.Close()
			w.fileClosed = true
		}
		w.mu.Unlock()
		<-w.done
		w.logDroppedRecords()
		return ctx.Err()
	case <-w.done:
	}

	err := w.closeFile()
	w.logDroppedRecords()
	return err
}

func (w *asyncAccessLogWriter) closeFile() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil || w.fileClosed {
		return nil
	}

	err := w.file.Close()
	w.fileClosed = true
	if err != nil {
		return fmt.Errorf("close access log file: %w", err)
	}
	return nil
}

func (w *asyncAccessLogWriter) logDroppedRecords() {
	if dropped := w.drops.Swap(0); dropped > 0 {
		logger.Warn("Subserver access log dropped records",
			zap.Uint64("dropped", dropped),
			zap.String("path", w.path))
	}
}

func (w *asyncAccessLogWriter) run() {
	defer w.wg.Done()
	defer close(w.done)

	for data := range w.queue {
		if _, err := w.file.Write(data); err != nil && w.warned.CompareAndSwap(false, true) {
			logger.Warn("Subserver access log write failed",
				zap.String("path", w.path),
				zap.Error(err))
		}
	}
}

// sanitizeAccessLogValue cleans values by removing carriage returns, newlines, and tabs,
// and trimming surrounding whitespace, suitable for log formatting.
func sanitizeAccessLogValue(value string) string {
	if value == "" {
		return ""
	}

	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "\"", " ")
	return strings.TrimSpace(value)
}

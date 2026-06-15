package subserver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const accessLogQueueSize = 1024

// AccessLogger writes subscription endpoint request summaries to a separate file.
type AccessLogger struct {
	writer *asyncAccessLogWriter
	log    *zap.Logger
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

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open access log file: %w", err)
	}

	writer := newAsyncAccessLogWriter(path, file)
	encoder := zapcore.NewConsoleEncoder(logger.NewConsoleEncoderConfig())
	core := zapcore.NewCore(encoder, zapcore.AddSync(writer), zapcore.InfoLevel)

	return &AccessLogger{
		writer: writer,
		log:    zap.New(core, zap.AddCaller(), zap.AddCallerSkip(2)),
	}, nil
}

// Enabled reports whether access logging is active.
func (l *AccessLogger) Enabled() bool {
	return l != nil && l.writer != nil
}

// Log writes one subscription request record.
func (l *AccessLogger) Log(r *http.Request, statusCode int, clientIP string) {
	if l == nil || l.log == nil {
		return
	}

	l.log.Info("SUBSERVER_ACCESS",
		zap.String("method", r.Method),
		zap.Int("status_code", statusCode),
		zap.String("url", r.URL.RequestURI()),
		zap.String("ip", sanitizeAccessLogValue(clientIP)),
		zap.String("x_hwid", sanitizeAccessLogValue(r.Header.Get("X-HWID"))),
		zap.String("x_device_os", sanitizeAccessLogValue(r.Header.Get("X-Device-Os"))),
		zap.String("x_ver_os", sanitizeAccessLogValue(r.Header.Get("X-Ver-Os"))),
		zap.String("x_device_model", sanitizeAccessLogValue(r.Header.Get("X-Device-Model"))),
		zap.String("user_agent", sanitizeAccessLogValue(r.Header.Get("User-Agent"))),
	)
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

type asyncAccessLogWriter struct {
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

func (w *asyncAccessLogWriter) Sync(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	close(w.queue)
	w.mu.Unlock()

	select {
	case <-w.done:
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

func sanitizeAccessLogValue(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	return strings.TrimSpace(value)
}

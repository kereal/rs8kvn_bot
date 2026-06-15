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
	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

const (
	accessLogQueueSize  = 1024
	accessLogTimeLayout = "2006-01-02T15:04:05.000Z0700"
)

var accessLogBufferPool = buffer.NewPool()

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
	encoder := newAccessLogEncoder()
	core := zapcore.NewCore(encoder, zapcore.AddSync(writer), zapcore.InfoLevel)

	return &AccessLogger{
		writer: writer,
		log:    zap.New(core),
	}, nil
}

type accessLogEncoder struct {
	zapcore.ObjectEncoder
}

func newAccessLogEncoder() *accessLogEncoder {
	return &accessLogEncoder{}
}

func (e *accessLogEncoder) Clone() zapcore.Encoder {
	return &accessLogEncoder{}
}

func (e *accessLogEncoder) EncodeEntry(ent zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	line := accessLogBufferPool.Get()

	appendAccessLogPart(line, ent.Time.Format(accessLogTimeLayout))
	appendAccessLogPart(line, ent.Level.CapitalString())

	if len(fields) > 0 {
		appendAccessLogPart(line, "")
		appendAccessLogFields(line, fields)
	}
	line.AppendByte('\n')

	return line, nil
}

func appendAccessLogPart(line *buffer.Buffer, value string) {
	if line.Len() > 0 {
		line.AppendByte('\t')
	}
	line.AppendString(value)
}

func appendAccessLogFields(line *buffer.Buffer, fields []zapcore.Field) {
	encoder := &accessLogFieldEncoder{line: line}
	for i, field := range fields {
		if i > 0 {
			line.AppendByte(' ')
		}
		field.AddTo(encoder)
	}
}

type accessLogFieldEncoder struct {
	line *buffer.Buffer
}

func (e *accessLogFieldEncoder) AddArray(key string, marshaler zapcore.ArrayMarshaler) error {
	appendAccessLogValue(e.line, "-")
	return nil
}

func (e *accessLogFieldEncoder) AddObject(key string, marshaler zapcore.ObjectMarshaler) error {
	appendAccessLogValue(e.line, "-")
	return nil
}

func (e *accessLogFieldEncoder) AddBinary(key string, value []byte) {
	appendAccessLogValue(e.line, fmt.Sprintf("[%d bytes]", len(value)))
}

func (e *accessLogFieldEncoder) AddByteString(key string, value []byte) {
	appendAccessLogValue(e.line, string(value))
}

func (e *accessLogFieldEncoder) AddBool(key string, value bool) {
	appendAccessLogValue(e.line, strconv.FormatBool(value))
}

func (e *accessLogFieldEncoder) AddComplex128(key string, value complex128) {
	appendAccessLogValue(e.line, strconv.FormatComplex(value, 'g', -1, 128))
}

func (e *accessLogFieldEncoder) AddComplex64(key string, value complex64) {
	appendAccessLogValue(e.line, strconv.FormatComplex(complex128(value), 'g', -1, 64))
}

func (e *accessLogFieldEncoder) AddDuration(key string, value time.Duration) {
	appendAccessLogValue(e.line, value.String())
}

func (e *accessLogFieldEncoder) AddFloat64(key string, value float64) {
	appendAccessLogValue(e.line, strconv.FormatFloat(value, 'f', -1, 64))
}

func (e *accessLogFieldEncoder) AddFloat32(key string, value float32) {
	appendAccessLogValue(e.line, strconv.FormatFloat(float64(value), 'f', -1, 32))
}

func (e *accessLogFieldEncoder) AddInt(key string, value int) {
	appendAccessLogValue(e.line, strconv.Itoa(value))
}

func (e *accessLogFieldEncoder) AddInt64(key string, value int64) {
	appendAccessLogValue(e.line, strconv.FormatInt(value, 10))
}

func (e *accessLogFieldEncoder) AddInt32(key string, value int32) {
	appendAccessLogValue(e.line, strconv.FormatInt(int64(value), 10))
}

func (e *accessLogFieldEncoder) AddInt16(key string, value int16) {
	appendAccessLogValue(e.line, strconv.FormatInt(int64(value), 10))
}

func (e *accessLogFieldEncoder) AddInt8(key string, value int8) {
	appendAccessLogValue(e.line, strconv.FormatInt(int64(value), 10))
}

func (e *accessLogFieldEncoder) AddString(key string, value string) {
	appendAccessLogValue(e.line, value)
}

func (e *accessLogFieldEncoder) AddTime(key string, value time.Time) {
	appendAccessLogValue(e.line, value.Format(time.RFC3339))
}

func (e *accessLogFieldEncoder) AddUint(key string, value uint) {
	appendAccessLogValue(e.line, strconv.FormatUint(uint64(value), 10))
}

func (e *accessLogFieldEncoder) AddUint64(key string, value uint64) {
	appendAccessLogValue(e.line, strconv.FormatUint(value, 10))
}

func (e *accessLogFieldEncoder) AddUint32(key string, value uint32) {
	appendAccessLogValue(e.line, strconv.FormatUint(uint64(value), 10))
}

func (e *accessLogFieldEncoder) AddUint16(key string, value uint16) {
	appendAccessLogValue(e.line, strconv.FormatUint(uint64(value), 10))
}

func (e *accessLogFieldEncoder) AddUint8(key string, value uint8) {
	appendAccessLogValue(e.line, strconv.FormatUint(uint64(value), 10))
}

func (e *accessLogFieldEncoder) AddUintptr(key string, value uintptr) {
	appendAccessLogValue(e.line, strconv.FormatUint(uint64(value), 10))
}

func (e *accessLogFieldEncoder) AddReflected(key string, value interface{}) error {
	appendAccessLogValue(e.line, fmt.Sprint(value))
	return nil
}

func (e *accessLogFieldEncoder) OpenNamespace(key string) {}

func appendAccessLogValue(line *buffer.Buffer, value string) {
	value = sanitizeAccessLogValue(value)
	if value == "" {
		value = "-"
	}
	if strings.Contains(value, " ") {
		value = strconv.Quote(value)
	}
	line.AppendString(value)
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

	l.log.Info("",
		zap.String("method", r.Method),
		zap.String("url", r.URL.RequestURI()),
		zap.Int("status_code", statusCode),
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
	if value == "" {
		return ""
	}

	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	return strings.TrimSpace(value)
}

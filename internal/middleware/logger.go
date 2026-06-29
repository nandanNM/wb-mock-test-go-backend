package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"backend/internal/logger"
)

// responseRecorder wraps http.ResponseWriter to capture the status code and the
// number of bytes written, so the logging middleware can report them.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		// Mirror net/http: an implicit WriteHeader(200) on first Write.
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush exposes the underlying flusher when available (e.g. for streaming).
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Logger logs one structured line per request with method, path, status,
// latency and timestamps, and injects a request-scoped logger (pre-tagged with
// the request ID) into the context so handlers can log with correlation.
//
// The base logger is the application logger created at startup.
func Logger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := RequestIDFromContext(r.Context())

			// Request-scoped logger: every handler log line is automatically
			// correlated by request_id.
			reqLog := base.With(
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
			)
			ctx := logger.WithContext(r.Context(), reqLog)
			r = r.WithContext(ctx)

			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

			// Log request receipt (helpful for spotting slow/hung requests that
			// never produce a response line).
			reqLog.Info("request started",
				"timestamp", start.UTC().Format(time.RFC3339Nano),
				"remote_addr", clientIP(r),
				"user_agent", r.UserAgent(),
				"query", r.URL.RawQuery,
			)

			next.ServeHTTP(rec, r)

			// Latency measured around the handler; logged in ms for dashboards
			// and as raw duration string for humans.
			latency := time.Since(start)
			reqLog.Info("request completed",
				"status", rec.status,
				"bytes", rec.bytes,
				"latency", latency.String(),
				"latency_ms", float64(latency.Microseconds())/1000.0,
				"timestamp", time.Now().UTC().Format(time.RFC3339Nano),
			)
		})
	}
}

// clientIP best-effort extracts the originating client IP, honoring common
// proxy headers. Trust these only behind a proxy you control.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}
	return r.RemoteAddr
}

package httpserver

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"
)

// responseRecorder keeps a copy of the body of failed responses only.
// It embeds gin.ResponseWriter so Flush, Hijack and CloseNotify pass through:
// reimplementing the interface would buffer SSE streams.
type responseRecorder struct {
	gin.ResponseWriter

	body bytes.Buffer
}

// Unwrap lets http.ResponseController and Huma's SSE reach the connection's
// write deadline, which the embedded interface alone hides.
func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.record(b)
	return r.ResponseWriter.Write(b)
}

func (r *responseRecorder) WriteString(s string) (int, error) {
	r.record([]byte(s))
	return r.ResponseWriter.WriteString(s)
}

func (r *responseRecorder) record(b []byte) {
	if r.Status() < http.StatusBadRequest {
		return
	}

	room := maxLoggedBodyBytes - r.body.Len()
	if room <= 0 {
		return
	}

	r.body.Write(b[:min(len(b), room)])
}

func (r *responseRecorder) capturedBody() string {
	return r.body.String()
}

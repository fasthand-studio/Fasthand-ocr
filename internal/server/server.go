// Package server exposes the OCR service over HTTP.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fasthand-studio/fasthand-ocr/internal/config"
	"github.com/fasthand-studio/fasthand-ocr/internal/ocr"
	"github.com/fasthand-studio/fasthand-ocr/web"
)

// Payload limits. A base64 body of MaxBodyBytes decodes to at most
// MaxImageBytes, so the caps are deliberately in a 4:3 ratio.
const (
	MaxBodyBytes  = 8 * 1024 * 1024
	MaxImageBytes = 6 * 1024 * 1024
)

// Client-facing messages are unchanged from the previous implementation, apart
// from the internal-error case which no longer echoes error text to callers.
const (
	msgNoImage       = "No image provided"
	msgBadBase64     = "Invalid base64 image data"
	msgBadJSON       = "Invalid JSON body"
	msgUnsupported   = "Unsupported image format (JPEG, PNG or WebP required)"
	msgUnauthorized  = "Unauthorized: missing or invalid X-API-Key header"
	msgMisconfigured = "Server misconfigured: API_KEY secret is not set"
	msgBodyTooLarge  = "Request body too large (max 8 MB)"
	msgImageTooLarge = "Image too large (max 6 MB decoded)"
	msgInternal      = "Internal server error"
	msgBusy          = "Server busy, please retry"
	msgNotFound      = "Not found"
	methodNotAllowed = "Method not allowed"
	allowMethods     = "GET, POST, OPTIONS"
	contentTypeJSON  = "application/json"
	contentTypeDocs  = "text/html; charset=utf-8"
	docsCSP          = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; frame-ancestors 'none'"
)

// apiError is a failure that can be reported to the caller as-is.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return e.message }

// Server wires the HTTP surface to an OCR provider.
type Server struct {
	provider ocr.Provider
	cfg      config.Config
	keyHash  [sha256.Size]byte // hashed secret, so comparison is fixed-length
	slots    chan struct{}
	log      *slog.Logger
}

// New returns a ready Server. A missing API key is deliberately not a startup
// failure: the service answers 503 with a precise reason, which is the
// behaviour existing callers already see.
func New(provider ocr.Provider, cfg config.Config, log *slog.Logger) *Server {
	s := &Server{
		provider: provider,
		cfg:      cfg,
		slots:    make(chan struct{}, cfg.MaxConcurrentOCR),
		log:      log,
	}
	if cfg.APIKey != "" {
		s.keyHash = sha256.Sum256([]byte(cfg.APIKey))
	}
	return s
}

// Handler returns the wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("OPTIONS /", http.HandlerFunc(s.handlePreflight))
	mux.HandleFunc("GET /{$}", s.handleDocs)
	mux.HandleFunc("POST /{$}", s.handleOCR)
	mux.HandleFunc("/", s.handleFallback)
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		defer func() {
			if p := recover(); p != nil {
				s.log.Error("panic recovered", "path", r.URL.Path, "panic", fmt.Sprint(p))
				writeJSON(rec, http.StatusInternalServerError, errorResponse{Error: msgInternal})
			}
			// Nothing here logs headers, bodies or key material.
			s.log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		}()

		setBaseHeaders(rec)
		next.ServeHTTP(rec, r)
	})
}

// handlePreflight answers CORS preflight for any path.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// handleFallback keeps the previous plain-text 405 for the API root and
// returns a JSON 404 for paths that never existed. The old build ran a full
// OCR request for POST on any path, and answered 405 to unknown GET paths.
func (s *Server) handleFallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		w.Header().Set("Allow", allowMethods)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		io.WriteString(w, methodNotAllowed) //nolint:errcheck // response best effort
		return
	}
	writeJSON(w, http.StatusNotFound, errorResponse{Error: msgNotFound})
}

// handleDocs serves the API documentation page, unchanged from the deployed
// Worker.
func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", docsCSP)
	w.Header().Set("Content-Type", contentTypeDocs)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(web.Docs)
}

func (s *Server) handleOCR(w http.ResponseWriter, r *http.Request) {
	if s.cfg.APIKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: msgMisconfigured})
		return
	}
	if !s.authenticated(r) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: msgUnauthorized})
		return
	}

	data, mime, apiErr := s.readImage(w, r)
	if apiErr != nil {
		writeJSON(w, apiErr.status, errorResponse{Error: apiErr.message})
		return
	}

	// Bound in-flight inference so a burst cannot exhaust memory or upstream
	// quota on behalf of everyone else.
	release, ok := s.acquireSlot()
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: msgBusy})
		return
	}
	defer release()

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()

	text, err := s.provider.Recognize(ctx, &ocr.Image{Data: data, MIME: mime})
	if err != nil {
		// Upstream detail is logged, never returned: the previous build copied
		// error.Error() into the response body.
		s.log.Error("ocr failed", "err", err, "mime", mime, "bytes", len(data))
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: msgInternal})
		return
	}

	result := ocr.BuildResult(text)
	writeJSON(w, http.StatusOK, successResponse{Success: true, Text: result.Text, Words: result.Words})
}

// readImage validates and decodes the request body into image bytes.
func (s *Server) readImage(w http.ResponseWriter, r *http.Request) ([]byte, string, *apiError) {
	// The advertised length is rejected early, but the real byte count is what
	// bounds the request: Content-Length can be missing or wrong.
	if r.ContentLength > MaxBodyBytes {
		return nil, "", &apiError{http.StatusRequestEntityTooLarge, msgBodyTooLarge}
	}

	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, "", &apiError{http.StatusRequestEntityTooLarge, msgBodyTooLarge}
		}
		return nil, "", &apiError{http.StatusBadRequest, msgBadJSON}
	}

	var body struct {
		Image json.RawMessage `json:"image"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, "", &apiError{http.StatusBadRequest, msgBadJSON}
	}

	// A missing, null, empty or non-string image all mean "No image provided",
	// exactly as before.
	encoded := strings.TrimSpace(string(body.Image))
	if encoded == "" || encoded[0] != '"' {
		return nil, "", &apiError{http.StatusBadRequest, msgNoImage}
	}
	var image64 string
	if err := json.Unmarshal(body.Image, &image64); err != nil || image64 == "" {
		return nil, "", &apiError{http.StatusBadRequest, msgNoImage}
	}

	data, err := imageFromBase64(image64)
	switch {
	case errors.Is(err, errDecodedTooLarge):
		return nil, "", &apiError{http.StatusRequestEntityTooLarge, msgImageTooLarge}
	case err != nil:
		return nil, "", &apiError{http.StatusBadRequest, msgBadBase64}
	}

	mime := sniffFormat(data)
	if mime == "" {
		return nil, "", &apiError{http.StatusBadRequest, msgUnsupported}
	}
	return data, mime, nil
}

func (s *Server) authenticated(r *http.Request) bool {
	provided := r.Header.Get("X-API-Key")
	if provided == "" {
		return false
	}
	// Hashing both sides keeps the comparison fixed-length, so it does not leak
	// the secret's length the way an early byte-length check would.
	sum := sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(sum[:], s.keyHash[:]) == 1
}

// acquireSlot takes one inference slot without queueing. Saturation asks the
// client to back off instead of stalling the server.
func (s *Server) acquireSlot() (func(), bool) {
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, true
	default:
		return nil, false
	}
}

type successResponse struct {
	Success bool       `json:"success"`
	Text    string     `json:"text"`
	Words   []ocr.Word `json:"words"`
}

type errorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

var baseHeaders = [][2]string{
	{"Access-Control-Allow-Origin", "*"},
	{"Access-Control-Allow-Methods", allowMethods},
	{"Access-Control-Allow-Headers", "Content-Type, X-API-Key"},
	{"Access-Control-Max-Age", "86400"},
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "DENY"},
	{"Referrer-Policy", "no-referrer"},
	{"Permissions-Policy", "camera=(), geolocation=(), microphone=()"},
	{"Cache-Control", "no-store"},
}

func setBaseHeaders(w http.ResponseWriter) {
	h := w.Header()
	for _, kv := range baseHeaders {
		h.Set(kv[0], kv[1])
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	// Field order and presence match the previous JSON.stringify output.
	buf, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, msgInternal, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

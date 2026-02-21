package web

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mickamy/grpc-tap/broker"
	"github.com/mickamy/grpc-tap/proxy"
)

//go:embed static
var staticFS embed.FS

// Server serves the grpc-tap web UI and API endpoints.
type Server struct {
	httpServer *http.Server
	broker     *broker.Broker
	proxy      proxy.Proxy
}

// New creates a new web Server backed by the given Broker and Proxy.
func New(b *broker.Broker, p proxy.Proxy) *Server {
	s := &Server{
		broker: b,
		proxy:  p,
	}

	mux := http.NewServeMux()

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(fmt.Sprintf("web: embedded static filesystem: %v", err))
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /api/events", s.handleSSE)
	mux.HandleFunc("POST /api/replay", s.handleReplay)

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Serve starts the HTTP server on the given listener.
func (s *Server) Serve(lis net.Listener) error {
	if err := s.httpServer.Serve(lis); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web: serve: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("web: shutdown: %w", err)
	}
	return nil
}

// Handler returns the HTTP handler for testing.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

type eventJSON struct {
	ID              string            `json:"id"`
	Method          string            `json:"method"`
	CallType        string            `json:"call_type"`
	Protocol        string            `json:"protocol"`
	StartTime       string            `json:"start_time"`
	DurationMs      float64           `json:"duration_ms"`
	Status          int32             `json:"status"`
	Error           string            `json:"error,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
}

func eventToJSON(ev proxy.Event) eventJSON {
	return eventJSON{
		ID:              ev.ID,
		Method:          ev.Method,
		CallType:        ev.CallType.String(),
		Protocol:        ev.Protocol.String(),
		StartTime:       ev.StartTime.Format(time.RFC3339Nano),
		DurationMs:      float64(ev.Duration.Microseconds()) / 1000,
		Status:          ev.Status,
		Error:           ev.Error,
		RequestHeaders:  flattenHeaders(ev.RequestHeaders),
		ResponseHeaders: flattenHeaders(ev.ResponseHeaders),
		RequestBody:     encodeBody(ev.RequestBody),
		ResponseBody:    encodeBody(ev.ResponseBody),
	}
}

func flattenHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	m := make(map[string]string, len(h))
	for k, vs := range h {
		m[k] = strings.Join(vs, ", ")
	}
	return m
}

func encodeBody(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush()

	ch, unsub := s.broker.Subscribe()
	defer unsub()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(eventToJSON(ev))
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

type replayRequest struct {
	Method      string `json:"method"`
	RequestBody string `json:"request_body"`
}

type replayResponse struct {
	Event *eventJSON `json:"event,omitempty"`
	Error string     `json:"error,omitempty"`
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2*proxy.MaxCaptureSize)

	var req replayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, &replayResponse{
			Error: "invalid request body: " + err.Error(),
		})
		return
	}

	if req.Method == "" || !strings.HasPrefix(req.Method, "/") {
		writeJSON(w, http.StatusBadRequest, &replayResponse{
			Error: "invalid method: must be a non-empty path starting with '/'",
		})
		return
	}

	body, err := base64.StdEncoding.DecodeString(req.RequestBody)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, &replayResponse{
			Error: "invalid base64 body: " + err.Error(),
		})
		return
	}

	if len(body) > proxy.MaxCaptureSize {
		writeJSON(w, http.StatusBadRequest, &replayResponse{
			Error: "request body too large",
		})
		return
	}

	ev, err := s.proxy.Replay(r.Context(), req.Method, body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, &replayResponse{
			Error: err.Error(),
		})
		return
	}

	ej := eventToJSON(ev)
	writeJSON(w, http.StatusOK, &replayResponse{Event: &ej})
}

func writeJSON(w http.ResponseWriter, status int, v *replayResponse) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}

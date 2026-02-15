package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// ReverseProxy is an HTTP-level reverse proxy that captures gRPC, gRPC-Web,
// and Connect protocol traffic.
type ReverseProxy struct {
	listenAddr string
	upstream   *url.URL
	events     chan Event
	server     *http.Server
	transport  http.RoundTripper
}

// New creates a new ReverseProxy.
// listenAddr is the address to listen on (e.g. ":8080").
// upstreamAddr is the upstream server address (e.g. "http://localhost:9090").
func New(listenAddr, upstreamAddr string) (*ReverseProxy, error) {
	u, err := url.Parse(upstreamAddr)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse upstream: %w", err)
	}

	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}

	rp := &ReverseProxy{
		listenAddr: listenAddr,
		upstream:   u,
		events:     make(chan Event, 256),
		transport:  transport,
	}

	h2s := &http2.Server{}
	rp.server = &http.Server{
		Addr:    listenAddr,
		Handler: h2c.NewHandler(rp, h2s),
	}

	return rp, nil
}

// ListenAndServe starts the proxy and blocks until ctx is cancelled.
func (rp *ReverseProxy) ListenAndServe(ctx context.Context) error {
	lis, err := net.Listen("tcp", rp.listenAddr)
	if err != nil {
		return fmt.Errorf("proxy: listen %s: %w", rp.listenAddr, err)
	}

	go func() {
		<-ctx.Done()
		_ = rp.server.Close()
	}()

	if err := rp.server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("proxy: serve: %w", err)
	}
	close(rp.events)
	return nil
}

// Events returns the channel of captured events.
func (rp *ReverseProxy) Events() <-chan Event {
	return rp.events
}

// Close stops the proxy.
func (rp *ReverseProxy) Close() error {
	return rp.server.Close()
}

// Replay sends a gRPC unary request to the upstream server and returns the
// resulting event. The body should be raw protobuf bytes (without gRPC framing).
// The event is also published to the events channel.
func (rp *ReverseProxy) Replay(ctx context.Context, method string, body []byte) (Event, error) {
	start := time.Now()

	// Wrap body in gRPC length-prefixed frame.
	frame := make([]byte, 5+len(body))
	frame[0] = 0 // no compression
	frame[1] = byte(len(body) >> 24)
	frame[2] = byte(len(body) >> 16)
	frame[3] = byte(len(body) >> 8)
	frame[4] = byte(len(body))
	copy(frame[5:], body)

	upstreamURL := *rp.upstream
	upstreamURL.Path = method

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL.String(), io.NopCloser(bytes.NewReader(frame)))
	if err != nil {
		return Event{}, fmt.Errorf("replay: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")

	resp, err := rp.transport.RoundTrip(req)
	if err != nil {
		return Event{}, fmt.Errorf("replay: roundtrip: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return Event{}, fmt.Errorf("replay: read response: %w", err)
	}

	status, errMsg := extractGRPCStatus(resp)
	respPayload := ExtractPayload(respData)

	ev := Event{
		ID:              uuid.New().String(),
		Method:          method,
		CallType:        Unary,
		Protocol:        ProtocolGRPC,
		StartTime:       start,
		Duration:        time.Since(start),
		Status:          status,
		Error:           errMsg,
		RequestHeaders:  req.Header.Clone(),
		ResponseHeaders: resp.Header.Clone(),
		RequestBody:     body,
		ResponseBody:    respPayload,
	}

	// Publish to event channel (non-blocking).
	select {
	case rp.events <- ev:
	default:
	}

	return ev, nil
}

// ServeHTTP handles each proxied request.
func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	protocol := DetectProtocol(r)
	contentType := r.Header.Get("Content-Type")
	method := r.URL.Path

	// Wrap request body for capture and frame counting.
	reqCapture := NewCaptureReader(r.Body, MaxCaptureSize)
	var reqFrames *FrameCounter
	body := io.Reader(reqCapture)
	if protocol == ProtocolGRPC || protocol == ProtocolGRPCWeb {
		reqFrames = NewFrameCounter(reqCapture)
		body = reqFrames
	}

	// Build upstream request.
	upstreamURL := *rp.upstream
	upstreamURL.Path = r.URL.Path
	upstreamURL.RawQuery = r.URL.RawQuery

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL.String(), io.NopCloser(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(outReq.Header, r.Header)
	// Announce trailers so the upstream response trailers are forwarded.
	outReq.Trailer = r.Trailer

	resp, err := rp.transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Copy response headers.
	copyHeaders(w.Header(), resp.Header)
	// Announce trailers from the response.
	for k := range resp.Trailer {
		w.Header().Add("Trailer", k)
	}
	w.WriteHeader(resp.StatusCode)

	// Wrap response body for capture and frame counting.
	respCapture := NewCaptureReader(resp.Body, MaxCaptureSize)
	var respFrames *FrameCounter
	respBody := io.Reader(respCapture)
	if protocol == ProtocolGRPC || protocol == ProtocolGRPCWeb {
		respFrames = NewFrameCounter(respCapture)
		respBody = respFrames
	}

	// Copy body (streaming).
	if f, ok := w.(http.Flusher); ok {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := respBody.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				f.Flush()
			}
			if readErr != nil {
				break
			}
		}
	} else {
		_, _ = io.Copy(w, respBody)
	}

	// Copy trailers.
	for k, vs := range resp.Trailer {
		for _, v := range vs {
			w.Header().Add(http.TrailerPrefix+k, v)
		}
	}

	// Emit event.
	status, errMsg := ExtractStatus(protocol, resp)
	capturedReq := reqCapture.Bytes()
	capturedResp := respCapture.Bytes()
	if protocol == ProtocolGRPC || protocol == ProtocolGRPCWeb {
		capturedReq = ExtractPayload(capturedReq)
		capturedResp = ExtractPayload(capturedResp)
	} else {
		capturedReq = DecompressGzip(capturedReq)
		capturedResp = DecompressGzip(capturedResp)
	}

	rp.events <- Event{
		ID:              uuid.New().String(),
		Method:          method,
		CallType:        DetectCallType(protocol, contentType, reqFrames, respFrames),
		Protocol:        protocol,
		StartTime:       start,
		Duration:        time.Since(start),
		Status:          status,
		Error:           errMsg,
		RequestHeaders:  r.Header.Clone(),
		ResponseHeaders: resp.Header.Clone(),
		RequestBody:     capturedReq,
		ResponseBody:    capturedResp,
	}
}

// DetectProtocol determines the wire protocol from the Content-Type header.
func DetectProtocol(r *http.Request) Protocol {
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/grpc-web"):
		return ProtocolGRPCWeb
	case strings.HasPrefix(ct, "application/grpc"):
		return ProtocolGRPC
	default:
		return ProtocolConnect
	}
}

// ExtractStatus extracts the gRPC status code from the response
// based on the wire protocol.
func ExtractStatus(p Protocol, resp *http.Response) (int32, string) {
	switch p {
	case ProtocolGRPC, ProtocolGRPCWeb:
		return extractGRPCStatus(resp)
	case ProtocolConnect:
		return extractConnectStatus(resp)
	default:
		return 0, ""
	}
}

// extractGRPCStatus reads grpc-status from response trailers or headers.
func extractGRPCStatus(resp *http.Response) (int32, string) {
	// Trailers (populated after body is fully read).
	if s := resp.Trailer.Get("Grpc-Status"); s != "" {
		code, _ := strconv.ParseInt(s, 10, 32)
		return int32(code), resp.Trailer.Get("Grpc-Message")
	}
	// Some implementations send grpc-status in headers (e.g. immediate errors).
	if s := resp.Header.Get("Grpc-Status"); s != "" {
		code, _ := strconv.ParseInt(s, 10, 32)
		return int32(code), resp.Header.Get("Grpc-Message")
	}
	return 0, ""
}

// extractConnectStatus maps HTTP status to a gRPC-compatible status code.
// Connect uses HTTP status codes; 200 = OK, others map to gRPC codes.
func extractConnectStatus(resp *http.Response) (int32, string) {
	if resp.StatusCode == http.StatusOK {
		return 0, "" // OK
	}
	return httpStatusToGRPCCode(resp.StatusCode), resp.Status
}

// httpStatusToGRPCCode maps an HTTP status code to a gRPC-compatible status code.
// This replicates the Connect protocol specification's httpToCode mapping.
func httpStatusToGRPCCode(httpStatus int) int32 {
	switch httpStatus {
	case http.StatusBadRequest:
		return int32(connect.CodeInternal)
	case http.StatusUnauthorized:
		return int32(connect.CodeUnauthenticated)
	case http.StatusForbidden:
		return int32(connect.CodePermissionDenied)
	case http.StatusNotFound:
		return int32(connect.CodeUnimplemented)
	case http.StatusTooManyRequests:
		return int32(connect.CodeUnavailable)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return int32(connect.CodeUnavailable)
	default:
		return int32(connect.CodeUnknown)
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

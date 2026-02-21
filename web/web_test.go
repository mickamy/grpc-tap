package web_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mickamy/grpc-tap/broker"
	"github.com/mickamy/grpc-tap/proxy"
	"github.com/mickamy/grpc-tap/web"
)

type fakeProxy struct {
	replayFunc func(ctx context.Context, method string, body []byte) (proxy.Event, error)
}

func (f *fakeProxy) ListenAndServe(context.Context) error { return nil }
func (f *fakeProxy) Events() <-chan proxy.Event           { return nil }
func (f *fakeProxy) Close() error                         { return nil }
func (f *fakeProxy) Replay(ctx context.Context, method string, body []byte) (proxy.Event, error) {
	if f.replayFunc != nil {
		return f.replayFunc(ctx, method, body)
	}
	return proxy.Event{}, nil
}

func newTestServer(t *testing.T, b *broker.Broker, p proxy.Proxy) *httptest.Server {
	t.Helper()
	srv := web.New(b, p)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// doPost sends a POST to /api/replay and returns the response.
func doPost(
	t *testing.T, ts *httptest.Server, body string,
) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost,
		ts.URL+"/api/replay", strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req) //nolint:gosec // test code
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSSE(t *testing.T) {
	t.Parallel()

	b := broker.New(8)
	ts := newTestServer(t, b, &fakeProxy{})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, ts.URL+"/api/events", nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req) //nolint:gosec // test code
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want %q", ct, "text/event-stream")
	}

	// Publish an event after SSE connection is established.
	ev := proxy.Event{
		ID:        "sse-1",
		Method:    "/test.Service/Hello",
		CallType:  proxy.Unary,
		Protocol:  proxy.ProtocolGRPC,
		StartTime: time.Now(),
		Duration:  10 * time.Millisecond,
		Status:    0,
	}

	// Give the SSE handler time to subscribe.
	time.Sleep(50 * time.Millisecond)
	b.Publish(ev)

	scanner := bufio.NewScanner(resp.Body)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for SSE event")
		default:
		}
		if !scanner.Scan() {
			t.Fatal("unexpected end of SSE stream")
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var got map[string]any
		if err := json.Unmarshal([]byte(data), &got); err != nil {
			t.Fatalf("invalid JSON in SSE event: %v", err)
		}
		if got["id"] != "sse-1" {
			t.Errorf("id = %v, want %q", got["id"], "sse-1")
		}
		if got["method"] != "/test.Service/Hello" {
			t.Errorf("method = %v, want %q", got["method"], "/test.Service/Hello")
		}
		return
	}
}

func TestReplay(t *testing.T) {
	t.Parallel()

	b := broker.New(8)
	fp := &fakeProxy{
		replayFunc: func(_ context.Context, method string, body []byte) (proxy.Event, error) {
			return proxy.Event{
				ID:           "replay-1",
				Method:       method,
				CallType:     proxy.Unary,
				Protocol:     proxy.ProtocolGRPC,
				StartTime:    time.Now(),
				Duration:     5 * time.Millisecond,
				Status:       0,
				RequestBody:  body,
				ResponseBody: []byte("resp"),
			}, nil
		},
	}
	ts := newTestServer(t, b, fp)

	reqBody := base64.StdEncoding.EncodeToString([]byte("hello"))
	payload := `{"method":"/test.Service/Hello","request_body":"` + reqBody + `"}`
	resp := doPost(t, ts, payload)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var result struct {
		Event *struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		} `json:"event"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Event.ID != "replay-1" {
		t.Errorf("id = %q, want %q", result.Event.ID, "replay-1")
	}
	if result.Event.Method != "/test.Service/Hello" {
		t.Errorf("method = %q, want %q", result.Event.Method, "/test.Service/Hello")
	}
}

func TestReplay_InvalidJSON(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, broker.New(8), &fakeProxy{})
	resp := doPost(t, ts, "{bad")
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestReplay_InvalidBase64(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, broker.New(8), &fakeProxy{})
	resp := doPost(t, ts, `{"method":"/test.Service/Hello","request_body":"not-valid-base64!!!"}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestReplay_EmptyMethod(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, broker.New(8), &fakeProxy{})
	resp := doPost(t, ts, `{"method":"","request_body":""}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestReplay_MethodWithoutSlash(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, broker.New(8), &fakeProxy{})
	resp := doPost(t, ts, `{"method":"test.Service/Hello","request_body":""}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestReplay_BodyTooLarge(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, broker.New(8), &fakeProxy{})

	largeBody := base64.StdEncoding.EncodeToString(make([]byte, proxy.MaxCaptureSize+1))
	payload := `{"method":"/test.Service/Hello","request_body":"` + largeBody + `"}`
	resp := doPost(t, ts, payload)
	defer func() { _ = resp.Body.Close() }()

	// MaxBytesReader may return 413 or the JSON decode may fail with 400.
	if resp.StatusCode != http.StatusBadRequest &&
		resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 400 or 413", resp.StatusCode)
	}
}

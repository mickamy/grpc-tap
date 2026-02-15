package proxy_test

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"testing"

	"github.com/mickamy/grpc-tap/proxy"
)

func buildGRPCFrame(payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(0) // no compression
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(payload)))
	buf.Write(length)
	buf.Write(payload)
	return buf.Bytes()
}

func TestFrameCounter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		frames int
	}{
		{"zero", 0},
		{"one", 1},
		{"three", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var data bytes.Buffer
			for range tt.frames {
				data.Write(buildGRPCFrame([]byte("hello")))
			}

			fc := proxy.NewFrameCounter(&data)
			if _, err := io.ReadAll(fc); err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if fc.Count != tt.frames {
				t.Errorf("Count = %d, want %d", fc.Count, tt.frames)
			}
		})
	}
}

func TestFrameCounter_SmallReads(t *testing.T) {
	t.Parallel()

	var data bytes.Buffer
	data.Write(buildGRPCFrame([]byte("hello")))
	data.Write(buildGRPCFrame([]byte("world")))

	fc := proxy.NewFrameCounter(&data)
	buf := make([]byte, 1) // read one byte at a time
	for {
		_, err := fc.Read(buf)
		if err != nil {
			break
		}
	}
	if fc.Count != 2 {
		t.Errorf("Count = %d, want 2", fc.Count)
	}
}

func TestFrameCounter_EmptyPayload(t *testing.T) {
	t.Parallel()

	var data bytes.Buffer
	data.Write(buildGRPCFrame(nil)) // zero-length payload

	fc := proxy.NewFrameCounter(&data)
	if _, err := io.ReadAll(fc); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if fc.Count != 1 {
		t.Errorf("Count = %d, want 1", fc.Count)
	}
}

func TestCaptureReader(t *testing.T) {
	t.Parallel()

	t.Run("captures all bytes within limit", func(t *testing.T) {
		t.Parallel()

		data := bytes.Repeat([]byte("a"), 100)
		cr := proxy.NewCaptureReader(bytes.NewReader(data), 256)
		got, err := io.ReadAll(cr)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("passthrough: got %d bytes, want %d", len(got), len(data))
		}
		if !bytes.Equal(cr.Bytes(), data) {
			t.Errorf("captured: got %d bytes, want %d", len(cr.Bytes()), len(data))
		}
	})

	t.Run("truncates at max size", func(t *testing.T) {
		t.Parallel()

		data := bytes.Repeat([]byte("b"), 200)
		cr := proxy.NewCaptureReader(bytes.NewReader(data), 50)
		got, err := io.ReadAll(cr)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if len(got) != 200 {
			t.Errorf("passthrough: got %d bytes, want 200", len(got))
		}
		if len(cr.Bytes()) != 50 {
			t.Errorf("captured: got %d bytes, want 50", len(cr.Bytes()))
		}
	})

	t.Run("empty reader", func(t *testing.T) {
		t.Parallel()

		cr := proxy.NewCaptureReader(bytes.NewReader(nil), 256)
		if _, err := io.ReadAll(cr); err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if len(cr.Bytes()) != 0 {
			t.Errorf("captured: got %d bytes, want 0", len(cr.Bytes()))
		}
	})
}

func TestExtractPayload(t *testing.T) {
	t.Parallel()

	t.Run("uncompressed", func(t *testing.T) {
		t.Parallel()

		payload := []byte("hello world")
		frame := buildGRPCFrame(payload)
		got := proxy.ExtractPayload(frame)
		if !bytes.Equal(got, payload) {
			t.Errorf("got %q, want %q", got, payload)
		}
	})

	t.Run("gzip compressed", func(t *testing.T) {
		t.Parallel()

		payload := []byte("compressed payload")
		var compressed bytes.Buffer
		w := gzip.NewWriter(&compressed)
		_, _ = w.Write(payload)
		_ = w.Close()

		// Build frame with compression flag = 1
		var frame bytes.Buffer
		frame.WriteByte(1) // compressed
		length := make([]byte, 4)
		binary.BigEndian.PutUint32(length, uint32(compressed.Len()))
		frame.Write(length)
		frame.Write(compressed.Bytes())

		got := proxy.ExtractPayload(frame.Bytes())
		if !bytes.Equal(got, payload) {
			t.Errorf("got %q, want %q", got, payload)
		}
	})

	t.Run("too short", func(t *testing.T) {
		t.Parallel()

		data := []byte{0, 1, 2}
		got := proxy.ExtractPayload(data)
		if !bytes.Equal(got, data) {
			t.Errorf("got %q, want %q", got, data)
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		t.Parallel()

		frame := buildGRPCFrame(nil)
		got := proxy.ExtractPayload(frame)
		if len(got) != 0 {
			t.Errorf("got %d bytes, want 0", len(got))
		}
	})
}

func TestDetectCallType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		protocol    proxy.Protocol
		contentType string
		reqFrames   int
		respFrames  int
		want        proxy.CallType
	}{
		{
			name:     "gRPC unary",
			protocol: proxy.ProtocolGRPC,
			reqFrames: 1, respFrames: 1,
			want: proxy.Unary,
		},
		{
			name:     "gRPC server stream",
			protocol: proxy.ProtocolGRPC,
			reqFrames: 1, respFrames: 5,
			want: proxy.ServerStream,
		},
		{
			name:     "gRPC client stream",
			protocol: proxy.ProtocolGRPC,
			reqFrames: 3, respFrames: 1,
			want: proxy.ClientStream,
		},
		{
			name:     "gRPC bidi stream",
			protocol: proxy.ProtocolGRPC,
			reqFrames: 3, respFrames: 5,
			want: proxy.BidiStream,
		},
		{
			name:        "Connect unary",
			protocol:    proxy.ProtocolConnect,
			contentType: "application/proto",
			want:        proxy.Unary,
		},
		{
			name:        "Connect streaming",
			protocol:    proxy.ProtocolConnect,
			contentType: "application/connect+proto",
			want:        proxy.ServerStream,
		},
		{
			name:        "Connect streaming JSON",
			protocol:    proxy.ProtocolConnect,
			contentType: "application/connect+json",
			want:        proxy.ServerStream,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var req, resp *proxy.FrameCounter
			if tt.protocol != proxy.ProtocolConnect {
				req = &proxy.FrameCounter{Count: tt.reqFrames}
				resp = &proxy.FrameCounter{Count: tt.respFrames}
			}

			got := proxy.DetectCallType(tt.protocol, tt.contentType, req, resp)
			if got != tt.want {
				t.Errorf("DetectCallType() = %v, want %v", got, tt.want)
			}
		})
	}
}

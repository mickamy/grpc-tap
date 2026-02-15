package proxy_test

import (
	"bytes"
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

package proxy_test

import (
	"fmt"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	"github.com/mickamy/grpc-tap/proxy"
)

func TestDetectProtocol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		want        proxy.Protocol
	}{
		{name: "gRPC", contentType: "application/grpc", want: proxy.ProtocolGRPC},
		{name: "gRPC+proto", contentType: "application/grpc+proto", want: proxy.ProtocolGRPC},
		{name: "gRPC-Web", contentType: "application/grpc-web", want: proxy.ProtocolGRPCWeb},
		{name: "gRPC-Web+proto", contentType: "application/grpc-web+proto", want: proxy.ProtocolGRPCWeb},
		{name: "Connect/proto", contentType: "application/proto", want: proxy.ProtocolConnect},
		{name: "Connect/json", contentType: "application/json", want: proxy.ProtocolConnect},
		{name: "Connect/connect+proto", contentType: "application/connect+proto", want: proxy.ProtocolConnect},
		{name: "empty", contentType: "", want: proxy.ProtocolConnect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _ := http.NewRequest(http.MethodPost, "/test.Service/Method", nil) //nolint:noctx // test code
			r.Header.Set("Content-Type", tt.contentType)
			got := proxy.DetectProtocol(r)
			if got != tt.want {
				t.Errorf("DetectProtocol(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestExtractStatus_Connect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		httpStatus int
		wantCode   int32
	}{
		{http.StatusOK, 0},
		{http.StatusBadRequest, int32(connect.CodeInternal)},
		{http.StatusUnauthorized, int32(connect.CodeUnauthenticated)},
		{http.StatusForbidden, int32(connect.CodePermissionDenied)},
		{http.StatusNotFound, int32(connect.CodeUnimplemented)},
		{http.StatusTooManyRequests, int32(connect.CodeUnavailable)},
		{http.StatusBadGateway, int32(connect.CodeUnavailable)},
		{http.StatusServiceUnavailable, int32(connect.CodeUnavailable)},
		{http.StatusGatewayTimeout, int32(connect.CodeUnavailable)},
		{http.StatusTeapot, int32(connect.CodeUnknown)},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("HTTP_%d", tt.httpStatus), func(t *testing.T) {
			t.Parallel()
			got, _ := proxy.ExtractStatus(proxy.ProtocolConnect, &http.Response{
				StatusCode: tt.httpStatus,
				Status:     http.StatusText(tt.httpStatus),
			})
			if got != tt.wantCode {
				t.Errorf("HTTP %d → gRPC code %d, want %d", tt.httpStatus, got, tt.wantCode)
			}
		})
	}
}

func TestExtractStatus_GRPC(t *testing.T) {
	t.Parallel()

	t.Run("from trailer", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			Header:  http.Header{},
			Trailer: http.Header{"Grpc-Status": {"13"}, "Grpc-Message": {"internal error"}},
		}
		code, msg := proxy.ExtractStatus(proxy.ProtocolGRPC, resp)
		if code != 13 {
			t.Errorf("code = %d, want 13", code)
		}
		if msg != "internal error" {
			t.Errorf("msg = %q, want %q", msg, "internal error")
		}
	})

	t.Run("from header", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			Header:  http.Header{"Grpc-Status": {"5"}, "Grpc-Message": {"not found"}},
			Trailer: http.Header{},
		}
		code, msg := proxy.ExtractStatus(proxy.ProtocolGRPC, resp)
		if code != 5 {
			t.Errorf("code = %d, want 5", code)
		}
		if msg != "not found" {
			t.Errorf("msg = %q, want %q", msg, "not found")
		}
	})

	t.Run("absent defaults to 0", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			Header:  http.Header{},
			Trailer: http.Header{},
		}
		code, msg := proxy.ExtractStatus(proxy.ProtocolGRPC, resp)
		if code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
		if msg != "" {
			t.Errorf("msg = %q, want empty", msg)
		}
	})

	t.Run("gRPC-Web uses same extraction", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			Header:  http.Header{},
			Trailer: http.Header{"Grpc-Status": {"7"}, "Grpc-Message": {"permission denied"}},
		}
		code, msg := proxy.ExtractStatus(proxy.ProtocolGRPCWeb, resp)
		if code != 7 {
			t.Errorf("code = %d, want 7", code)
		}
		if msg != "permission denied" {
			t.Errorf("msg = %q, want %q", msg, "permission denied")
		}
	})
}

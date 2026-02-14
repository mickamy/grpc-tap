package proxy_test

import (
	"testing"

	"github.com/mickamy/grpc-tap/proxy"
)

func TestCallType_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ct   proxy.CallType
		want string
	}{
		{name: "Unary", ct: proxy.Unary, want: "Unary"},
		{name: "ServerStream", ct: proxy.ServerStream, want: "ServerStream"},
		{name: "ClientStream", ct: proxy.ClientStream, want: "ClientStream"},
		{name: "BidiStream", ct: proxy.BidiStream, want: "BidiStream"},
		{name: "Unknown", ct: proxy.CallType(99), want: "UnknownCallType(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.ct.String(); got != tt.want {
				t.Errorf("CallType.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

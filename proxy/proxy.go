package proxy

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// CallType represents the gRPC call type.
type CallType int32

const (
	Unary        CallType = iota // Unary RPC
	ServerStream                 // Server-streaming RPC
	ClientStream                 // Client-streaming RPC
	BidiStream                   // Bidirectional-streaming RPC
)

func (c CallType) String() string {
	switch c {
	case Unary:
		return "Unary"
	case ServerStream:
		return "ServerStream"
	case ClientStream:
		return "ClientStream"
	case BidiStream:
		return "BidiStream"
	}
	return fmt.Sprintf("UnknownCallType(%d)", c)
}

// Protocol represents the wire protocol detected from the request.
type Protocol int32

const (
	ProtocolGRPC    Protocol = iota // Native gRPC (application/grpc)
	ProtocolGRPCWeb                 // gRPC-Web (application/grpc-web)
	ProtocolConnect                 // Connect protocol (application/proto, application/json, application/connect+*)
)

func (p Protocol) String() string {
	switch p {
	case ProtocolGRPC:
		return "gRPC"
	case ProtocolGRPCWeb:
		return "gRPC-Web"
	case ProtocolConnect:
		return "Connect"
	}
	return fmt.Sprintf("UnknownProtocol(%d)", p)
}

// MaxCaptureSize is the maximum number of bytes captured per body.
const MaxCaptureSize = 64 * 1024

// Event represents a captured gRPC call event.
type Event struct {
	ID              string
	Method          string // Full method name, e.g. "/package.Service/Method"
	CallType        CallType
	Protocol        Protocol
	StartTime       time.Time
	Duration        time.Duration
	Status          int32  // gRPC status code (codes.Code)
	Error           string // Error message, empty on success
	RequestHeaders  http.Header
	ResponseHeaders http.Header
	RequestBody     []byte // Captured request body (up to MaxCaptureSize)
	ResponseBody    []byte // Captured response body (up to MaxCaptureSize)
}

// Proxy is the interface for gRPC reverse proxies.
type Proxy interface {
	// ListenAndServe accepts client connections and relays them to the upstream gRPC server.
	ListenAndServe(ctx context.Context) error
	// Events returns the channel of captured events.
	Events() <-chan Event
	// Replay sends a request to the upstream server and returns the resulting event.
	Replay(ctx context.Context, method string, body []byte) (Event, error)
	// Close stops the proxy.
	Close() error
}

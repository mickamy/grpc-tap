package proxy

import (
	"context"
	"fmt"
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

// Event represents a captured gRPC call event.
type Event struct {
	ID        string
	Method    string // Full method name, e.g. "/package.Service/Method"
	CallType  CallType
	StartTime time.Time
	Duration  time.Duration
	Status    int32  // gRPC status code (codes.Code)
	Error     string // Error message, empty on success
}

// Proxy is the interface for gRPC reverse proxies.
type Proxy interface {
	// ListenAndServe accepts client connections and relays them to the upstream gRPC server.
	ListenAndServe(ctx context.Context) error
	// Events returns the channel of captured events.
	Events() <-chan Event
	// Close stops the proxy.
	Close() error
}

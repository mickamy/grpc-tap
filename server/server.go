package server

import (
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mickamy/grpc-tap/broker"
	tapv1 "github.com/mickamy/grpc-tap/gen/tap/v1"
	"github.com/mickamy/grpc-tap/proxy"
)

// Server exposes a gRPC TapService for TUI clients to connect to.
type Server struct {
	grpcServer *grpc.Server
}

// New creates a new Server backed by the given Broker.
func New(b *broker.Broker) *Server {
	gs := grpc.NewServer()
	svc := &tapService{broker: b}
	tapv1.RegisterTapServiceServer(gs, svc)

	return &Server{grpcServer: gs}
}

// Serve starts the gRPC server on the given listener.
func (s *Server) Serve(lis net.Listener) error {
	if err := s.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("server: serve: %w", err)
	}
	return nil
}

// Stop immediately stops the server.
func (s *Server) Stop() {
	s.grpcServer.Stop()
}

// GracefulStop gracefully stops the server.
func (s *Server) GracefulStop() {
	s.grpcServer.GracefulStop()
}

type tapService struct {
	tapv1.UnimplementedTapServiceServer

	broker *broker.Broker
}

func (s *tapService) Watch(_ *tapv1.WatchRequest, stream grpc.ServerStreamingServer[tapv1.WatchResponse]) error {
	ch, unsub := s.broker.Subscribe()
	defer unsub()

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("server: watch: %w", ctx.Err())
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&tapv1.WatchResponse{
				Event: eventToProto(ev),
			}); err != nil {
				return fmt.Errorf("server: watch send: %w", err)
			}
		}
	}
}

func eventToProto(ev proxy.Event) *tapv1.GRPCEvent {
	return &tapv1.GRPCEvent{
		Id:           ev.ID,
		Method:       ev.Method,
		CallType:     callTypeToProto(ev.CallType),
		StartTime:    timestamppb.New(ev.StartTime),
		Duration:     durationpb.New(ev.Duration),
		Status:       ev.Status,
		Error:        ev.Error,
		Protocol:     protocolToProto(ev.Protocol),
		RequestBody:  ev.RequestBody,
		ResponseBody: ev.ResponseBody,
	}
}

func callTypeToProto(ct proxy.CallType) tapv1.CallType {
	switch ct {
	case proxy.Unary:
		return tapv1.CallType_CALL_TYPE_UNARY
	case proxy.ServerStream:
		return tapv1.CallType_CALL_TYPE_SERVER_STREAM
	case proxy.ClientStream:
		return tapv1.CallType_CALL_TYPE_CLIENT_STREAM
	case proxy.BidiStream:
		return tapv1.CallType_CALL_TYPE_BIDI_STREAM
	default:
		return tapv1.CallType_CALL_TYPE_UNSPECIFIED
	}
}

func protocolToProto(p proxy.Protocol) tapv1.Protocol {
	switch p {
	case proxy.ProtocolGRPC:
		return tapv1.Protocol_PROTOCOL_GRPC
	case proxy.ProtocolGRPCWeb:
		return tapv1.Protocol_PROTOCOL_GRPC_WEB
	case proxy.ProtocolConnect:
		return tapv1.Protocol_PROTOCOL_CONNECT
	default:
		return tapv1.Protocol_PROTOCOL_UNSPECIFIED
	}
}

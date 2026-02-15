package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	echov1 "github.com/mickamy/grpc-tap/example/gen/echo/v1"
	"github.com/mickamy/grpc-tap/example/gen/echo/v1/echov1connect"
)

type echoServer struct{}

func (s *echoServer) Echo(
	_ context.Context,
	req *connect.Request[echov1.EchoRequest],
) (*connect.Response[echov1.EchoResponse], error) {
	if req.Msg.GetMessage() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("message must not be empty"))
	}
	return connect.NewResponse(&echov1.EchoResponse{
		Message: req.Msg.GetMessage(),
	}), nil
}

func (s *echoServer) Upper(
	_ context.Context,
	req *connect.Request[echov1.UpperRequest],
) (*connect.Response[echov1.UpperResponse], error) {
	if req.Msg.GetMessage() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("message must not be empty"))
	}
	return connect.NewResponse(&echov1.UpperResponse{
		Message: strings.ToUpper(req.Msg.GetMessage()),
	}), nil
}

func (s *echoServer) Reverse(
	_ context.Context,
	req *connect.Request[echov1.ReverseRequest],
) (*connect.Response[echov1.ReverseResponse], error) {
	msg := req.Msg.GetMessage()
	runes := []rune(msg)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return connect.NewResponse(&echov1.ReverseResponse{
		Message: string(runes),
	}), nil
}

func main() {
	mux := http.NewServeMux()
	path, handler := echov1connect.NewEchoServiceHandler(&echoServer{})
	mux.Handle(path, handler)

	addr := ":9000"
	log.Printf("echo server listening on %s", addr)
	//nolint:gosec // G114: example server
	if err := http.ListenAndServe(addr, h2c.NewHandler(mux, &http2.Server{})); err != nil {
		fmt.Printf("error: %v\n", err)
	}
}

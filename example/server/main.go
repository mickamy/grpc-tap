package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

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
	return connect.NewResponse(&echov1.EchoResponse{
		Message: req.Msg.GetMessage(),
	}), nil
}

func main() {
	mux := http.NewServeMux()
	path, handler := echov1connect.NewEchoServiceHandler(&echoServer{})
	mux.Handle(path, handler)

	addr := ":9000"
	log.Printf("echo server listening on %s", addr)
	if err := http.ListenAndServe(addr, h2c.NewHandler(mux, &http2.Server{})); err != nil {
		fmt.Printf("error: %v\n", err)
	}
}

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	echov1 "github.com/mickamy/grpc-tap/example/gen/echo/v1"
	"github.com/mickamy/grpc-tap/example/gen/echo/v1/echov1connect"
)

func main() {
	addr := "http://localhost:8080" // proxy address
	if a := os.Getenv("PROXY_ADDR"); a != "" {
		addr = a
	}

	if err := run(addr); err != nil {
		log.Fatal(err)
	}
}

func run(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	httpClient := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
	}

	grpcClient := echov1connect.NewEchoServiceClient(
		httpClient,
		addr,
		connect.WithGRPC(),
	)

	connectClient := echov1connect.NewEchoServiceClient(
		httpClient,
		addr,
	)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for i := 1; ; i++ {
		// gRPC protocol
		resp, err := grpcClient.Echo(ctx, connect.NewRequest(&echov1.EchoRequest{
			Message: fmt.Sprintf("hello from gRPC #%d", i),
		}))
		if err != nil {
			log.Printf("[gRPC] error: %v", err)
		} else {
			fmt.Printf("[gRPC]    echo: %s\n", resp.Msg.GetMessage())
		}

		// Connect protocol
		resp, err = connectClient.Echo(ctx, connect.NewRequest(&echov1.EchoRequest{
			Message: fmt.Sprintf("hello from Connect #%d", i),
		}))
		if err != nil {
			log.Printf("[Connect] error: %v", err)
		} else {
			fmt.Printf("[Connect] echo: %s\n", resp.Msg.GetMessage())
		}

		select {
		case <-ctx.Done():
			fmt.Println("shutting down")
			return nil
		case <-ticker.C:
		}
	}
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mickamy/grpc-tap/broker"
	"github.com/mickamy/grpc-tap/proxy"
	"github.com/mickamy/grpc-tap/server"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("grpc-tapd", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "grpc-tapd — gRPC proxy daemon for grpc-tap\n\nUsage:\n  grpc-tapd [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}

	listen := fs.String("listen", "", "client listen address (required)")
	upstream := fs.String("upstream", "", "upstream gRPC server address (required)")
	grpcAddr := fs.String("grpc", ":9090", "gRPC server address for TUI")
	showVersion := fs.Bool("version", false, "show version and exit")

	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("grpc-tapd %s\n", version)
		return
	}

	if *listen == "" || *upstream == "" {
		fs.Usage()
		os.Exit(1)
	}

	if err := run(*listen, *upstream, *grpcAddr); err != nil {
		log.Fatal(err)
	}
}

func run(listen, upstream, grpcAddr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Broker
	b := broker.New(256)

	// Reverse proxy
	p, err := proxy.New(listen, upstream)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}

	// gRPC server for TUI clients
	var lc net.ListenConfig
	grpcLis, err := lc.Listen(ctx, "tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen grpc %s: %w", grpcAddr, err)
	}
	srv := server.New(b, p)
	go func() {
		log.Printf("gRPC server listening on %s", grpcAddr)
		if err := srv.Serve(grpcLis); err != nil {
			log.Printf("grpc serve: %v", err)
		}
	}()

	go func() {
		for ev := range p.Events() {
			b.Publish(ev)
		}
	}()

	log.Printf("proxying %s -> %s", listen, upstream)
	if err := p.ListenAndServe(ctx); err != nil {
		return fmt.Errorf("proxy: %w", err)
	}

	srv.GracefulStop()
	return nil
}

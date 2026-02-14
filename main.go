package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	tapv1 "github.com/mickamy/grpc-tap/gen/tap/v1"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("grpc-tap", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "grpc-tap — Watch gRPC traffic in real-time\n\nUsage:\n  grpc-tap [flags] <addr>\n\nFlags:\n")
		fs.PrintDefaults()
	}

	showVersion := fs.Bool("version", false, "show version and exit")

	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("grpc-tap %s\n", version)
		return
	}

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	if err := watch(fs.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func watch(addr string) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client := tapv1.NewTapServiceClient(conn)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stream, err := client.Watch(ctx, &tapv1.WatchRequest{})
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	fmt.Println("Watching gRPC traffic... (Ctrl+C to quit)")

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("recv: %w", err)
		}

		ev := resp.GetEvent()
		dur := ev.GetDuration().AsDuration()
		ts := ev.GetStartTime().AsTime().Format(time.TimeOnly)

		status := "OK"
		if ev.GetStatus() != 0 {
			status = fmt.Sprintf("ERR(%d)", ev.GetStatus())
		}
		if ev.GetError() != "" {
			status = fmt.Sprintf("ERR(%d: %s)", ev.GetStatus(), ev.GetError())
		}

		fmt.Printf("%s  %-8s  %-10s  %8s  %s\n",
			ts,
			ev.GetProtocol().String(),
			status,
			formatDuration(dur),
			ev.GetMethod(),
		)
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

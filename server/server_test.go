package server_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/mickamy/grpc-tap/broker"
	tapv1 "github.com/mickamy/grpc-tap/gen/tap/v1"
	"github.com/mickamy/grpc-tap/proxy"
	"github.com/mickamy/grpc-tap/server"
)

func startServer(t *testing.T, b *broker.Broker) tapv1.TapServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := server.New(b)
	t.Cleanup(srv.Stop)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("serve: %v", err)
		}
	}()

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return tapv1.NewTapServiceClient(conn)
}

// waitForSubscriber polls until the broker has at least one subscriber.
// This avoids the race between client.Watch() returning and the server-side
// handler calling broker.Subscribe().
func waitForSubscriber(t *testing.T, b *broker.Broker) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for b.SubscriberCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for subscriber")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestWatch(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	b := broker.New(8)
	client := startServer(t, b)

	stream, err := client.Watch(ctx, &tapv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}

	waitForSubscriber(t, b)

	ev := proxy.Event{
		ID:        "test-1",
		Method:    "/test.Service/Hello",
		CallType:  proxy.Unary,
		StartTime: time.Now(),
		Duration:  42 * time.Millisecond,
		Status:    0,
	}
	b.Publish(ev)

	resp, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}

	got := resp.GetEvent()
	if got.GetId() != ev.ID {
		t.Errorf("ID = %q, want %q", got.GetId(), ev.ID)
	}
	if got.GetMethod() != ev.Method {
		t.Errorf("Method = %q, want %q", got.GetMethod(), ev.Method)
	}
	if got.GetCallType() != tapv1.CallType_CALL_TYPE_UNARY {
		t.Errorf("CallType = %v, want CALL_TYPE_UNARY", got.GetCallType())
	}
	if got.GetStatus() != ev.Status {
		t.Errorf("Status = %d, want %d", got.GetStatus(), ev.Status)
	}
}

func TestWatch_MultipleEvents(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	b := broker.New(8)
	client := startServer(t, b)

	stream, err := client.Watch(ctx, &tapv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}

	waitForSubscriber(t, b)

	for i := range 3 {
		b.Publish(proxy.Event{
			ID:     fmt.Sprintf("ev-%d", i),
			Method: "/test.Service/Hello",
		})
	}

	for i := range 3 {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		want := fmt.Sprintf("ev-%d", i)
		if got := resp.GetEvent().GetId(); got != want {
			t.Errorf("event[%d] ID = %q, want %q", i, got, want)
		}
	}
}

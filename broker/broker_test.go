package broker_test

import (
	"testing"
	"time"

	"github.com/mickamy/grpc-tap/broker"
	"github.com/mickamy/grpc-tap/proxy"
)

func TestBroker_PublishSubscribe(t *testing.T) {
	t.Parallel()

	b := broker.New(8)
	ch, unsub := b.Subscribe()
	defer unsub()

	ev := proxy.Event{
		ID:     "1",
		Method: "/test.Service/Method",
	}
	b.Publish(ev)

	select {
	case got := <-ch:
		if got.ID != ev.ID {
			t.Errorf("got ID %q, want %q", got.ID, ev.ID)
		}
		if got.Method != ev.Method {
			t.Errorf("got Method %q, want %q", got.Method, ev.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBroker_MultipleSubscribers(t *testing.T) {
	t.Parallel()

	b := broker.New(8)

	ch1, unsub1 := b.Subscribe()
	defer unsub1()
	ch2, unsub2 := b.Subscribe()
	defer unsub2()

	ev := proxy.Event{ID: "1", Method: "/test.Service/Method"}
	b.Publish(ev)

	for i, ch := range []<-chan proxy.Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got.ID != ev.ID {
				t.Errorf("subscriber %d: got ID %q, want %q", i, got.ID, ev.ID)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for event", i)
		}
	}
}

func TestBroker_Unsubscribe(t *testing.T) {
	t.Parallel()

	b := broker.New(8)
	_, unsub := b.Subscribe()

	if got := b.SubscriberCount(); got != 1 {
		t.Fatalf("SubscriberCount() = %d, want 1", got)
	}

	unsub()

	if got := b.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() after unsub = %d, want 0", got)
	}

	// idempotent
	unsub()

	if got := b.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() after double unsub = %d, want 0", got)
	}
}

func TestBroker_DropOnFullBuffer(t *testing.T) {
	t.Parallel()

	b := broker.New(1)
	ch, unsub := b.Subscribe()
	defer unsub()

	// Fill the buffer.
	b.Publish(proxy.Event{ID: "1"})
	// This should be dropped without blocking.
	b.Publish(proxy.Event{ID: "2"})

	select {
	case got := <-ch:
		if got.ID != "1" {
			t.Errorf("got ID %q, want %q", got.ID, "1")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}

	select {
	case got := <-ch:
		t.Fatalf("unexpected event: %+v", got)
	default:
		// expected: buffer was full, second event dropped
	}
}

package bus

import (
	"sync"
	"testing"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// testEvent is a simple event implementation for testing
type testEvent struct {
	eventType event.EventType
	payload   string
}

func (e *testEvent) Type() event.EventType {
	return e.eventType
}

func TestEventBus_PublishSubscribe(t *testing.T) {
	bus := New()
	defer bus.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	received := make(chan event.Event, 1)
	processor := func(e event.Event) {
		received <- e
		wg.Done()
	}

	// Subscribe to FSRead events
	err := bus.Subscribe(event.EventTypeFSRead, processor)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Publish an event
	testEvt := &testEvent{
		eventType: event.EventTypeFSRead,
		payload:   "test payload",
	}
	bus.Publish(testEvt)

	// Wait for event to be processed with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for event to be processed")
	}

	// Verify the received event
	select {
	case evt := <-received:
		if evt.Type() != event.EventTypeFSRead {
			t.Errorf("Expected event type %v, got %v", event.EventTypeFSRead, evt.Type())
		}
		if testEvt, ok := evt.(*testEvent); ok {
			if testEvt.payload != "test payload" {
				t.Errorf("Expected payload 'test payload', got '%s'", testEvt.payload)
			}
		}
	default:
		t.Error("No event received")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	bus := New()
	defer bus.Close()

	const numSubscribers = 5
	var wg sync.WaitGroup
	wg.Add(numSubscribers)

	receivedCount := make(chan int, numSubscribers)

	// Subscribe multiple processors to the same event type
	for i := 0; i < numSubscribers; i++ {
		subscriberID := i
		processor := func(e event.Event) {
			receivedCount <- subscriberID
			wg.Done()
		}
		err := bus.Subscribe(event.EventTypeFSWrite, processor)
		if err != nil {
			t.Fatalf("Subscribe failed for subscriber %d: %v", i, err)
		}
	}

	// Publish a single event
	testEvt := &testEvent{
		eventType: event.EventTypeFSWrite,
		payload:   "multi-subscriber test",
	}
	bus.Publish(testEvt)

	// Wait for all subscribers to process with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for all subscribers to process event")
	}

	// Verify all subscribers received the event
	close(receivedCount)
	received := make(map[int]bool)
	for id := range receivedCount {
		received[id] = true
	}

	if len(received) != numSubscribers {
		t.Errorf("Expected %d subscribers to receive event, got %d", numSubscribers, len(received))
	}
}

func TestEventBus_MultipleEventTypes(t *testing.T) {
	bus := New()
	defer bus.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	receivedFSRead := make(chan bool, 1)
	receivedFSWrite := make(chan bool, 1)

	// Subscribe to FSRead events
	processorRead := func(e event.Event) {
		if e.Type() == event.EventTypeFSRead {
			receivedFSRead <- true
		}
		wg.Done()
	}
	err := bus.Subscribe(event.EventTypeFSRead, processorRead)
	if err != nil {
		t.Fatalf("Subscribe to FSRead failed: %v", err)
	}

	// Subscribe to FSWrite events
	processorWrite := func(e event.Event) {
		if e.Type() == event.EventTypeFSWrite {
			receivedFSWrite <- true
		}
		wg.Done()
	}
	err = bus.Subscribe(event.EventTypeFSWrite, processorWrite)
	if err != nil {
		t.Fatalf("Subscribe to FSWrite failed: %v", err)
	}

	// Publish both event types
	bus.Publish(&testEvent{eventType: event.EventTypeFSRead, payload: "read"})
	bus.Publish(&testEvent{eventType: event.EventTypeFSWrite, payload: "write"})

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for events to be processed")
	}

	// Verify both events were received
	select {
	case <-receivedFSRead:
	case <-time.After(100 * time.Millisecond):
		t.Error("FSRead event not received")
	}

	select {
	case <-receivedFSWrite:
	case <-time.After(100 * time.Millisecond):
		t.Error("FSWrite event not received")
	}
}

func TestEventBus_SubscriberIsolation(t *testing.T) {
	bus := New()
	defer bus.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	wrongEventReceived := false
	var mu sync.Mutex

	// Subscribe only to FSRead events
	processor := func(e event.Event) {
		mu.Lock()
		defer mu.Unlock()
		if e.Type() != event.EventTypeFSRead {
			wrongEventReceived = true
		}
		wg.Done()
	}
	err := bus.Subscribe(event.EventTypeFSRead, processor)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Publish FSWrite event (should not be received)
	bus.Publish(&testEvent{eventType: event.EventTypeFSWrite, payload: "should not receive"})

	// Wait a bit to ensure it's not received
	time.Sleep(100 * time.Millisecond)

	// Publish FSRead event (should be received)
	bus.Publish(&testEvent{eventType: event.EventTypeFSRead, payload: "should receive"})

	// Wait for the correct event
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for event to be processed")
	}

	mu.Lock()
	defer mu.Unlock()
	if wrongEventReceived {
		t.Error("Subscriber received event of wrong type")
	}
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := New()
	defer bus.Close()

	eventCount := 0
	var mu sync.Mutex

	processor := func(e event.Event) {
		mu.Lock()
		eventCount++
		mu.Unlock()
	}

	// Subscribe
	err := bus.Subscribe(event.EventTypeLibrary, processor)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Publish first event
	bus.Publish(&testEvent{eventType: event.EventTypeLibrary, payload: "first"})
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	firstCount := eventCount
	mu.Unlock()

	if firstCount != 1 {
		t.Errorf("Expected 1 event received, got %d", firstCount)
	}

	// Unsubscribe
	err = bus.Unsubscribe(event.EventTypeLibrary, processor)
	if err != nil {
		t.Fatalf("Unsubscribe failed: %v", err)
	}

	// Publish second event (should not be received)
	bus.Publish(&testEvent{eventType: event.EventTypeLibrary, payload: "second"})
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	finalCount := eventCount
	mu.Unlock()

	if finalCount != 1 {
		t.Errorf("Expected event count to remain 1 after unsubscribe, got %d", finalCount)
	}
}

func TestEventBus_ConcurrentPublish(t *testing.T) {
	bus := New()
	defer bus.Close()

	const numGoroutines = 10
	const eventsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines * eventsPerGoroutine)

	eventCount := 0
	var mu sync.Mutex

	processor := func(e event.Event) {
		mu.Lock()
		eventCount++
		mu.Unlock()
		wg.Done()
	}

	err := bus.Subscribe(event.EventTypeTlsPayloadSend, processor)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Publish events concurrently from multiple goroutines
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < eventsPerGoroutine; j++ {
				bus.Publish(&testEvent{
					eventType: event.EventTypeTlsPayloadSend,
					payload:   "concurrent",
				})
			}
		}(i)
	}

	// Wait for all events to be processed with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		mu.Lock()
		count := eventCount
		mu.Unlock()
		t.Fatalf("Timeout waiting for concurrent events. Received %d/%d events", count, numGoroutines*eventsPerGoroutine)
	}

	mu.Lock()
	finalCount := eventCount
	mu.Unlock()

	expectedCount := numGoroutines * eventsPerGoroutine
	if finalCount != expectedCount {
		t.Errorf("Expected %d events, got %d", expectedCount, finalCount)
	}
}

func TestEventBus_Close(t *testing.T) {
	bus := New()

	processor := func(e event.Event) {
		// No-op
	}

	err := bus.Subscribe(event.EventTypeHttpRequest, processor)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Close should not panic
	bus.Close()

	// Publishing after close should not panic
	// (behavior is implementation-specific, but should not crash)
	bus.Publish(&testEvent{eventType: event.EventTypeHttpRequest, payload: "after close"})

	// Give it time to process
	time.Sleep(100 * time.Millisecond)
}

func TestEventBus_NoSubscribers(t *testing.T) {
	bus := New()
	defer bus.Close()

	// Publishing to an event type with no subscribers should not panic
	bus.Publish(&testEvent{eventType: event.EventTypeHttpResponse, payload: "no subscribers"})

	// Give it time
	time.Sleep(100 * time.Millisecond)
}

func TestEventBus_SubscribeError(t *testing.T) {
	bus := New()
	defer bus.Close()

	// Test that Subscribe returns an error (or nil) consistently
	// This tests the contract of the interface
	processor := func(e event.Event) {}

	err := bus.Subscribe(event.EventTypeMCPMessage, processor)
	if err != nil {
		// If Subscribe can return errors, that's fine
		t.Logf("Subscribe returned error (acceptable): %v", err)
	}
}

func TestEventBus_UnsubscribeError(t *testing.T) {
	bus := New()
	defer bus.Close()

	processor := func(e event.Event) {}

	// Unsubscribe without subscribing first
	err := bus.Unsubscribe(event.EventTypeHttpSSE, processor)
	if err != nil {
		// If Unsubscribe can return errors, that's fine
		t.Logf("Unsubscribe returned error (acceptable): %v", err)
	}
}

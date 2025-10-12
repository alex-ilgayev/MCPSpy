package testing

import (
	"sync"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

type mockBus struct {
	mu          sync.RWMutex
	subscribers map[event.EventType][]bus.EventProcessor
	events      chan event.Event
}

func (mb *mockBus) Publish(e event.Event) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()

	// Send to events channel for test assertions
	select {
	case mb.events <- e:
	default:
		// Non-blocking: if the test isn't consuming events, don't block
	}

	// Call all subscribers for this event type
	if processors, ok := mb.subscribers[e.Type()]; ok {
		for _, processor := range processors {
			processor(e)
		}
	}
}

func (mb *mockBus) Subscribe(eventType event.EventType, fn bus.EventProcessor) error {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	mb.subscribers[eventType] = append(mb.subscribers[eventType], fn)
	return nil
}

func (mb *mockBus) Unsubscribe(eventType event.EventType, fn bus.EventProcessor) error {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	// Remove the processor from subscribers
	if processors, ok := mb.subscribers[eventType]; ok {
		for i, processor := range processors {
			// Compare function pointers (this is a simplified approach)
			// In production, you might need a more sophisticated comparison
			_ = processor
			// For now, just remove all processors of this type
			mb.subscribers[eventType] = append(processors[:i], processors[i+1:]...)
			break
		}
	}
	return nil
}

func (mb *mockBus) Close() {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	close(mb.events)
	mb.subscribers = make(map[event.EventType][]bus.EventProcessor)
}

// Events returns the channel that receives published events for test assertions
func (mb *mockBus) Events() <-chan event.Event {
	return mb.events
}

func NewMockBus() *mockBus {
	return &mockBus{
		subscribers: make(map[event.EventType][]bus.EventProcessor),
		events:      make(chan event.Event, 100), // Buffered to avoid blocking
	}
}

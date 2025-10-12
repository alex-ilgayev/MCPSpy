package bus

import (
	"fmt"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
	evbus "github.com/asaskevich/EventBus"
)

type EventProcessor func(e event.Event)

// EventBus is a thread-safe, publish/subscribe system.
// Using github.com/asaskevich/EventBus behind the scenes.
type EventBus interface {
	Publish(e event.Event)
	Subscribe(eventType event.EventType, fn EventProcessor) error
	Unsubscribe(eventType event.EventType, fn EventProcessor) error
	Close()
}

type eventBus struct {
	bus evbus.Bus
}

// New creates a new EventBus.
func New() EventBus {
	return &eventBus{
		bus: evbus.New(),
	}
}

// Publish sends an event to all registered subscribers for that event type.
// The processing is done asynchronously in separate goroutines.
func (b *eventBus) Publish(e event.Event) {
	topic := fmt.Sprintf("topic:%s", e.Type().String())

	b.bus.Publish(topic, e)
}

// Subscribe registers an EventProcessor to receive events it's interested in.
func (b *eventBus) Subscribe(eventType event.EventType, fn EventProcessor) error {
	topic := fmt.Sprintf("topic:%s", eventType.String())

	return b.bus.SubscribeAsync(topic, fn, false)
}

// Unsubscribe removes a previously registered EventProcessor for a specific event type.
func (b *eventBus) Unsubscribe(eventType event.EventType, fn EventProcessor) error {
	topic := fmt.Sprintf("topic:%s", eventType.String())
	return b.bus.Unsubscribe(topic, fn)
}

// Close cleans up the EventBus resources.
func (b *eventBus) Close() {
	// No explicit close method in the underlying library,
	b.bus = evbus.New()
}

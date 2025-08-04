//go:build userland

package ebpf

import (
	"context"
	"fmt"
)

// Loader is a stub implementation for userland builds
type Loader struct {
	eventCh chan Event
}

// New creates a stub eBPF loader for userland mode
func New(debug bool) (*Loader, error) {
	return &Loader{
		eventCh: make(chan Event, 100),
	}, nil
}

// Load is a no-op for userland mode
func (l *Loader) Load() error {
	return fmt.Errorf("eBPF mode not available in userland build")
}

// Events returns the events channel
func (l *Loader) Events() <-chan Event {
	return l.eventCh
}

// Start is a no-op for userland mode
func (l *Loader) Start(ctx context.Context) error {
	return fmt.Errorf("eBPF mode not available in userland build")
}

// Close cleans up resources
func (l *Loader) Close() error {
	close(l.eventCh)
	return nil
}

// RunIterLibEnum is a no-op for userland mode
func (l *Loader) RunIterLibEnum() error {
	return fmt.Errorf("eBPF iterator not available in userland build")
}
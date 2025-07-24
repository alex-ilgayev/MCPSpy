package ebpf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/sirupsen/logrus"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -cc clang -cflags "-D__TARGET_ARCH_x86" mcpspy ../../bpf/mcpspy.c

// Loader manages eBPF program lifecycle
type Loader struct {
	objs    *mcpspyObjects
	links   []link.Link
	reader  *ringbuf.Reader
	eventCh chan Event
	debug   bool
}

// New creates a new eBPF loader
func New(debug bool) (*Loader, error) {
	// Remove memory limit for eBPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("failed to remove memlock: %w", err)
	}

	return &Loader{
		eventCh: make(chan Event, 1000),
		debug:   debug,
	}, nil
}

// Load attaches eBPF programs to kernel
func (l *Loader) Load() error {
	// Load pre-compiled eBPF objects
	objs := &mcpspyObjects{}
	if err := loadMcpspyObjects(objs, nil); err != nil {
		var verifierError *ebpf.VerifierError
		if errors.As(err, &verifierError) && logrus.IsLevelEnabled(logrus.DebugLevel) {
			fmt.Fprintln(os.Stderr, strings.Join(verifierError.Log, "\n"))
		}
		return fmt.Errorf("failed to load eBPF objects: %w", err)
	}
	l.objs = objs

	// Attaching exit_vfs_read with Fexit
	readEnterLink, err := link.AttachTracing(link.TracingOptions{
		Program:    l.objs.ExitVfsRead,
		AttachType: ebpf.AttachTraceFExit,
	})
	if err != nil {
		return fmt.Errorf("failed to attach %s tracepoint: %w", l.objs.ExitVfsRead.String(), err)
	}
	l.links = append(l.links, readEnterLink)

	// Attaching exit_vfs_write with Fexit
	readExitLink, err := link.AttachTracing(link.TracingOptions{
		Program:    l.objs.ExitVfsWrite,
		AttachType: ebpf.AttachTraceFExit,
	})
	if err != nil {
		return fmt.Errorf("failed to attach %s tracepoint: %w", l.objs.ExitVfsWrite.String(), err)
	}
	l.links = append(l.links, readExitLink)

	// Open ring buffer reader
	reader, err := ringbuf.NewReader(l.objs.Events)
	if err != nil {
		return fmt.Errorf("failed to create ring buffer reader: %w", err)
	}
	l.reader = reader

	logrus.Debug("eBPF programs loaded and attached successfully")
	return nil
}

// Events returns a channel for receiving events
func (l *Loader) Events() <-chan Event {
	return l.eventCh
}

// Start begins reading events from the ring buffer
func (l *Loader) Start(ctx context.Context) error {
	if l.reader == nil {
		return fmt.Errorf("loader not loaded")
	}

	go func() {
		defer close(l.eventCh)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				record, err := l.reader.Read()
				if err != nil {
					if errors.Is(err, os.ErrClosed) {
						logrus.Debug("Ring buffer closed, exiting")
						return
					}

					logrus.WithError(err).Error("Failed to read from ring buffer")
					continue
				}

				if len(record.RawSample) < int(unsafe.Sizeof(Event{})) {
					logrus.Warn("Received incomplete event")
					continue
				}

				var event Event
				reader := bytes.NewReader(record.RawSample)
				if err := binary.Read(reader, binary.LittleEndian, &event); err != nil {
					logrus.WithError(err).Error("Failed to parse event")
					continue
				}

				select {
				case l.eventCh <- event:
				case <-ctx.Done():
					return
				default:
					logrus.Warn("Event channel full, dropping event")
				}
			}
		}
	}()

	return nil
}

// Close cleans up resources
func (l *Loader) Close() error {
	var errs []error

	// Close ring buffer reader
	if l.reader != nil {
		if err := l.reader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close ring buffer reader: %w", err))
		}
	}

	// Detach all links
	for _, link := range l.links {
		if err := link.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close link: %w", err))
		}
	}

	// Close eBPF objects
	if l.objs != nil {
		if err := l.objs.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close eBPF objects: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during cleanup: %v", errs)
	}

	logrus.Debug("eBPF loader cleaned up successfully")
	return nil
}

package ebpf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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

	// Iterator link for library enumeration
	// Will be != nil if enumeration is ongoing
	iterLink link.Link

	// ecapture integration
	ecapture *ecaptureManager
}

// New creates a new eBPF loader
func New(debug bool) (*Loader, error) {
	// Remove memory limit for eBPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("failed to remove memlock: %w", err)
	}

	// Create event channel
	// approximately maximum of 25-100MB memory.
	eventCh := make(chan Event, 100000)

	// Create ecapture manager
	// based on popular tls inspection tool:
	// https://github.com/gojue/ecapture
	ecapture, err := newEcaptureManager(eventCh)
	if err != nil {
		return nil, fmt.Errorf("failed to create ecapture manager: %w", err)
	}

	return &Loader{
		eventCh:  eventCh,
		debug:    debug,
		ecapture: ecapture,
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

	// Start ecapture
	if err := l.ecapture.start(ctx); err != nil {
		return fmt.Errorf("failed to start ecapture: %w", err)
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

				// First, reading the first byte to get the event type
				if len(record.RawSample) < 1 {
					logrus.Warn("Received empty event. Can't be read the event type.")
					continue
				}

				eventType := EventType(record.RawSample[0])

				var event Event
				reader := bytes.NewReader(record.RawSample)

				switch eventType {
				case EventTypeRead, EventTypeWrite:
					if len(record.RawSample) < int(unsafe.Sizeof(DataEvent{})) {
						logrus.Warn("Received incomplete data event for data event")
						continue
					}

					var dataEvent DataEvent
					if err := binary.Read(reader, binary.LittleEndian, &dataEvent); err != nil {
						logrus.WithError(err).Error("Failed to parse data event")
						continue
					}
					event = &dataEvent
				case EventTypeLibrary:
					if len(record.RawSample) < int(unsafe.Sizeof(LibraryEvent{})) {
						logrus.Warn("Received incomplete library event")
						continue
					}

					var libraryEvent LibraryEvent
					if err := binary.Read(reader, binary.LittleEndian, &libraryEvent); err != nil {
						logrus.WithError(err).Error("Failed to parse library event")
						continue
					}
					event = &libraryEvent
				default:
					logrus.WithField("type", eventType).Warn("Unknown event type")
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

	if l.ecapture != nil {
		if err := l.ecapture.stop(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop ecapture: %w", err))
		}
	}

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

	// Detach iterator link
	if l.iterLink != nil {
		if err := l.iterLink.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close iterator link: %w", err))
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

// RunIterLibEnum triggers the eBPF iterator to enumerate all loaded libraries.
// The discovered libraries will be sent to the event channel.
func (l *Loader) RunIterLibEnum() error {
	if l.objs == nil {
		return fmt.Errorf("loader not loaded")
	}

	// Create iterator link
	iterLink, err := link.AttachIter(link.IterOptions{
		Program: l.objs.EnumerateLoadedModules,
	})
	if err != nil {
		return fmt.Errorf("failed to attach iterator: %w", err)
	}
	defer iterLink.Close()

	// Store the iterator link
	// so we'll be able to close it during Close()
	l.iterLink = iterLink
	defer func() {
		l.iterLink = nil
	}()

	// Open the iterator to get a file descriptor
	iter, err := iterLink.Open()
	if err != nil {
		return fmt.Errorf("failed to open iterator: %w", err)
	}
	defer iter.Close()

	// Trigger the iterator by reading from it
	buf := make([]byte, 1)
	_, err = iter.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read from iterator: %w", err)
	}

	return nil
}

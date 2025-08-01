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

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -cc clang -cflags "-D__TARGET_ARCH_x86" mcpspy_bpfel_x86 ../../bpf/mcpspy.c
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target arm64 -cc clang -cflags "-D__TARGET_ARCH_arm64" mcpspy_bpfel_arm64 ../../bpf/mcpspy.c

// Loader manages eBPF program lifecycle
type Loader struct {
	objs    *archObjects
	links   []link.Link
	reader  *ringbuf.Reader
	eventCh chan Event
	debug   bool

	// Iterator link for library enumeration
	// Will be != nil if enumeration is ongoing
	iterLink link.Link
}

// New creates a new eBPF loader
func New(debug bool) (*Loader, error) {
	// Remove the memory limit for eBPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("failed to remove memlock: %w", err)
	}

	return &Loader{
		// approximately maximum of 25-100MB memory.
		eventCh: make(chan Event, 100000),
		debug:   debug,
	}, nil
}

// Load attaches eBPF programs to kernel
func (l *Loader) Load() error {
	// Load pre-compiled eBPF objects
	objs := &archObjects{}
	if err := loadArchObjects(objs, nil); err != nil {
		var verifierError *ebpf.VerifierError
		if errors.As(err, &verifierError) && logrus.IsLevelEnabled(logrus.DebugLevel) {
			if _, err := fmt.Fprintln(os.Stderr, strings.Join(verifierError.Log, "\n")); err != nil {
				logrus.WithError(err).Warn("Failed to write verifier log to stderr")
			}
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

	// Attaching trace_security_file_open with Fentry to track dynamic library loading
	securityFileOpenLink, err := link.AttachTracing(link.TracingOptions{
		Program:    l.objs.TraceSecurityFileOpen,
		AttachType: ebpf.AttachTraceFEntry,
	})
	if err != nil {
		return fmt.Errorf("failed to attach %s tracepoint: %w", l.objs.TraceSecurityFileOpen.String(), err)
	}
	l.links = append(l.links, securityFileOpenLink)

	// Open the ring buffer reader
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

				// First, reading the first byte to get the event type
				if len(record.RawSample) < 1 {
					logrus.Warn("Received empty event. Can't be read the event type.")
					continue
				}

				eventType := EventType(record.RawSample[0])

				var event Event
				reader := bytes.NewReader(record.RawSample)

				switch eventType {
				case EventTypeFSRead, EventTypeFSWrite:
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
				case EventTypeTlsSend, EventTypeTlsRecv:
					if len(record.RawSample) < int(unsafe.Sizeof(TlsEvent{})) {
						logrus.Warn("Received incomplete TLS event")
						continue
					}

					var tlsEvent TlsEvent
					if err := binary.Read(reader, binary.LittleEndian, &tlsEvent); err != nil {
						logrus.WithError(err).Error("Failed to parse TLS event")
						continue
					}
					event = &tlsEvent
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

	// Close ring buffer reader
	if l.reader != nil {
		if err := l.reader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close ring buffer reader: %w", err))
		}
	}

	// Detach all links
	for _, programLink := range l.links {
		if err := programLink.Close(); err != nil {
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

// AttachSSLProbes attaches SSL read/write probes to a specific library
func (l *Loader) AttachSSLProbes(libraryPath string) error {
	if l.objs == nil {
		return fmt.Errorf("loader not loaded")
	}

	// Open the executable/library
	ex, err := link.OpenExecutable(libraryPath)
	if err != nil {
		return fmt.Errorf("failed to open executable %s: %w", libraryPath, err)
	}

	// Attach SSL_read entry uprobe
	sslReadEntryLink, err := ex.Uprobe("SSL_read", l.objs.SslReadEntry, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_read entry uprobe: %w", err)
	}
	l.links = append(l.links, sslReadEntryLink)

	// Attach SSL_read exit uretprobe
	sslReadExitLink, err := ex.Uretprobe("SSL_read", l.objs.SslReadExit, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_read exit uretprobe: %w", err)
	}
	l.links = append(l.links, sslReadExitLink)

	// Attach SSL_write uprobe
	sslWriteLink, err := ex.Uprobe("SSL_write", l.objs.SslWriteEntry, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_write uprobe: %w", err)
	}
	l.links = append(l.links, sslWriteLink)

	// Attach SSL_read_ex entry uprobe
	sslReadExEntryLink, err := ex.Uprobe("SSL_read_ex", l.objs.SslReadExEntry, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_read_ex entry uprobe: %w", err)
	}
	l.links = append(l.links, sslReadExEntryLink)

	// Attach SSL_read_ex exit uretprobe
	sslReadExExitLink, err := ex.Uretprobe("SSL_read_ex", l.objs.SslReadExExit, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_read_ex exit uretprobe: %w", err)
	}
	l.links = append(l.links, sslReadExExitLink)

	// Attach SSL_write_ex uprobe
	sslWriteExLink, err := ex.Uprobe("SSL_write_ex", l.objs.SslWriteExEntry, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_write_ex uprobe: %w", err)
	}
	l.links = append(l.links, sslWriteExLink)

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

package ebpf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"github.com/alex-ilgayev/mcpspy/pkg/discovery"
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
}

// New creates a new eBPF loader
func New() (*Loader, error) {
	// Remove memory limit for eBPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("failed to remove memlock: %w", err)
	}

	return &Loader{
		eventCh: make(chan Event, 1000),
	}, nil
}

// Load attaches eBPF programs to kernel
func (l *Loader) Load() error {
	// Load pre-compiled eBPF objects
	objs := &mcpspyObjects{}
	if err := loadMcpspyObjects(objs, nil); err != nil {
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

	fmt.Println("Discovering SSL targets")
	// Discover SSL targets
	discoverer := discovery.NewSSLDiscovery()
	targets, err := discoverer.Discover()
	if err != nil {
		fmt.Println("Failed to discover SSL targets")
		return fmt.Errorf("failed to discover SSL targets: %w", err)
	}

	fmt.Println(targets)

	if len(targets) == 0 {
		logrus.Warn("No SSL targets found")
		return nil
	}
	logrus.Debugf("Found %d SSL targets", len(targets))

	// Attach to each target
	for _, target := range targets {
		if err := l.attachToTarget(target); err != nil {
			logrus.WithError(err).Warnf("Failed to attach to target %s", target.Path)
			continue
		}
	}
	logrus.Debugf("SSL eBPF programs loaded and attached to %d targets", len(l.links))
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

	go l.readFromRingBuffer(ctx, l.reader, "stdio")

	return nil
}

// readFromRingBuffer reads events from a specific ring buffer
func (l *Loader) readFromRingBuffer(ctx context.Context, reader *ringbuf.Reader, source string) {
	defer func() {
		if source == "stdio" {
			close(l.eventCh)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			record, err := reader.Read()
			if err != nil {
				if errors.Is(err, os.ErrClosed) {
					logrus.Debugf("%s ring buffer closed, exiting", source)
					return
				}

				logrus.WithError(err).Errorf("Failed to read from %s ring buffer", source)
				continue
			}

			if len(record.RawSample) < int(unsafe.Sizeof(Event{})) {
				logrus.Warnf("Received incomplete event from %s", source)
				continue
			}

			var event Event
			reader := bytes.NewReader(record.RawSample)
			if err := binary.Read(reader, binary.LittleEndian, &event); err != nil {
				logrus.WithError(err).Errorf("Failed to parse event from %s", source)
				continue
			}

			select {
			case l.eventCh <- event:
			case <-ctx.Done():
				return
			default:
				logrus.Warnf("Event channel full, dropping %s event", source)
			}
		}
	}
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

// attachToTarget attaches uprobes to a specific SSL target
func (l *Loader) attachToTarget(target discovery.SSLTarget) error {
	// For static binaries, we need to find the symbol offset
	// For dynamic libraries, we can use the symbol name directly

	var ex *link.Executable
	var err error

	if target.Type == discovery.SSLTypeDynamic {
		// For dynamic libraries, open as shared library
		ex, err = link.OpenExecutable(target.Path)
	} else {
		// For static binaries, open as executable
		ex, err = link.OpenExecutable(target.Path)
	}

	if err != nil {
		return fmt.Errorf("failed to open executable %s: %w", target.Path, err)
	}

	// Attach SSL_read uprobe
	// readOpts := &link.UprobeOptions{
	// 	Address: 0,
	// 	PID:     int(target.PID), // 0 means system-wide
	// }

	// Try to attach to SSL_read
	// readLink, err := ex.Uprobe("SSL_read", l.objs.UprobeSslRead, readOpts)
	// if err != nil {
	// 	// For static binaries, the symbol might be stripped or mangled
	// 	// Try some common variations
	// 	if target.Type == discovery.SSLTypeStatic {
	// 		// Try without symbol name (would need offset)
	// 		logrus.Debugf("Failed to find SSL_read symbol in %s, skipping", target.Path)
	// 		return fmt.Errorf("SSL_read symbol not found")
	// 	}
	// 	return fmt.Errorf("failed to attach SSL_read uprobe: %w", err)
	// }
	// l.links = append(l.links, readLink)

	// Attach SSL_write uprobe
	writeOpts := &link.UprobeOptions{
		Address: 0,
		PID:     int(target.PID),
	}

	writeLink, err := ex.Uretprobe("SSL_write", l.objs.UretprobeSslWrite, writeOpts)
	if err != nil {
		if target.Type == discovery.SSLTypeStatic {
			logrus.Debugf("Failed to find SSL_write symbol in %s, skipping", target.Path)
			// Don't fail completely if write fails but read succeeded
			return nil
		}
		return fmt.Errorf("failed to attach SSL_write uprobe: %w", err)
	}
	l.links = append(l.links, writeLink)

	logrus.Debugf("Successfully attached to SSL functions in %s (PID: %d)", target.Path, target.PID)
	return nil
}

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

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	mcpevents "github.com/alex-ilgayev/mcpspy/pkg/event"
)

//go:generate sh -c "if [ \"$MCPSPY_TRACE_LOG\" = \"1\" ]; then go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -cc clang -cflags '-D__TARGET_ARCH_x86 -DMCPSPY_TRACE_LOG' mcpspy_bpfel_x86 ../../bpf/mcpspy.c; else go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -cc clang -cflags '-D__TARGET_ARCH_x86' mcpspy_bpfel_x86 ../../bpf/mcpspy.c; fi"
//go:generate sh -c "if [ \"$MCPSPY_TRACE_LOG\" = \"1\" ]; then go run github.com/cilium/ebpf/cmd/bpf2go -target arm64 -cc clang -cflags '-D__TARGET_ARCH_arm64 -DMCPSPY_TRACE_LOG' mcpspy_bpfel_arm64 ../../bpf/mcpspy.c; else go run github.com/cilium/ebpf/cmd/bpf2go -target arm64 -cc clang -cflags '-D__TARGET_ARCH_arm64' mcpspy_bpfel_arm64 ../../bpf/mcpspy.c; fi"

// Loader manages eBPF program lifecycle
type Loader struct {
	objs      *archObjects
	links     []link.Link
	reader    *ringbuf.Reader
	mcpspyPID uint32

	// Iterator link for library enumeration
	// Will be != nil if enumeration is ongoing
	iterLink link.Link

	eventBus bus.EventBus
}

// New creates a new eBPF loader
func New(mcpspyPID uint32, eventBus bus.EventBus) (*Loader, error) {
	// Remove the memory limit for eBPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("failed to remove memlock: %w", err)
	}

	return &Loader{
		mcpspyPID: mcpspyPID,
		eventBus:  eventBus,
	}, nil
}

// Load attaches eBPF programs to kernel
func (l *Loader) Load() error {
	// Load the eBPF collection spec
	spec, err := loadArchSpec()
	if err != nil {
		return fmt.Errorf("failed to load eBPF spec: %w", err)
	}

	// eBPF log levels match logrus Level enumeration exactly:
	bpfLogLevel := uint32(logrus.GetLevel())

	// This following code is a workaround to rewrite the 'log_level' variable
	// The right way to do it is to use spec.RewriteConstants with the following code:
	// spec.RewriteConstants(map[string]interface{}{
	// 	"log_level": bpfLogLevel,
	// })
	//
	// Unfortunately, seems that it's not working. So we rewrite the variable in the section manually.
	if dataSpec, ok := spec.Maps[".data"]; ok {
		// Get the current value bytes
		if len(dataSpec.Contents) > 0 {
			// The value should be a byte slice containing the .data section
			if valueBytes, ok := dataSpec.Contents[0].Value.([]byte); ok {
				// log_level is a 4-byte uint32 at the beginning of .data (offset 0)
				if len(valueBytes) >= 4 {
					binary.LittleEndian.PutUint32(valueBytes[0:4], bpfLogLevel)
					logrus.WithField("bpf_log_level", bpfLogLevel).Debug("Set eBPF log level in .data section")
				}
			}
		}
	}

	// Load eBPF objects with the modified spec
	objs := &archObjects{}
	if err := spec.LoadAndAssign(objs, nil); err != nil {
		var verifierError *ebpf.VerifierError
		if errors.As(err, &verifierError) && logrus.IsLevelEnabled(logrus.DebugLevel) {
			if _, err := fmt.Fprintln(os.Stderr, strings.Join(verifierError.Log, "\n")); err != nil {
				logrus.WithError(err).Warn("Failed to write verifier log to stderr")
			}
		}
		return fmt.Errorf("failed to load eBPF objects: %w", err)
	}
	l.objs = objs

	// Set the mcpspy PID in the map
	key := uint32(0)
	if err := l.objs.McpspyPidMap.Put(&key, &l.mcpspyPID); err != nil {
		return fmt.Errorf("failed to set mcpspy PID in map: %w", err)
	}
	logrus.WithField("mcpspy_pid", l.mcpspyPID).Debug("Set mcpspy PID in map")

	// Attaching exit_vfs_read with Fexit
	readEnterLink, err := link.AttachTracing(link.TracingOptions{
		Program:    l.objs.ExitVfsRead,
		AttachType: ebpf.AttachTraceFExit,
	})
	if err != nil {
		return fmt.Errorf("failed to attach %s fexit: %w", l.objs.ExitVfsRead.String(), err)
	}
	l.links = append(l.links, readEnterLink)

	// Attaching exit_vfs_write with Fexit
	readExitLink, err := link.AttachTracing(link.TracingOptions{
		Program:    l.objs.ExitVfsWrite,
		AttachType: ebpf.AttachTraceFExit,
	})
	if err != nil {
		return fmt.Errorf("failed to attach %s fexit: %w", l.objs.ExitVfsWrite.String(), err)
	}
	l.links = append(l.links, readExitLink)

	// Attaching trace_security_file_open with Fentry to track dynamic library loading
	securityFileOpenLink, err := link.AttachTracing(link.TracingOptions{
		Program:    l.objs.TraceSecurityFileOpen,
		AttachType: ebpf.AttachTraceFEntry,
	})
	if err != nil {
		return fmt.Errorf("failed to attach %s fentry: %w", l.objs.TraceSecurityFileOpen.String(), err)
	}
	l.links = append(l.links, securityFileOpenLink)

	// Attaching trace_destroy_inode with Fentry for inode cleanup
	destroyInodeLink, err := link.AttachTracing(link.TracingOptions{
		Program:    l.objs.TraceDestroyInode,
		AttachType: ebpf.AttachTraceFEntry,
	})
	if err != nil {
		return fmt.Errorf("failed to attach %s fentry: %w", l.objs.TraceDestroyInode.String(), err)
	}
	l.links = append(l.links, destroyInodeLink)

	// Open the ring buffer reader
	reader, err := ringbuf.NewReader(l.objs.Events)
	if err != nil {
		return fmt.Errorf("failed to create ring buffer reader: %w", err)
	}
	l.reader = reader

	logrus.Debug("eBPF programs loaded and attached successfully")
	return nil
}

// Start begins reading events from the ring buffer
func (l *Loader) Start(ctx context.Context) error {
	if l.reader == nil {
		return fmt.Errorf("loader not loaded")
	}

	go func() {
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
					logrus.Warn("Received empty event. Can't read the event type.")
					continue
				}

				eventType := mcpevents.EventType(record.RawSample[0])

				var event mcpevents.Event
				reader := bytes.NewReader(record.RawSample)

				switch eventType {
				case mcpevents.EventTypeFSRead, mcpevents.EventTypeFSWrite:
					if len(record.RawSample) < int(unsafe.Sizeof(mcpevents.FSDataEvent{})) {
						logrus.Warn("Received incomplete data event for data event")
						continue
					}

					var dataEvent mcpevents.FSDataEvent
					if err := binary.Read(reader, binary.LittleEndian, &dataEvent); err != nil {
						logrus.WithError(err).Error("Failed to parse data event")
						continue
					}
					event = &dataEvent
				case mcpevents.EventTypeLibrary:
					if len(record.RawSample) < int(unsafe.Sizeof(mcpevents.LibraryEvent{})) {
						logrus.Warn("Received incomplete library event")
						continue
					}

					var libraryEvent mcpevents.LibraryEvent
					if err := binary.Read(reader, binary.LittleEndian, &libraryEvent); err != nil {
						logrus.WithError(err).Error("Failed to parse library event")
						continue
					}
					event = &libraryEvent
				case mcpevents.EventTypeTlsPayloadSend, mcpevents.EventTypeTlsPayloadRecv:
					if len(record.RawSample) < int(unsafe.Sizeof(mcpevents.TlsPayloadEvent{})) {
						logrus.Warn("Received incomplete TLS event")
						continue
					}

					var tlsEvent mcpevents.TlsPayloadEvent
					if err := binary.Read(reader, binary.LittleEndian, &tlsEvent); err != nil {
						logrus.WithError(err).Error("Failed to parse TLS event")
						continue
					}
					event = &tlsEvent
				case mcpevents.EventTypeTlsFree:
					if len(record.RawSample) < int(unsafe.Sizeof(mcpevents.TlsFreeEvent{})) {
						logrus.Warn("Received incomplete TLS free event")
						continue
					}

					var tlsFreeEvent mcpevents.TlsFreeEvent
					if err := binary.Read(reader, binary.LittleEndian, &tlsFreeEvent); err != nil {
						logrus.WithError(err).Error("Failed to parse TLS free event")
						continue
					}
					event = &tlsFreeEvent
				default:
					logrus.WithField("type", eventType).Warn("Unknown event type")
					continue
				}

				l.eventBus.Publish(event)
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

	// Attach SSL_new uretprobe for session creation
	sslNewLink, err := ex.Uretprobe("SSL_new", l.objs.SslNewExit, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_new uretprobe: %w", err)
	}
	l.links = append(l.links, sslNewLink)

	// Attach SSL_free uprobe for session destruction
	sslFreeLink, err := ex.Uprobe("SSL_free", l.objs.SslFreeEntry, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_free uprobe: %w", err)
	}
	l.links = append(l.links, sslFreeLink)

	// Attach SSL_do_handshake entry uprobe
	sslHandshakeEntryLink, err := ex.Uprobe("SSL_do_handshake", l.objs.SslDoHandshakeEntry, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_do_handshake entry uprobe: %w", err)
	}
	l.links = append(l.links, sslHandshakeEntryLink)

	// Attach SSL_do_handshake exit uretprobe
	sslHandshakeExitLink, err := ex.Uretprobe("SSL_do_handshake", l.objs.SslDoHandshakeExit, nil)
	if err != nil {
		return fmt.Errorf("failed to attach SSL_do_handshake exit uretprobe: %w", err)
	}
	l.links = append(l.links, sslHandshakeExitLink)

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

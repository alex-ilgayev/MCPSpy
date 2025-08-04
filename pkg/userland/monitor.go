package userland

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/alex-ilgayev/mcpspy/pkg/ebpf"
)

// Monitor represents the userland monitoring system
type Monitor struct {
	ctx       context.Context
	cancel    context.CancelFunc
	events    chan interface{}
	mu        sync.RWMutex
	running   bool
	logger    *logrus.Logger
	config    *Config
	cgoMonitor *CGOMonitor
}

// Config holds configuration for userland monitoring
type Config struct {
	MonitorStdio   bool
	MonitorHTTP    bool
	MonitorSSL     bool
	MonitorPackets bool
	HTTPPort       string
	SSLPort        string
	Interface      string
	LogLevel       logrus.Level
}

// ProcessInfo holds information about monitored processes
type ProcessInfo struct {
	PID     int32
	Command string
	Args    []string
}

// New creates a new userland monitor
func New(config *Config) *Monitor {
	ctx, cancel := context.WithCancel(context.Background())
	logger := logrus.New()
	logger.SetLevel(config.LogLevel)

	return &Monitor{
		ctx:        ctx,
		cancel:     cancel,
		events:     make(chan interface{}, 1000),
		logger:     logger,
		config:     config,
		cgoMonitor: NewCGOMonitor(config),
	}
}

// Start begins monitoring MCP communications
func (m *Monitor) Start() error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("monitor already running")
	}
	m.running = true
	m.mu.Unlock()

	// Start the CGO-based C library monitor
	if err := m.cgoMonitor.Start(); err != nil {
		return fmt.Errorf("failed to start CGO monitor: %w", err)
	}

	// Start event forwarding goroutine
	go m.forwardCGOEvents()

	return nil
}

// forwardCGOEvents forwards events from CGO monitor to the events channel
func (m *Monitor) forwardCGOEvents() {
	defer close(m.events)

	for cgoEvent := range m.cgoMonitor.Events() {
		// Convert CGO event to eBPF-compatible event for existing parser
		event := &ebpf.DataEvent{
			EventHeader: ebpf.EventHeader{
				PID:       uint32(cgoEvent.PID),
				EventType: m.convertEventType(cgoEvent.EventType),
			},
			BufSize: uint32(cgoEvent.BufSize),
		}

		// Copy buffer data
		if len(cgoEvent.Data) > 0 {
			copy(event.Buf[:], cgoEvent.Data)
		}

		// Copy command name
		copy(event.Buf[len(cgoEvent.Data):], []byte(cgoEvent.Comm))

		select {
		case m.events <- event:
		case <-m.ctx.Done():
			return
		}
	}
}

// convertEventType converts CGO event type to eBPF event type
func (m *Monitor) convertEventType(eventType string) ebpf.EventType {
	switch eventType {
	case "read":
		return ebpf.EventTypeRead
	case "write":
		return ebpf.EventTypeWrite
	default:
		return ebpf.EventTypeRead
	}
}

// Stop stops the monitoring
func (m *Monitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if !m.running {
		return
	}
	
	m.running = false
	m.cancel()
	
	// Stop CGO monitor
	m.cgoMonitor.Stop()
}

// Events returns the events channel
func (m *Monitor) Events() <-chan interface{} {
	return m.events
}

// AttachToProcess attaches to a running process using LD_PRELOAD
func (m *Monitor) AttachToProcess(pid int) error {
	// This would implement process attachment via LD_PRELOAD injection
	// For now, return not implemented
	return fmt.Errorf("process attachment via LD_PRELOAD injection not yet implemented")
}

// LaunchAndMonitor launches a command with LD_PRELOAD monitoring
func (m *Monitor) LaunchAndMonitor(command string, args []string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(m.ctx, command, args...)
	
	// Set LD_PRELOAD environment variable to our library
	env := os.Environ()
	libPath := "userland/libmcpspy.so" // Path to our compiled library
	
	// Add LD_PRELOAD and MCPSpy environment variables
	env = append(env, "LD_PRELOAD="+libPath)
	env = append(env, "MCPSPY_ENABLE=1")
	env = append(env, "MCPSPY_LOG_FILE=/tmp/mcpspy.log")
	
	cmd.Env = env
	
	// Start the command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command with LD_PRELOAD: %w", err)
	}
	
	m.logger.WithFields(logrus.Fields{
		"pid":     cmd.Process.Pid,
		"command": command,
		"args":    args,
	}).Info("Launched process with LD_PRELOAD monitoring")
	
	return cmd, nil
}

// Close cleans up the monitor
func (m *Monitor) Close() error {
	m.Stop()
	return nil
}
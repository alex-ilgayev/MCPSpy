package debug

import (
	"bufio"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

func PrintTracePipe(log *logrus.Logger) error {
	p, err := os.Open("/sys/kernel/debug/tracing/trace_pipe")
	if err != nil {
		return err
	}
	defer p.Close()

	scanner := bufio.NewScanner(p)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		// Example line:
		// mcpspy-linux-am-260585  [015] ...11 63008.728893: bpf_trace_printk: DEBUG: exit_vfs_read: <msg>
		// <comm>-<pid> [<cpu>] <flags> <timestamp>: bpf_trace_printk: <your message>
		//
		// We want to parse the log level from the message and log it accordingly.
		line := strings.TrimSpace(scanner.Text())

		// Find "bpf_trace_printk: " which marks the start of the actual message
		bpfMarker := "bpf_trace_printk: "
		bpfIdx := strings.Index(line, bpfMarker)
		if bpfIdx == -1 {
			continue
		}

		// Parse comm-pid from the beginning of the line
		// Format: <comm>-<pid> [<cpu>] ...
		commPidEnd := strings.Index(line, " ")
		var comm, pid string
		if commPidEnd != -1 {
			commPid := line[:commPidEnd]
			// Find the last hyphen to separate comm from pid
			lastHyphen := strings.LastIndex(commPid, "-")
			if lastHyphen != -1 {
				comm = commPid[:lastHyphen]
				pid = commPid[lastHyphen+1:]
			}
		}

		message := line[bpfIdx+len(bpfMarker):]

		// Check for log level prefix in the message
		var logLevel logrus.Level
		var cleanMessage string

		switch {
		case strings.HasPrefix(message, "ERROR: "):
			logLevel = logrus.ErrorLevel
			cleanMessage = message[7:]
		case strings.HasPrefix(message, "WARN: "):
			logLevel = logrus.WarnLevel
			cleanMessage = message[6:]
		case strings.HasPrefix(message, "INFO: "):
			logLevel = logrus.InfoLevel
			cleanMessage = message[6:]
		case strings.HasPrefix(message, "DEBUG: "):
			logLevel = logrus.DebugLevel
			cleanMessage = message[7:]
		case strings.HasPrefix(message, "TRACE: "):
			logLevel = logrus.TraceLevel
			cleanMessage = message[7:]
		default:
			// No recognized prefix, log as debug
			logLevel = logrus.DebugLevel
			cleanMessage = message
		}

		if !log.IsLevelEnabled(logLevel) {
			continue
		}

		// Log at the appropriate level with structured fields
		entry := log.WithField("ebpf", true)
		if comm != "" {
			entry = entry.WithField("comm", comm)
		}
		if pid != "" {
			entry = entry.WithField("pid", pid)
		}

		switch logLevel {
		case logrus.ErrorLevel:
			entry.Error(cleanMessage)
		case logrus.WarnLevel:
			entry.Warn(cleanMessage)
		case logrus.InfoLevel:
			entry.Info(cleanMessage)
		case logrus.DebugLevel:
			entry.Debug(cleanMessage)
		case logrus.TraceLevel:
			entry.Trace(cleanMessage)
		default:
			entry.Debug(cleanMessage)
		}
	}

	return nil
}

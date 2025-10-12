package eventlogger

import (
	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/sirupsen/logrus"
)

// EventLogger subscribes to all event types and logs them using logrus
type EventLogger struct {
	eventBus bus.EventBus
}

func New(eventBus bus.EventBus) (*EventLogger, error) {
	el := &EventLogger{
		eventBus: eventBus,
	}

	if err := el.eventBus.Subscribe(event.EventTypeFSRead, el.logEvent); err != nil {
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeFSWrite, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeLibrary, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeTlsPayloadSend, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeTlsPayloadRecv, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeTlsFree, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeHttpRequest, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeHttpResponse, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeHttpSSE, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}
	if err := el.eventBus.Subscribe(event.EventTypeMCPMessage, el.logEvent); err != nil {
		el.Close()
		return nil, err
	}

	return el, nil
}

func (el *EventLogger) logEvent(e event.Event) {
	switch evt := e.(type) {
	case *event.LibraryEvent:
		logrus.WithFields(logrus.Fields{
			"pid":     evt.PID,
			"comm":    evt.Comm(),
			"path":    evt.Path(),
			"inode":   evt.Inode,
			"mountNS": evt.MntNSID,
		}).Trace("Library loaded")

	case *event.TlsPayloadEvent:
		logrus.WithFields(logrus.Fields{
			"type":     evt.Type(),
			"pid":      evt.PID,
			"comm":     evt.Comm(),
			"size":     evt.Size,
			"buf_size": evt.BufSize,
			"version":  evt.HttpVersion,
		}).Trace("TLS payload event")

	case *event.TlsFreeEvent:
		logrus.WithFields(logrus.Fields{
			"pid":     evt.PID,
			"comm":    evt.Comm(),
			"ssl_ctx": evt.SSLContext,
		}).Trace("TLS free event")

	case *event.HttpRequestEvent:
		logrus.WithFields(logrus.Fields{
			"method": evt.Method,
			"host":   evt.Host,
			"path":   evt.Path,
		}).Trace("HTTP request event")

	case *event.HttpResponseEvent:
		logrus.WithFields(logrus.Fields{
			"method":     evt.Method,
			"host":       evt.Host,
			"path":       evt.Path,
			"code":       evt.Code,
			"is_chunked": evt.IsChunked,
		}).Trace("HTTP response event")

	case *event.SSEEvent:
		logrus.WithFields(logrus.Fields{
			"method":    evt.Method,
			"host":      evt.Host,
			"path":      evt.Path,
			"sse_event": evt.SSEEventType,
		}).Trace("HTTP SSE event")

	case *event.MCPEvent:
		logrus.WithFields(logrus.Fields{
			"transport":    evt.TransportType,
			"message_type": evt.MessageType,
			"method":       evt.Method,
			"id":           evt.ID,
		}).Trace("MCP message event")
	}
}

func (el *EventLogger) Close() {
	el.eventBus.Unsubscribe(event.EventTypeFSRead, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeFSWrite, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeLibrary, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeTlsPayloadSend, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeTlsPayloadRecv, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeTlsFree, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeHttpRequest, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeHttpResponse, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeHttpSSE, el.logEvent)
	el.eventBus.Unsubscribe(event.EventTypeMCPMessage, el.logEvent)
}

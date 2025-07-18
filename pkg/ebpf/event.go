package ebpf

type EventType uint8

const (
	EventTypeRead  EventType = 1
	EventTypeWrite EventType = 2
)

// Event represents data captured by eBPF program
type Event struct {
	PID       uint32
	Comm      [16]uint8
	EventType EventType
	_         [3]uint8 // padding
	Size      uint32
	BufSize   uint32
	Buf       [16 * 1024]uint8
}

package fs

import (
	"testing"
	"time"

	testutil "github.com/alex-ilgayev/mcpspy/internal/testing"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// Helper function to drain events and return only FSAggregatedEvent
func receiveAggregatedEvent(ch <-chan event.Event, timeout time.Duration) (event.Event, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case evt := <-ch:
			// Skip raw FS events, only return aggregated events
			if evt.Type() == event.EventTypeFSAggregatedRead || evt.Type() == event.EventTypeFSAggregatedWrite {
				return evt, true
			}
			// Skip raw events and continue waiting
		case <-deadline:
			return nil, false
		}
	}
}

func TestSessionManager_SingleCompleteJson(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	jsonData := []byte(`{"jsonrpc":"2.0","method":"test","id":1}`)

	fsEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       1234,
			},
			FilePtr: 0x7fff12345678,
		},
		Size:    uint32(len(jsonData)),
		BufSize: uint32(len(jsonData)),
	}
	copy(fsEvent.Buf[:], jsonData)

	// Publish raw FS event to trigger processing
	mockBus.Publish(fsEvent)

	// Should receive one FSAggregatedEvent
	evt, ok := receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("No FSAggregatedEvent received")
	}
	if evt.Type() != event.EventTypeFSAggregatedRead {
		t.Errorf("Expected EventTypeFSAggregatedRead, got %v", evt.Type())
	}
	aggEvt := evt.(*event.FSAggregatedEvent)
	if string(aggEvt.Payload) != string(jsonData) {
		t.Errorf("Expected payload %q, got %q", jsonData, aggEvt.Payload)
	}
	if aggEvt.PID != 1234 {
		t.Errorf("Expected PID 1234, got %d", aggEvt.PID)
	}
	if aggEvt.FilePtr != 0x7fff12345678 {
		t.Errorf("Expected FilePtr 0x7fff12345678, got 0x%x", aggEvt.FilePtr)
	}
}

func TestSessionManager_FragmentedJson(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	pid := uint32(5678)
	filePtr := uint64(0xabcdef123456)

	// Send JSON in three fragments
	fragment1 := []byte(`{"jsonrpc":"2.0","me`)
	fragment2 := []byte(`thod":"test",`)
	fragment3 := []byte(`"id":1}`)

	// First fragment
	event1 := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSWrite,
				PID:       pid,
			},
			FilePtr: filePtr,
		},
		BufSize: uint32(len(fragment1)),
	}
	copy(event1.Buf[:], fragment1)
	mockBus.Publish(event1)

	// Should not emit aggregated event yet (incomplete JSON)
	evt, ok := receiveAggregatedEvent(mockBus.Events(), 50*time.Millisecond)
	if ok {
		t.Fatalf("Should not emit aggregated event for incomplete JSON, got %v", evt)
	}

	// Second fragment
	event2 := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSWrite,
				PID:       pid,
			},
			FilePtr: filePtr,
		},
		BufSize: uint32(len(fragment2)),
	}
	copy(event2.Buf[:], fragment2)
	mockBus.Publish(event2)

	// Still incomplete
	evt, ok = receiveAggregatedEvent(mockBus.Events(), 50*time.Millisecond)
	if ok {
		t.Fatalf("Should not emit aggregated event for incomplete JSON, got %v", evt)
	}

	// Third fragment - completes the JSON
	event3 := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSWrite,
				PID:       pid,
			},
			FilePtr: filePtr,
		},
		BufSize: uint32(len(fragment3)),
	}
	copy(event3.Buf[:], fragment3)
	mockBus.Publish(event3)

	// Now should emit complete JSON
	expectedJson := `{"jsonrpc":"2.0","method":"test","id":1}`
	evt, ok = receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("No FSAggregatedEvent received after complete JSON")
	}
	if evt.Type() != event.EventTypeFSAggregatedWrite {
		t.Errorf("Expected EventTypeFSAggregatedWrite, got %v", evt.Type())
	}
	aggEvt := evt.(*event.FSAggregatedEvent)
	if string(aggEvt.Payload) != expectedJson {
		t.Errorf("Expected payload %q, got %q", expectedJson, aggEvt.Payload)
	}
}

func TestSessionManager_MultipleJsonInOneEvent(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	// Two complete JSON objects in one event
	jsonData := []byte(`{"id":1,"method":"first"}
{"id":2,"method":"second"}`)

	fsEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       9999,
			},
			FilePtr: 0x1111111111,
		},
		BufSize: uint32(len(jsonData)),
	}
	copy(fsEvent.Buf[:], jsonData)

	mockBus.Publish(fsEvent)

	// Should receive two separate FSAggregatedEvents
	expectedPayloads := []string{
		`{"id":1,"method":"first"}`,
		`{"id":2,"method":"second"}`,
	}

	for i, expectedPayload := range expectedPayloads {
		evt, ok := receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
		if !ok {
			t.Fatalf("Did not receive FSAggregatedEvent %d", i)
		}
		if evt.Type() != event.EventTypeFSAggregatedRead {
			t.Errorf("Event %d: Expected EventTypeFSAggregatedRead, got %v", i, evt.Type())
		}
		aggEvt := evt.(*event.FSAggregatedEvent)
		if string(aggEvt.Payload) != expectedPayload {
			t.Errorf("Event %d: Expected payload %q, got %q", i, expectedPayload, aggEvt.Payload)
		}
	}
}

func TestSessionManager_MultipleJsonAcrossFragments(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	pid := uint32(4444)
	filePtr := uint64(0x2222222222)

	// First event: complete JSON + start of second
	event1Data := []byte(`{"id":1}
{"id":2,"dat`)
	event1 := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       pid,
			},
			FilePtr: filePtr,
		},
		BufSize: uint32(len(event1Data)),
	}
	copy(event1.Buf[:], event1Data)
	mockBus.Publish(event1)

	// Should emit first complete JSON
	evt, ok := receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("Did not receive first JSON")
	}
	aggEvt := evt.(*event.FSAggregatedEvent)
	if string(aggEvt.Payload) != `{"id":1}` {
		t.Errorf("Expected first JSON, got %q", aggEvt.Payload)
	}

	// Second event: complete the second JSON + third complete JSON
	event2Data := []byte(`a":"value"}
{"id":3}`)
	event2 := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       pid,
			},
			FilePtr: filePtr,
		},
		BufSize: uint32(len(event2Data)),
	}
	copy(event2.Buf[:], event2Data)
	mockBus.Publish(event2)

	// Should emit second JSON
	evt, ok = receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("Did not receive second JSON")
	}
	aggEvt = evt.(*event.FSAggregatedEvent)
	if string(aggEvt.Payload) != `{"id":2,"data":"value"}` {
		t.Errorf("Expected second JSON, got %q", aggEvt.Payload)
	}

	// Should emit third JSON
	evt, ok = receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("Did not receive third JSON")
	}
	aggEvt = evt.(*event.FSAggregatedEvent)
	if string(aggEvt.Payload) != `{"id":3}` {
		t.Errorf("Expected third JSON, got %q", aggEvt.Payload)
	}
}

func TestSessionManager_MultipleSessions(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	// Session 1
	pid1 := uint32(1111)
	filePtr1 := uint64(0xaaaa)
	json1 := []byte(`{"session":1}`)

	event1 := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       pid1,
			},
			FilePtr: filePtr1,
		},
		BufSize: uint32(len(json1)),
	}
	copy(event1.Buf[:], json1)
	mockBus.Publish(event1)

	// Session 2 (different PID)
	pid2 := uint32(2222)
	filePtr2 := uint64(0xbbbb)
	json2 := []byte(`{"session":2}`)

	event2 := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSWrite,
				PID:       pid2,
			},
			FilePtr: filePtr2,
		},
		BufSize: uint32(len(json2)),
	}
	copy(event2.Buf[:], json2)
	mockBus.Publish(event2)

	// Session 3 (same PID as session 1, but different file pointer)
	filePtr3 := uint64(0xcccc)
	json3 := []byte(`{"session":3}`)

	event3 := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       pid1,
			},
			FilePtr: filePtr3,
		},
		BufSize: uint32(len(json3)),
	}
	copy(event3.Buf[:], json3)
	mockBus.Publish(event3)

	// Should receive all three events
	receivedEvents := make(map[string]*event.FSAggregatedEvent)

	for i := 0; i < 3; i++ {
		evt, ok := receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
		if !ok {
			t.Fatalf("Expected 3 events, only received %d", i)
		}
		aggEvt := evt.(*event.FSAggregatedEvent)
		receivedEvents[string(aggEvt.Payload)] = aggEvt
	}

	// Verify all sessions
	if evt, ok := receivedEvents[`{"session":1}`]; !ok {
		t.Error("Did not receive event for session 1")
	} else {
		if evt.PID != pid1 {
			t.Errorf("Session 1: expected PID %d, got %d", pid1, evt.PID)
		}
		if evt.FilePtr != filePtr1 {
			t.Errorf("Session 1: expected FilePtr 0x%x, got 0x%x", filePtr1, evt.FilePtr)
		}
		if evt.Type() != event.EventTypeFSAggregatedRead {
			t.Errorf("Session 1: expected aggregated read event type")
		}
	}

	if evt, ok := receivedEvents[`{"session":2}`]; !ok {
		t.Error("Did not receive event for session 2")
	} else {
		if evt.PID != pid2 {
			t.Errorf("Session 2: expected PID %d, got %d", pid2, evt.PID)
		}
		if evt.FilePtr != filePtr2 {
			t.Errorf("Session 2: expected FilePtr 0x%x, got 0x%x", filePtr2, evt.FilePtr)
		}
		if evt.Type() != event.EventTypeFSAggregatedWrite {
			t.Errorf("Session 2: expected aggregated write event type")
		}
	}

	if evt, ok := receivedEvents[`{"session":3}`]; !ok {
		t.Error("Did not receive event for session 3")
	} else {
		if evt.PID != pid1 {
			t.Errorf("Session 3: expected PID %d, got %d", pid1, evt.PID)
		}
		if evt.FilePtr != filePtr3 {
			t.Errorf("Session 3: expected FilePtr 0x%x, got 0x%x", filePtr3, evt.FilePtr)
		}
	}
}

func TestSessionManager_WhitespaceHandling(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	// JSON with leading/trailing whitespace
	jsonData := []byte(`
	{"id":1}
  {"id":2}
`)

	fsEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       7777,
			},
			FilePtr: 0x3333,
		},
		BufSize: uint32(len(jsonData)),
	}
	copy(fsEvent.Buf[:], jsonData)

	mockBus.Publish(fsEvent)

	// Should receive both JSON objects (whitespace trimmed)
	for i := 1; i <= 2; i++ {
		evt, ok := receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
		if !ok {
			t.Fatalf("Did not receive event %d", i)
		}
		aggEvt := evt.(*event.FSAggregatedEvent)
		expectedPayload := `{"id":` + string(rune('0'+i)) + `}`
		if string(aggEvt.Payload) != expectedPayload {
			t.Errorf("Event %d: Expected %q, got %q", i, expectedPayload, aggEvt.Payload)
		}
	}
}

func TestSessionManager_NestedStructures(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	complexJson := []byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"test","arguments":{"nested":{"deeply":{"value":[1,2,3]}}}}}`)

	fsEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSWrite,
				PID:       8888,
			},
			FilePtr: 0x4444,
		},
		BufSize: uint32(len(complexJson)),
	}
	copy(fsEvent.Buf[:], complexJson)

	mockBus.Publish(fsEvent)

	evt, ok := receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("Did not receive complex JSON event")
	}
	aggEvt := evt.(*event.FSAggregatedEvent)
	if string(aggEvt.Payload) != string(complexJson) {
		t.Errorf("Expected complex JSON, got %q", aggEvt.Payload)
	}
}

func TestSessionManager_CleanupSession(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	pid := uint32(6666)
	filePtr := uint64(0x5555)

	// Send incomplete JSON
	incompleteJson := []byte(`{"incomplete":`)
	fsEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       pid,
			},
			FilePtr: filePtr,
		},
		BufSize: uint32(len(incompleteJson)),
	}
	copy(fsEvent.Buf[:], incompleteJson)
	mockBus.Publish(fsEvent)

	// Verify session exists
	sm.mu.Lock()
	key := sessionKey{pid: pid, filePtr: filePtr, origEventType: event.EventTypeFSRead}
	_, exists := sm.sessions[key]
	sm.mu.Unlock()
	if !exists {
		t.Fatal("Session should exist after processing incomplete JSON")
	}

	// Cleanup session
	sm.CleanupSession(pid, filePtr)

	// Verify sessions are deleted
	sm.mu.Lock()
	_, exists1 := sm.sessions[sessionKey{pid: pid, filePtr: filePtr, origEventType: event.EventTypeFSRead}]
	_, exists2 := sm.sessions[sessionKey{pid: pid, filePtr: filePtr, origEventType: event.EventTypeFSWrite}]
	sm.mu.Unlock()
	if exists1 || exists2 {
		t.Fatal("Sessions should be deleted after CleanupSession")
	}
}

func TestSessionManager_EmptyBuffer(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	fsEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       3333,
			},
			FilePtr: 0x6666,
		},
		BufSize: 0,
	}

	mockBus.Publish(fsEvent)

	// Should not emit any aggregated event
	evt, ok := receiveAggregatedEvent(mockBus.Events(), 50*time.Millisecond)
	if ok {
		t.Fatalf("Should not emit aggregated event for empty buffer, got %v", evt)
	}
}

func TestSessionManager_JsonArray(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	jsonArray := []byte(`[{"id":1},{"id":2},{"id":3}]`)

	fsEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSWrite,
				PID:       5555,
			},
			FilePtr: 0x7777,
		},
		BufSize: uint32(len(jsonArray)),
	}
	copy(fsEvent.Buf[:], jsonArray)

	mockBus.Publish(fsEvent)

	// Should receive the complete array as one event
	evt, ok := receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("Did not receive JSON array event")
	}
	aggEvt := evt.(*event.FSAggregatedEvent)
	if string(aggEvt.Payload) != string(jsonArray) {
		t.Errorf("Expected array %q, got %q", jsonArray, aggEvt.Payload)
	}
}

func TestSessionManager_ReadWriteEventTypes(t *testing.T) {
	mockBus := testutil.NewMockBus()
	defer mockBus.Close()

	sm, err := NewSessionManager(mockBus)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}
	defer sm.Close()

	pid := uint32(1000)
	filePtr := uint64(0x8888)

	// Read event
	readJson := []byte(`{"type":"read"}`)
	readEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSRead,
				PID:       pid,
			},
			FilePtr: filePtr,
		},
		BufSize: uint32(len(readJson)),
	}
	copy(readEvent.Buf[:], readJson)
	mockBus.Publish(readEvent)

	// Write event (different file pointer to create separate session)
	writeJson := []byte(`{"type":"write"}`)
	writeEvent := &event.FSDataEvent{
		FSEventBase: event.FSEventBase{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeFSWrite,
				PID:       pid,
			},
			FilePtr: filePtr + 1,
		},
		BufSize: uint32(len(writeJson)),
	}
	copy(writeEvent.Buf[:], writeJson)
	mockBus.Publish(writeEvent)

	// Check read event type
	evt, ok := receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("Did not receive read event")
	}
	if evt.Type() != event.EventTypeFSAggregatedRead {
		t.Errorf("Expected EventTypeFSAggregatedRead, got %v", evt.Type())
	}

	// Check write event type
	evt, ok = receiveAggregatedEvent(mockBus.Events(), 100*time.Millisecond)
	if !ok {
		t.Fatal("Did not receive write event")
	}
	if evt.Type() != event.EventTypeFSAggregatedWrite {
		t.Errorf("Expected EventTypeFSAggregatedWrite, got %v", evt.Type())
	}
}

package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSession_ID(t *testing.T) {
	tests := []struct {
		name       string
		session    *Session
		expectedID string
	}{
		{
			name: "external ID takes precedence",
			session: &Session{
				ExternalID: "external-123",
				InternalID: "internal-456",
			},
			expectedID: "external-123",
		},
		{
			name: "internal ID when no external ID",
			session: &Session{
				InternalID: "internal-456",
			},
			expectedID: "internal-456",
		},
		{
			name: "internal ID when external ID is empty",
			session: &Session{
				ExternalID: "",
				InternalID: "internal-789",
			},
			expectedID: "internal-789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedID, tt.session.ID())
		})
	}
}

func TestNewFromProtocol(t *testing.T) {
	externalID := "test-external-id"
	internalID := "test-internal-id"

	session := NewFromProtocol(externalID, internalID)

	assert.NotNil(t, session)
	assert.Equal(t, externalID, session.ExternalID)
	assert.Equal(t, internalID, session.InternalID)
	assert.Equal(t, externalID, session.ID())
}

func TestNewFromHeuristic(t *testing.T) {
	internalID := "test-internal-id"

	session := NewFromHeuristic(internalID)

	assert.NotNil(t, session)
	assert.Empty(t, session.ExternalID)
	assert.Equal(t, internalID, session.InternalID)
	assert.Equal(t, internalID, session.ID())
}

func TestGenerateUUID(t *testing.T) {
	id1 := GenerateUUID()
	id2 := GenerateUUID()

	// Should generate different UUIDs
	assert.NotEqual(t, id1, id2)

	// Should be valid UUID format (36 characters with dashes)
	assert.Len(t, id1, 36)
	assert.Contains(t, id1, "-")
}

func TestGenerateDeterministicID(t *testing.T) {
	tests := []struct {
		name       string
		components []interface{}
		wantSame   bool
	}{
		{
			name:       "same components generate same ID",
			components: []interface{}{"pid", 1234, "comm", "test"},
			wantSame:   true,
		},
		{
			name:       "different components generate different ID",
			components: []interface{}{"pid", 5678, "comm", "other"},
			wantSame:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id1 := GenerateDeterministicID(tt.components...)
			id2 := GenerateDeterministicID(tt.components...)

			if tt.wantSame {
				assert.Equal(t, id1, id2, "deterministic ID should be same for same components")
			}

			// Should be valid UUID-like format (36 characters with dashes)
			assert.Len(t, id1, 36)
			assert.Contains(t, id1, "-")
		})
	}

	// Test that different components generate different IDs
	id1 := GenerateDeterministicID("test", 123)
	id2 := GenerateDeterministicID("test", 456)
	assert.NotEqual(t, id1, id2)
}

func TestGenerateDeterministicID_Consistency(t *testing.T) {
	// Test that the same inputs always produce the same output
	components := []interface{}{uint32(1234), "test-comm", uint64(0x123456)}

	id1 := GenerateDeterministicID(components...)
	id2 := GenerateDeterministicID(components...)
	id3 := GenerateDeterministicID(components...)

	assert.Equal(t, id1, id2)
	assert.Equal(t, id2, id3)
}

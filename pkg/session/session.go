package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

// SessionIDType indicates the source of the session ID
type SessionIDType string

const (
	// SessionIDTypeProtocol indicates the session ID came from the MCP protocol (Mcp-Session-Id header)
	SessionIDTypeProtocol SessionIDType = "protocol"
	// SessionIDTypeHeuristic indicates the session ID was generated using heuristics
	SessionIDTypeHeuristic SessionIDType = "heuristic"
)

// Session represents a unique MCP communication session with identifiers
type Session struct {
	// ProtocolID is the session ID from the MCP protocol (Mcp-Session-Id header), if available
	ProtocolID string `json:"protocol_id,omitempty"`
	// InternalID is a generated session ID based on transport characteristics
	InternalID string `json:"internal_id"`
	// Type indicates which ID source is primary
	Type SessionIDType `json:"type"`
}

// ID returns the primary session identifier for this session
// If a protocol-level session ID is available, it returns that; otherwise returns the internal ID
func (s *Session) ID() string {
	if s.ProtocolID != "" {
		return s.ProtocolID
	}
	return s.InternalID
}

// NewFromProtocol creates a Session from a protocol-provided session ID (e.g., Mcp-Session-Id header)
// It also generates an internal ID for additional correlation
func NewFromProtocol(protocolID string, internalID string) *Session {
	return &Session{
		ProtocolID: protocolID,
		InternalID: internalID,
		Type:       SessionIDTypeProtocol,
	}
}

// NewFromHeuristic creates a Session using only a heuristically-generated ID
func NewFromHeuristic(internalID string) *Session {
	return &Session{
		InternalID: internalID,
		Type:       SessionIDTypeHeuristic,
	}
}

// GenerateUUID generates a new random UUID as a session ID
func GenerateUUID() string {
	return uuid.New().String()
}

// GenerateDeterministicID generates a deterministic session ID based on input parameters
// This is useful for creating consistent session IDs from transport characteristics
func GenerateDeterministicID(components ...interface{}) string {
	// Build a string from all components
	var composite string
	for _, comp := range components {
		composite += fmt.Sprintf("%v:", comp)
	}

	// Generate SHA-256 hash
	hash := sha256.Sum256([]byte(composite))
	hashStr := hex.EncodeToString(hash[:])

	// Format as UUID v4 (with proper version and variant bits)
	// Use first 32 hex characters and format as: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
	// where y is 8, 9, a, or b (variant bits)
	return fmt.Sprintf("%s-%s-4%s-%s-%s",
		hashStr[0:8],
		hashStr[8:12],
		hashStr[13:16],
		hashStr[16:20],
		hashStr[20:32])
}

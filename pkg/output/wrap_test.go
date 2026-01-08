package output

import (
	"strings"
	"testing"

	"github.com/muesli/reflow/ansi"
)

func TestWrapANSIText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		width    int
		expected int // expected number of lines
	}{
		{
			name:     "short text no wrap",
			input:    "hello",
			width:    20,
			expected: 1,
		},
		{
			name:     "plain text wrap",
			input:    "this is a longer string that should wrap",
			width:    20,
			expected: 2,
		},
		{
			name:     "ansi colored text wrap",
			input:    "\x1b[32mthis is a longer green string that should wrap\x1b[0m",
			width:    20,
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapANSIText(tt.input, tt.width)
			if len(result) != tt.expected {
				t.Errorf("expected %d lines, got %d: %v", tt.expected, len(result), result)
			}
		})
	}
}

func TestWrapANSITextPreservesColor(t *testing.T) {
	// A colored string that will wrap
	input := "\x1b[32mthis is a green string\x1b[0m"
	width := 10

	lines := wrapANSIText(input, width)

	// Check that the second line also has the green color
	for i, line := range lines {
		if i > 0 {
			// Wrapped lines should start with the color sequence
			if !strings.HasPrefix(line, "\x1b[32m") {
				t.Errorf("line %d should start with green color sequence, got: %q", i, line)
			}
		}
	}
}

func TestWrapANSITextCorrectWidth(t *testing.T) {
	input := "\x1b[32mabcdefghijklmnopqrstuvwxyz\x1b[0m"
	width := 10

	lines := wrapANSIText(input, width)

	for i, line := range lines {
		w := ansi.PrintableRuneWidth(line)
		if w > width {
			t.Errorf("line %d has width %d, expected <= %d: %q", i, w, width, line)
		}
	}
}

func TestTruncateANSI(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		width         int
		expectedWidth int
	}{
		{
			name:          "short text no truncate",
			input:         "hello",
			width:         20,
			expectedWidth: 5,
		},
		{
			name:          "truncate plain text",
			input:         "hello world!",
			width:         5,
			expectedWidth: 5,
		},
		{
			name:          "truncate ansi text",
			input:         "\x1b[32mhello world!\x1b[0m",
			width:         5,
			expectedWidth: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateANSI(tt.input, tt.width)
			w := ansi.PrintableRuneWidth(result)
			if w > tt.expectedWidth {
				t.Errorf("expected width <= %d, got %d: %q", tt.expectedWidth, w, result)
			}
		})
	}
}

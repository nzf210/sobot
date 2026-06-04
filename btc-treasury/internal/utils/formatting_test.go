package utils

import (
	"testing"
)

func TestEscapeMdv2(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello_world", "hello\\_world"},
		{"hello*world", "hello\\*world"},
		{"1.23", "1\\.23"},
		{"hello-world", "hello\\-world"},
		{"abc\\def", "abc\\\\def"},
	}

	for _, test := range tests {
		output := EscapeMdv2(test.input)
		if output != test.expected {
			t.Errorf("EscapeMdv2(%q) = %q, expected %q", test.input, output, test.expected)
		}
	}
}

func TestChunkText(t *testing.T) {
	small := "hello world"
	chunks := ChunkText(small, 20)
	if len(chunks) != 1 || chunks[0] != small {
		t.Errorf("Expected 1 chunk equal to input, got %d", len(chunks))
	}

	text := "line1\nline2\nline3"
	chunks = ChunkText(text, 5)
	if len(chunks) != 3 {
		t.Errorf("Expected 3 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != "line1" || chunks[1] != "line2" || chunks[2] != "line3" {
		t.Errorf("Unexpected chunks: %v", chunks)
	}
}

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointing(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.checkpoint")

	// Read missing file returns 0
	n, err := readCheckpoint(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}

	// Write 42
	if err := writeCheckpoint(path, 42); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	// Read back 42
	n, err = readCheckpoint(path)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if n != 42 {
		t.Errorf("expected 42, got %d", n)
	}
}

func TestReadCheckpoint_Corrupt(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "corrupt.checkpoint")
	if err := os.WriteFile(path, []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, err := readCheckpoint(path)
	if err == nil {
		t.Errorf("expected parse error")
	}
}

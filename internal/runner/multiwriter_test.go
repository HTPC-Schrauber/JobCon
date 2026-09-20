package runner

import (
	"bytes"
	"testing"
	"time"
)

func TestMultiWriterAndBroadcaster(t *testing.T) {
	var dest bytes.Buffer
	broadcaster := NewLogBroadcaster(10)

	mw := NewMultiWriter(&dest, broadcaster)

	ch, _, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	input := "Line A\nLine B\nPartial"
	if _, err := mw.Write([]byte(input)); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Line A and Line B should be broadcasted
	select {
	case l1 := <-ch:
		if l1 != "Line A" {
			t.Errorf("expected Line A, got %q", l1)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for Line A")
	}

	select {
	case l2 := <-ch:
		if l2 != "Line B" {
			t.Errorf("expected Line B, got %q", l2)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for Line B")
	}

	// Flush partial line
	mw.Flush()
	select {
	case l3 := <-ch:
		if l3 != "Partial" {
			t.Errorf("expected Partial, got %q", l3)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for Partial line")
	}

	// Check destination writer
	expectedDest := "Line A\nLine B\nPartial"
	if dest.String() != expectedDest {
		t.Errorf("expected dest %q, got %q", expectedDest, dest.String())
	}
}

package cachelock

import (
	"errors"
	"testing"
)

func TestAcquireExclusive(t *testing.T) {
	dir := t.TempDir()

	release, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire: want ErrLocked, got %v", err)
	}

	release()

	again, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	again()
}

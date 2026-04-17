package fsio

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestAcquireLock_HappyPath verifies a basic lock → release cycle succeeds.
func TestAcquireLock_HappyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claudeorch.lock")

	release, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// TestAcquireLock_Reacquire verifies the lock can be re-acquired after release.
func TestAcquireLock_Reacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claudeorch.lock")

	rel1, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := rel1(); err != nil {
		t.Fatal(err)
	}

	rel2, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatalf("re-acquire failed: %v", err)
	}
	_ = rel2()
}

// TestAcquireLock_Contention verifies that a second goroutine blocks until
// the first releases, and that both goroutines succeed in sequence.
func TestAcquireLock_Contention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claudeorch.lock")

	rel1, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var order []int

	done := make(chan struct{})
	go func() {
		defer close(done)
		rel2, err2 := AcquireLock(context.Background(), path)
		if err2 != nil {
			t.Errorf("goroutine AcquireLock: %v", err2)
			return
		}
		mu.Lock()
		order = append(order, 2)
		mu.Unlock()
		_ = rel2()
	}()

	// Give the goroutine a moment to start and block.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	order = append(order, 1)
	mu.Unlock()

	_ = rel1()

	select {
	case <-done:
	case <-time.After(LockTimeout + time.Second):
		t.Fatal("second goroutine did not acquire lock within deadline")
	}

	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("unexpected acquisition order: %v", order)
	}
}

// TestAcquireLock_Timeout verifies that ErrLockTimeout is returned when the
// lock is already held for the full LockTimeout duration.
func TestAcquireLock_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}
	path := filepath.Join(t.TempDir(), "claudeorch.lock")

	rel, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rel() }()

	start := time.Now()
	_, err = AcquireLock(context.Background(), path)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrLockTimeout) {
		t.Errorf("expected ErrLockTimeout, got: %v", err)
	}
	// Allow 500ms slack around the expected 3-second timeout.
	if elapsed < LockTimeout-500*time.Millisecond || elapsed > LockTimeout+500*time.Millisecond {
		t.Errorf("timeout elapsed = %v, expected ~%v", elapsed, LockTimeout)
	}
}

// TestAcquireLock_ContextDeadline verifies that a context deadline shorter
// than LockTimeout is respected.
func TestAcquireLock_ContextDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claudeorch.lock")

	rel, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rel() }()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = AcquireLock(ctx, path)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrLockTimeout) {
		t.Errorf("expected ErrLockTimeout, got: %v", err)
	}
	// Should have returned well before the full 3-second LockTimeout.
	if elapsed > LockTimeout {
		t.Errorf("context deadline not respected: elapsed %v > LockTimeout %v", elapsed, LockTimeout)
	}
}

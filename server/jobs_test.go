package server

import (
	"sync"
	"testing"
	"time"
)

func TestJobStore_Create(t *testing.T) {
	store := NewJobStore()
	job := store.Create("biz1")

	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if job.BusinessID != "biz1" {
		t.Errorf("BusinessID = %q, want %q", job.BusinessID, "biz1")
	}
	if job.Status != StatusPending {
		t.Errorf("Status = %q, want %q", job.Status, StatusPending)
	}
	if job.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if job.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}
}

func TestJobStore_Create_UniqueIDs(t *testing.T) {
	store := NewJobStore()
	a := store.Create("biz1")
	b := store.Create("biz1")
	if a.ID == b.ID {
		t.Errorf("expected unique IDs, got %q twice", a.ID)
	}
}

func TestJobStore_Get(t *testing.T) {
	store := NewJobStore()
	job := store.Create("biz1")

	got, ok := store.Get(job.ID)
	if !ok {
		t.Fatal("Get returned false for existing job")
	}
	if got.ID != job.ID {
		t.Errorf("ID = %q, want %q", got.ID, job.ID)
	}
	if got.BusinessID != "biz1" {
		t.Errorf("BusinessID = %q, want %q", got.BusinessID, "biz1")
	}
}

func TestJobStore_Get_Missing(t *testing.T) {
	store := NewJobStore()
	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("expected false for unknown job ID")
	}
}

func TestJobStore_Update(t *testing.T) {
	store := NewJobStore()
	job := store.Create("biz1")
	before := job.UpdatedAt

	// Ensure at least 1ms elapses so UpdatedAt is strictly after CreatedAt.
	time.Sleep(time.Millisecond)

	store.Update(job.ID, func(j *Job) {
		j.Status = StatusRunning
	})

	got, ok := store.Get(job.ID)
	if !ok {
		t.Fatal("job missing after Update")
	}
	if got.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", got.Status, StatusRunning)
	}
	if !got.UpdatedAt.After(before) {
		t.Error("expected UpdatedAt to be bumped after Update")
	}
}

func TestJobStore_Update_SetResult(t *testing.T) {
	store := NewJobStore()
	job := store.Create("biz1")

	store.Update(job.ID, func(j *Job) {
		j.Status = StatusComplete
		j.Result = "all safe"
	})

	got, _ := store.Get(job.ID)
	if got.Status != StatusComplete {
		t.Errorf("Status = %q, want %q", got.Status, StatusComplete)
	}
	if got.Result != "all safe" {
		t.Errorf("Result = %q, want %q", got.Result, "all safe")
	}
}

func TestJobStore_Update_SetError(t *testing.T) {
	store := NewJobStore()
	job := store.Create("biz1")

	store.Update(job.ID, func(j *Job) {
		j.Status = StatusFailed
		j.Error = "something went wrong"
	})

	got, _ := store.Get(job.ID)
	if got.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, StatusFailed)
	}
	if got.Error != "something went wrong" {
		t.Errorf("Error = %q, want %q", got.Error, "something went wrong")
	}
}

func TestJobStore_Update_Missing(t *testing.T) {
	store := NewJobStore()
	// Update on a missing ID must not panic.
	store.Update("nonexistent", func(j *Job) {
		j.Status = StatusRunning
	})
}

func TestJobStore_Concurrent(t *testing.T) {
	store := NewJobStore()
	const n = 50

	var mu sync.Mutex
	ids := make([]string, 0, n)

	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job := store.Create("biz")
			mu.Lock()
			ids = append(ids, job.ID)
			mu.Unlock()
		}()
	}
	wg.Wait()

	for _, id := range ids {
		if _, ok := store.Get(id); !ok {
			t.Errorf("job %q missing after concurrent Create", id)
		}
	}
}

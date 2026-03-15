package server

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

type JobStatus string

const (
	StatusPending  JobStatus = "pending"
	StatusRunning  JobStatus = "running"
	StatusComplete JobStatus = "complete"
	StatusFailed   JobStatus = "failed"
)

type Job struct {
	ID         string    `json:"job_id"`
	BusinessID string    `json:"business_id"`
	Status     JobStatus `json:"status"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

// NewJobStore returns an empty JobStore ready for use.
func NewJobStore() *JobStore {
	return &JobStore{jobs: make(map[string]*Job)}
}

// Create adds a new pending job for businessID and returns it.
func (s *JobStore) Create(businessID string) *Job {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read: %v", err))
	}
	job := &Job{
		ID:         fmt.Sprintf("%x", b),
		BusinessID: businessID,
		Status:     StatusPending,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()
	return job
}

// Get returns a copy of the job so callers can read it without holding the lock.
func (s *JobStore) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	copy := *job
	return &copy, true
}

// Update applies fn to the job with the given id and stamps UpdatedAt.
func (s *JobStore) Update(id string, fn func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[id]; ok {
		fn(job)
		job.UpdatedAt = time.Now()
	}
}

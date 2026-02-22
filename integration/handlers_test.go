// Package integration contains end-to-end tests for the HTTP server.
// They spin up the real handler mux via httptest and exercise routes
// without making live Gemini API calls (a stubAnalyzer is injected instead).
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fodmap/data/schemas"
	"fodmap/server"
)

// stubAnalyzer is a test double for server.Analyzer.
type stubAnalyzer struct {
	result string
	err    error
	// delay simulates slow LLM responses so callers can observe interim states.
	delay time.Duration
}

func (s *stubAnalyzer) Analyze(_ context.Context, _ []schemas.ReviewSchemaS) (string, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return s.result, s.err
}

// newMux returns the handler mux used by the server, wired to the stub analyzer.
func newMux(t *testing.T, analyzer server.Analyzer) http.Handler {
	t.Helper()
	srv := server.NewServer(analyzer, 0)
	return srv.Handler()
}

// jobResponse is used to decode the POST /analyze JSON body.
type jobResponse struct {
	JobID string `json:"job_id"`
}

// resultResponse mirrors server.Job for decoding GET /results/{job_id}.
type resultResponse struct {
	JobID      string `json:"job_id"`
	BusinessID string `json:"business_id"`
	Status     string `json:"status"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

// --- /analyze ---

func TestAnalyzeHandler_MissingBusinessID(t *testing.T) {
	mux := newMux(t, &stubAnalyzer{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/analyze", nil)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAnalyzeHandler_ReturnsJobID(t *testing.T) {
	mux := newMux(t, &stubAnalyzer{result: "ok"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/analyze?business_id=biz1", nil)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	var resp jobResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.JobID == "" {
		t.Error("expected non-empty job_id in response")
	}
}

// --- /results/{job_id} ---

func TestResultsHandler_UnknownJob(t *testing.T) {
	mux := newMux(t, &stubAnalyzer{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/results/doesnotexist", nil)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestResultsHandler_PendingJob(t *testing.T) {
	// Use a long delay so runAnalysis is still in-flight when we query.
	mux := newMux(t, &stubAnalyzer{delay: 10 * time.Second})

	// Create a job.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/analyze?business_id=biz1", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("analyze status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	var job jobResponse
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job response: %v", err)
	}

	// Immediately fetch results — job should be pending or running.
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/results/"+job.JobID, nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("results status = %d, want %d", rec2.Code, http.StatusOK)
	}

	var result resultResponse
	if err := json.NewDecoder(rec2.Body).Decode(&result); err != nil {
		t.Fatalf("decode results response: %v", err)
	}
	if result.JobID != job.JobID {
		t.Errorf("job_id = %q, want %q", result.JobID, job.JobID)
	}
	if result.Status != "pending" && result.Status != "running" {
		t.Errorf("status = %q, want pending or running", result.Status)
	}
}

// --- /reviews ---

func TestReviewsHandler_MissingBusinessID(t *testing.T) {
	mux := newMux(t, &stubAnalyzer{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/reviews", nil)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestReviewsHandler_ArchiveMissing(t *testing.T) {
	// In the integration test environment the archive is not present, so the
	// handler should respond with 500 and not panic.
	mux := newMux(t, &stubAnalyzer{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/reviews?business_id=biz1", nil)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

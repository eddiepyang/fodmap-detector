package data

import (
	"archive/tar"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fodmap/data/schemas"
)

// createTestTar creates a plain (non-gzipped) tar file at path containing
// the given entries. Each entry is a filename → content pair.
func createTestTar(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tw := tar.NewWriter(f)
	defer tw.Close()

	for name, content := range entries {
		hdr := &tar.Header{
			Name: name,
			Mode: 0600,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
}

// ---- GetArchive ----

func TestGetArchive_FileNotFound(t *testing.T) {
	_, _, err := GetArchive("/nonexistent/path.tar", "anything")
	if err == nil {
		t.Error("expected error for nonexistent archive")
	}
}

func TestGetArchive_EntryNotFound(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "test.tar")
	createTestTar(t, tarPath, map[string]string{
		"some_other_file.jsonl": `{"id": "1"}`,
	})

	_, _, err := GetArchive(tarPath, "nonexistent_file")
	if err == nil {
		t.Error("expected error for missing entry")
	}
}

func TestGetArchive_Success(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "test.tar")
	createTestTar(t, tarPath, map[string]string{
		"yelp_review.jsonl": `{"review_id":"r1","text":"Great"}`,
	})

	scanner, closer, err := GetArchive(tarPath, "review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer closer.Close()

	if !scanner.Scan() {
		t.Fatal("expected at least one line")
	}
	if scanner.Text() != `{"review_id":"r1","text":"Great"}` {
		t.Errorf("got %q", scanner.Text())
	}
}

// ---- GetReviewsByBusiness ----

func TestGetReviewsByBusiness_Success(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "test.tar")

	r1, _ := json.Marshal(schemas.Review{ReviewID: "r1", BusinessID: "biz1", Stars: 5, Text: "Amazing"})
	r2, _ := json.Marshal(schemas.Review{ReviewID: "r2", BusinessID: "biz2", Stars: 3, Text: "OK"})
	r3, _ := json.Marshal(schemas.Review{ReviewID: "r3", BusinessID: "biz1", Stars: 4, Text: "Good"})

	content := string(r1) + "\n" + string(r2) + "\n" + string(r3) + "\n"
	createTestTar(t, tarPath, map[string]string{
		"yelp_academic_dataset_review.jsonl": content,
	})

	reviews, err := GetReviewsByBusiness(tarPath, "biz1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("got %d reviews, want 2", len(reviews))
	}
	if reviews[0].ReviewID != "r1" {
		t.Errorf("first review ID = %q, want %q", reviews[0].ReviewID, "r1")
	}
	if reviews[1].ReviewID != "r3" {
		t.Errorf("second review ID = %q, want %q", reviews[1].ReviewID, "r3")
	}
}

func TestGetReviewsByBusiness_NoMatchingBusiness(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "test.tar")

	r1, _ := json.Marshal(schemas.Review{ReviewID: "r1", BusinessID: "biz1", Text: "test"})
	createTestTar(t, tarPath, map[string]string{
		"yelp_review.jsonl": string(r1) + "\n",
	})

	reviews, err := GetReviewsByBusiness(tarPath, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 0 {
		t.Errorf("got %d reviews, want 0", len(reviews))
	}
}

func TestGetReviewsByBusiness_NoReviewFile(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "test.tar")
	createTestTar(t, tarPath, map[string]string{
		"business.jsonl": `{"business_id":"b1"}`,
	})

	_, err := GetReviewsByBusiness(tarPath, "biz1")
	if err == nil {
		t.Error("expected error when review file is not in archive")
	}
}

func TestGetReviewsByBusiness_FileNotFound(t *testing.T) {
	_, err := GetReviewsByBusiness("/nonexistent/path.tar", "biz1")
	if err == nil {
		t.Error("expected error for nonexistent archive")
	}
}

// ---- GetBusinessMap ----

func TestGetBusinessMap_Success(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "test.tar")

	b1, _ := json.Marshal(schemas.Business{BusinessID: "b1", Name: "Pizza Place", City: "NYC", State: "NY"})
	b2, _ := json.Marshal(schemas.Business{BusinessID: "b2", Name: "Taco Shop", City: "LA", State: "CA"})

	content := string(b1) + "\n" + string(b2) + "\n"
	createTestTar(t, tarPath, map[string]string{
		"yelp_academic_dataset_business.jsonl": content,
	})

	businesses, err := GetBusinessMap(tarPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(businesses) != 2 {
		t.Fatalf("got %d businesses, want 2", len(businesses))
	}
	if businesses["b1"].Name != "Pizza Place" {
		t.Errorf("b1 name = %q, want %q", businesses["b1"].Name, "Pizza Place")
	}
	if businesses["b2"].City != "LA" {
		t.Errorf("b2 city = %q, want %q", businesses["b2"].City, "LA")
	}
}

func TestGetBusinessMap_NoBusinessFile(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "test.tar")
	createTestTar(t, tarPath, map[string]string{
		"review.jsonl": `{"review_id":"r1"}`,
	})

	_, err := GetBusinessMap(tarPath)
	if err == nil {
		t.Error("expected error when business file is not in archive")
	}
}

func TestGetBusinessMap_FileNotFound(t *testing.T) {
	_, err := GetBusinessMap("/nonexistent/path.tar")
	if err == nil {
		t.Error("expected error for nonexistent archive")
	}
}

// ---- getTarReader (plain tar fallback) ----

func TestGetTarReader_PlainTar(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "plain.tar")
	createTestTar(t, tarPath, map[string]string{
		"test.txt": "hello",
	})

	f, err := os.Open(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tr, closer, err := getTarReader(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer closer.Close()

	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if hdr.Name != "test.txt" {
		t.Errorf("name = %q, want %q", hdr.Name, "test.txt")
	}
}

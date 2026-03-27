package data

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	goio "io"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"fodmap/data/schemas"
)

// DefaultArchivePath is the default path to the Yelp dataset TAR archive.
const DefaultArchivePath = "../data/yelp_dataset.tar"

// UnmarshalReview parses a single JSONL review record from inputBytes.
func UnmarshalReview(pattern *regexp.Regexp, inputBytes []byte) (schemas.Review, error) {
	jsonl := &schemas.Review{}
	if err := json.Unmarshal(inputBytes, jsonl); err != nil {
		return schemas.Review{}, err
	}
	return *jsonl, nil
}

// GetArchive opens the archive at archivePath and returns a scanner positioned
// at the first entry whose name contains fileName, along with a closer for the
// underlying file. The caller must call Close() when done. Returns an error if
// the archive cannot be opened or the entry is not found.
type multiCloser struct{ a, b goio.Closer }

func (m multiCloser) Close() error { _ = m.a.Close(); return m.b.Close() }

func getTarReader(f *os.File) (*tar.Reader, goio.Closer, error) {
	gz, err := gzip.NewReader(f)
	if err == nil {
		return tar.NewReader(gz), gz, nil
	}
	if err == gzip.ErrHeader {
		if _, err := f.Seek(0, 0); err != nil {
			return nil, nil, fmt.Errorf("seeking file: %w", err)
		}
		return tar.NewReader(f), goio.NopCloser(f), nil
	}
	return nil, nil, fmt.Errorf("opening gzip stream: %w", err)
}

func GetArchive(archivePath, fileName string) (*bufio.Scanner, goio.Closer, error) {
	files, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening archive: %w", err)
	}

	archiveFiles, gz, err := getTarReader(files)
	if err != nil {
		_ = files.Close()
		return nil, nil, err
	}
	for {
		file, err := archiveFiles.Next()
		if errors.Is(err, goio.EOF) {
			_ = gz.Close()
			_ = files.Close()
			return nil, nil, fmt.Errorf("file %q not found in archive", fileName)
		}
		if err != nil {
			_ = gz.Close()
			_ = files.Close()
			return nil, nil, fmt.Errorf("reading tar: %w", err)
		}
		if strings.Contains(file.Name, fileName) {
			return bufio.NewScanner(archiveFiles), multiCloser{gz, files}, nil
		}
	}
}

// GetReviewsByBusiness returns all reviews in the archive for the given businessID.
// If archivePath is empty, DefaultArchivePath is used.
func GetReviewsByBusiness(archivePath, businessID string) ([]schemas.Review, error) {
	if archivePath == "" {
		archivePath = DefaultArchivePath
	}
	files, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	defer files.Close()

	archiveFiles, gz, err := getTarReader(files)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	for {
		file, err := archiveFiles.Next()
		if errors.Is(err, goio.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if !strings.Contains(file.Name, "review") {
			continue
		}

		scanner := bufio.NewScanner(archiveFiles)
		buf := make([]byte, 4*1024*1024)
		scanner.Buffer(buf, 4*1024*1024)

		var results []schemas.Review
		for scanner.Scan() {
			var r schemas.Review
			if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
				continue
			}
			if r.BusinessID == businessID {
				results = append(results, r)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("scanning archive: %w", err)
		}
		return results, nil
	}
	return nil, fmt.Errorf("review file not found in archive")
}

// GetBusinessMap reads the business file from the archive and returns a map keyed by business_id.
// The caller can use the map for O(1) lookups when joining reviews with business metadata.
// If archivePath is empty, DefaultArchivePath is used.
func GetBusinessMap(archivePath string) (map[string]schemas.Business, error) {
	if archivePath == "" {
		archivePath = DefaultArchivePath
	}
	files, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	defer files.Close()

	archiveFiles, gz, err := getTarReader(files)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	for {
		file, err := archiveFiles.Next()
		if errors.Is(err, goio.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if !strings.Contains(file.Name, "business") {
			continue
		}

		scanner := bufio.NewScanner(archiveFiles)
		buf := make([]byte, 4*1024*1024)
		scanner.Buffer(buf, 4*1024*1024)

		businesses := make(map[string]schemas.Business)
		for scanner.Scan() {
			var b schemas.Business
			if err := json.Unmarshal(scanner.Bytes(), &b); err != nil {
				continue
			}
			businesses[b.BusinessID] = b
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("scanning archive: %w", err)
		}
		return businesses, nil
	}
	return nil, fmt.Errorf("business file not found in archive")
}

// ListDir logs all files in ../../../data/. Returns an error if the directory cannot be read.
func ListDir() error {
	files, err := os.ReadDir("../../../data/")
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}

	for _, file := range files {
		slog.Info("listing files", "name", file.Name(), "is_dir", file.IsDir())
	}
	return nil
}


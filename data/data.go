package data

import (
	"archive/tar"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	goio "io"
	"log/slog"
	"math"
	"os"
	"regexp"
	"strings"
	"time"

	"fodmap/data/io"
	"fodmap/data/schemas"

	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/reader"
	"github.com/xitongsys/parquet-go/writer"
)

const (
	archiveGz         = "./data/archive.tar.gz"
	writeStopRowBatch = 100
)

// UnmarshalReview parses a single JSONL review record from inputBytes.
func UnmarshalReview(pattern *regexp.Regexp, inputBytes []byte) (schemas.Review, error) {
	jsonl := &schemas.Review{}
	if err := json.Unmarshal(inputBytes, jsonl); err != nil {
		return schemas.Review{}, err
	}
	return *jsonl, nil
}

// GetArchive opens the archive and returns a scanner positioned at the first
// entry whose name contains fileName. Returns an error if the archive cannot
// be opened or the entry is not found.
func GetArchive(fileName string) (*bufio.Scanner, error) {
	files, err := os.Open(archiveGz)
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}

	archiveFiles := tar.NewReader(files)
	for {
		file, err := archiveFiles.Next()
		if errors.Is(err, goio.EOF) {
			return nil, fmt.Errorf("file %q not found in archive", fileName)
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if strings.Contains(file.Name, fileName) {
			return bufio.NewScanner(archiveFiles), nil
		}
	}
}

// GetReviewsByBusiness returns all reviews in the archive for the given businessID.
func GetReviewsByBusiness(businessID string) ([]schemas.Review, error) {
	files, err := os.Open(archiveGz)
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	defer files.Close()

	archiveFiles := tar.NewReader(files)
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
func GetBusinessMap() (map[string]schemas.Business, error) {
	files, err := os.Open(archiveGz)
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	defer files.Close()

	archiveFiles := tar.NewReader(files)
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

// WriteBatchParquet reads JSONL records from fileScanner, parses them as Review records,
// and writes them to outFile in Parquet format. Returns an error if writing fails.
func WriteBatchParquet(outFile string, fileScanner *bufio.Scanner) error {
	start := time.Now()

	fw, err := local.NewLocalFileWriter(outFile)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer fw.Close()

	pw, err := writer.NewParquetWriter(fw, new(schemas.Review), 20)
	if err != nil {
		return fmt.Errorf("creating parquet writer: %w", err)
	}

	inChan := make(chan io.ParseResult, 3)
	go io.ReadToChan(UnmarshalReview, inChan, fileScanner, writeStopRowBatch)

	var parseErrors int
	for item := range inChan {
		if item.Err != nil {
			parseErrors++
			slog.Error("skipping record due to parse error", "error", item.Err)
			continue
		}
		if err = pw.Write(item.Record); err != nil {
			return fmt.Errorf("writing to parquet: %w", err)
		}
	}

	if err := pw.WriteStop(); err != nil {
		return fmt.Errorf("finalizing parquet file: %w", err)
	}

	slog.Info("process completed", "duration", time.Since(start), "file", outFile, "parse_errors", parseErrors)
	return nil
}

// ReadParquet reads up to earlyStop rows from fileName and returns them as []schemas.Review.
func ReadParquet(fileName string, earlyStop int64) (any, error) {
	fr, err := local.NewLocalFileReader(fileName)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer fr.Close()

	pr, err := reader.NewParquetReader(fr, new(schemas.Review), 4)
	if err != nil {
		return nil, fmt.Errorf("creating parquet reader: %w", err)
	}

	n := pr.GetNumRows()
	stop := int(math.Min(float64(n), float64(earlyStop)))
	slog.Info("reading rows", "count", stop)
	rows := make([]schemas.Review, stop)
	if err := pr.Read(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

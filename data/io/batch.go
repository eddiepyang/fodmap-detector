package io

import (
	"bufio"
	"log/slog"
	"regexp"

	"fodmap/data/schemas"
)

type parseSchemaFunc func(pattern *regexp.Regexp, inputBytes []byte) (schemas.Review, error)

// ParseResult is the item sent on inChan for every scanned line.
// Err is non-nil when the parser failed; callers can count these to track error rates.
type ParseResult struct {
	Record schemas.Review
	Err    error
}

func ReadToChan(parserFunc parseSchemaFunc, inChan chan ParseResult, s *bufio.Scanner, stop int) {
	defer close(inChan)

	pattern := regexp.MustCompile(`[a-zA-Z0-9'-]+`)

	for counter := 0; s.Scan(); counter++ {
		record, err := parserFunc(pattern, s.Bytes())
		inChan <- ParseResult{Record: record, Err: err}

		if counter >= stop && stop != 0 {
			return
		}
	}

	if err := s.Err(); err != nil {
		slog.Error("reading standard input", "error", err)
	}
}

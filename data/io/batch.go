package io

import (
	"bufio"
	"fodmap/data/schemas"
	"log"
	"regexp"

	"github.com/davecgh/go-spew/spew"
)

type parseSchemaFunc func(pattern *regexp.Regexp, inputBytes []byte) schemas.ReviewSchemaS

func ReadToChan(parserFunc parseSchemaFunc, inChan chan schemas.ReviewSchemaS, doneCh chan struct{}, s *bufio.Scanner, stop int) {
	defer close(inChan)
	defer close(doneCh)

	pattern := regexp.MustCompile(`[a-zA-Z0-9'-]+`)

	for counter := 0; s.Scan(); counter++ {
		chanInput := parserFunc(pattern, s.Bytes())
		spew.Dump("channel input", chanInput)
		inChan <- chanInput

		if counter >= stop && stop != 0 {
			doneCh <- struct{}{}
			return
		}
	}

	if err := s.Err(); err != nil {
		log.Printf("reading standard input: %v", err)
	}

	doneCh <- struct{}{}

}

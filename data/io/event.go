package io

import (
	"bufio"
	"encoding/json"
	"log"
	"os"

	"github.com/davecgh/go-spew/spew"
	"github.com/hamba/avro/v2/ocf"
)

func WriteEventFile(scanner *bufio.Scanner, writePath string, outputSchema string) {
	// Create output file
	outFile, err := os.Create(writePath)
	if err != nil {
		log.Fatal("Failed to create file:", err)
	}
	defer outFile.Close()

	encoder, err := ocf.NewEncoder(outputSchema, outFile)
	if err != nil {
		log.Fatal("Failed to create OCF encoder:", err)
	}

	for scanner.Scan() {
		// Create a new map for each record
		var avroMap interface{}

		if err := json.Unmarshal(scanner.Bytes(), &avroMap); err != nil {
			log.Fatal("Failed to unmarshal:", err)
		}

		// Debug print the map
		log.Println("Record to be written:")
		spew.Dump(avroMap)

		// Append the record
		if err := encoder.Encode(avroMap); err != nil {
			log.Fatal("Failed to append record:", err)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal("Scanner error:", err)
	}
}

func ReadFile(filePath string) error {
	avroFile, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer avroFile.Close()

	decoder, err := ocf.NewDecoder(avroFile)
	if err != nil {
		return err
	}

	for decoder.HasNext() {
		var datum interface{}
		if err := decoder.Decode(&datum); err != nil {
			return err
		}
		spew.Dump(datum)
	}

	return decoder.Error()
}

package io

import (
	"bufio"
	"encoding/json"
	"log"
	"os"

	"github.com/davecgh/go-spew/spew"
	"github.com/linkedin/goavro"
)

func WriteEventFile(scanner *bufio.Scanner, writePath string, outputSchema string) {

	codec, err := goavro.NewCodec(outputSchema)
	if err != nil {
		log.Fatal(err)
	}

	// Create output file
	outFile, err := os.Create(writePath)
	if err != nil {
		log.Fatal("Failed to create file:", err)
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)

	if err != nil {
		log.Fatal(err)
	}
	// Create OCF writer
	ocfWriter, err := goavro.NewOCFWriter(goavro.OCFConfig{
		W:     writer,
		Codec: codec,
	})
	if err != nil {
		log.Fatal("Failed to create OCF writer:", err)
	}

	for i := 0; i <= 1; i++ {
		if !scanner.Scan() {
			break
		}

		// Create a new map for each record
		avroMap := make(map[string]interface{})

		if err := json.Unmarshal(scanner.Bytes(), &avroMap); err != nil {
			log.Fatal("Failed to unmarshal:", err)
		}

		// Debug print the map
		log.Println("Record to be written:")
		spew.Dump(avroMap)

		// Append the record
		if err := ocfWriter.Append([]interface{}{avroMap}); err != nil {
			log.Fatal("Failed to append record:", err)
		}
	}

	// Flush and close before reading
	writer.Flush()
	outFile.Close()
}

func ReadFile(filePath string) error {

	avroFile, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer avroFile.Close()

	ocfReader, err := goavro.NewOCFReader(avroFile)
	if err != nil {
		return err
	}

	for ocfReader.Scan() {
		datum, err := ocfReader.Read()
		if err != nil {
			return err
		}
		spew.Dump(datum)
	}

	return nil
}

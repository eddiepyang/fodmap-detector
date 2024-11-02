package avro

import (
	"errors"
	"log"
	"os"
	"test-server/data"
	"testing"
)

const outfile = "outfile.avro"

// todo: mock this object
func setUp() {
	_, err := os.OpenFile(outfile, os.O_RDWR|os.O_CREATE, 0644)
	if errors.Is(err, os.ErrNotExist) {
		scanner := data.GetArchive("review")
		WriteAvroFile(scanner, outfile, Schema)
	}

}

func TestReadFile(t *testing.T) {
	setUp()
	err := ReadFile(outfile)
	if err != nil {
		log.Fatal(err)
	}
}
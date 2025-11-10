package data

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"fodmap/data/io"
	"fodmap/data/schemas"
	"log"
	"math"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/reader"
	"github.com/xitongsys/parquet-go/writer"
)

const (
	archiveGz         = "./data/archive.tar.gz"
	writeStopRowBatch = 100
)

type CompressedFileReader[R zip.Reader | tar.Reader] interface {
	getReader() (R, error)
	close() float64
}

type ZipReader struct { //todo: define methods for zip
	file       os.File
	readCloser zip.ReadCloser
}

type TarReader struct { //todo: define methods for tar
	file       os.File
	readCloser tar.Reader
}

type ReviewSchema interface {
	ParseText(inputString string) []string
}

// func (r *ReviewSchemaS) ParseText(inputString string) []string {
// 	return r.Pattern.FindAllString(inputString, -1)
// }

func UnmarshalReview(pattern *regexp.Regexp, inputBytes []byte) schemas.ReviewSchemaS {
	jsonl := &schemas.ReviewSchemaS{}
	// test := map[string]interface{}{}
	if err := json.Unmarshal(inputBytes, jsonl); err != nil {
		log.Panic(err, "unmarshalling err")
	}
	spew.Dump("jsonl is", jsonl)
	// jsonl.ParsedText = pattern.FindAllString(jsonl.Text, -1)
	return *jsonl
}

func JsonifyReview(pattern *regexp.Regexp, inputBytes []byte) string {
	return string(inputBytes)
}

func (z *ZipReader) read(fileName string) (*bufio.Scanner, error) {
	for _, file := range z.readCloser.File { //todo: fix this
		if strings.Contains(file.FileHeader.Name, fileName) {
			spew.Dump("file header", file.FileHeader)
			rc, err := file.Open()
			if err != nil {
				log.Panic(err)
			}
			defer rc.Close()
			return bufio.NewScanner(rc), nil
		}
	}
	return nil, fmt.Errorf("error loading file")
}

func GetArchive(fileName string) *bufio.Scanner {
	files, err := os.Open(archiveGz)
	if err != nil {
		log.Fatal(err)
	}

	archiveFiles := tar.NewReader(files)

	for {
		file, err := archiveFiles.Next()
		if err != nil {
			os.Exit(0)
		}
		log.Println()
		spew.Dump("file name", file)
		if strings.Contains(file.Name, fileName) {
			return bufio.NewScanner(archiveFiles)
		}

	}
}

func ListDir() {
	files, err := os.ReadDir("../../../data/")
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		log.Printf("listing files: %v, directory?: %v \n", file.Name(), file.IsDir())
	}
}

func WriteBatchParquet(outFile string, fileScanner *bufio.Scanner) {
	start := time.Now()

	//write
	fw, err := local.NewLocalFileWriter(outFile)
	if err != nil {
		log.Println("Can't create file", err)
		return
	}
	defer fw.Close()
	pw, err := writer.NewParquetWriter(fw, new(schemas.ReviewSchemaS), 20)
	if err != nil {
		log.Println("Can't create writer", err)
		return
	}
	defer pw.Flush(true)

	inChan := make(chan schemas.ReviewSchemaS, 3)
	doneCh := make(chan struct{})

	go func() {
		io.ReadToChan(UnmarshalReview, inChan, doneCh, fileScanner, writeStopRowBatch)
	}()

L:
	for {

		select {
		case <-doneCh:
			if pw.WriteStop() != nil {
				log.Println("write completed")
				break L
			}

		case item := <-inChan:
			spew.Dump("item is", item)
			err = pw.Write(item)
			if err != nil {
				log.Panicf("Error writing to Parquet: %v", err)
			}
		}

	}

	log.Printf("process completed in %v, file in %v.\n", time.Since(start), outFile)
}


func ReadParquet(fileName string, earlyStop int64) (interface{}, error) {
	fr, err := local.NewLocalFileReader(fileName)
	if err != nil {
		log.Println("Can't open file", err.Error())
		return nil, err
	}
	defer fr.Close()

	pr, err := reader.NewParquetReader(fr, new(schemas.ReviewSchemaS), 4)
	if err != nil {
		log.Println("Can't create parquet reader", err)
		return nil, err
	}

	n := pr.GetNumRows()
	stop := int(math.Min(float64(n), float64(earlyStop)))
	log.Printf("reading %v rows", stop)
	var rows = make([]schemas.ReviewSchemaS, stop)
	if err := pr.Read(&rows); err != nil {
		return nil, err
	}
	// Create a slice to hold the results
	// s := make([]*map[string]interface{}, 0, stop)

	// // Read into the slice
	// for i := int64(0); i < stop; i++ {
	// 	record := new(map[string]interface{})
	// 	if err := pr.Read(record); err != nil {
	// 		log.Println("Can't read row", err)
	// 		return nil, err
	// 	}
	// 	s[i] = record
	// }

	spew.Dump("reading files \n", rows)
	return rows, nil
}

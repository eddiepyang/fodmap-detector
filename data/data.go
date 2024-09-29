package data

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
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
	sc = `
{
	"Tag":"name=yelp-reviews",
	"Fields":[
		{"Tag":"name=review_id, inname=review_id, type=BYTE_ARRAY, repetitiontype=REQUIRED"},
		{"Tag":"name=user_id, inname=user_id, type=BYTE_ARRAY, repetitiontype=REQUIRED"},
		{"Tag":"name=business_id, inname=business_id, type=BYTE_ARRAY, repetitiontype=REQUIRED"},
		{"Tag":"name=stars, inname=stars, type=FLOAT, repetitiontype=REQUIRED"},
		{"Tag":"name=useful, inname=useful, type=FLOAT, repetitiontype=REQUIRED"},
		{"Tag":"name=funny, inname=funny, type=FLOAT, repetitiontype=REQUIRED"},
		{"Tag":"name=cool, inname=cool, type=FLOAT, repetitiontype=REQUIRED"},
		{"Tag":"name=text, inname=text, type=BYTE_ARRAY, repetitiontype=REQUIRED"}	
	]
}
`
	archive      = "../../data/yelp_dataset.tar" //todo: move this to config
	reivewFile   = "yelp_academic_dataset_review.json"
	writeStopRow = 500000
)

type ReviewSchema struct {
	Review_id   string   `parquet:"name=review_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	User_id     string   `parquet:"name=user_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Business_id string   `parquet:"name=business_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Stars       float32  `parquet:"name=stars, type=FLOAT"`
	Useful      int32    `parquet:"name=useful, type=INT32"`
	Funny       int32    `parquet:"name=funny, type=INT32"`
	Text        string   `parquet:"name=text, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ParsedText  []string `parquet:"name=parsed_text, type=MAP, convertedtype=LIST, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
}

type CompressedFileReader[R zip.Reader | tar.Reader] interface {
	getReader() (R, error)
	close() float64
}

type ZipReader struct { //todo: define methods for zip
	File         os.File
	readerCloser zip.ReadCloser
}

type TarReader struct { //todo: define methods for tar
	file       os.File
	readCloser tar.Reader
}

func (z *ZipReader) read(fileName string) (*bufio.Scanner, error) {
	for _, file := range z.readerCloser.File { //todo: fix this
		if strings.Contains(file.FileHeader.Name, fileName) {
			spew.Dump(file.FileHeader)
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

func read(ch chan ReviewSchema, doneCh chan struct{}, s *bufio.Scanner, stop int) {
	defer close(ch)
	defer close(doneCh)

	pattern := regexp.MustCompile(`[a-z0-9'-]+`)

	for counter := 0; s.Scan(); counter++ {
		var jsonl ReviewSchema
		if err := json.Unmarshal(s.Bytes(), &jsonl); err != nil {
			log.Panic(err, "unmarshalling err")
		}
		jsonl.ParsedText = pattern.FindAllString(jsonl.Text, 0)
		ch <- jsonl
		// spew.Dump(jsonl)
		if counter >= stop && stop != 0 {
			doneCh <- struct{}{}
			return
		}
	}

	if err := s.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "reading standard input:", err)
	}

	doneCh <- struct{}{}

}

func GetArchive(fileName string) *bufio.Scanner {
	files, err := os.Open(archive)
	if err != nil {
		log.Fatal(err)
	}

	archiveFiles := tar.NewReader(files)

	for {
		file, err := archiveFiles.Next()
		if err != nil {
			os.Exit(0)
		}
		fmt.Println()
		spew.Dump(file)
		if strings.Contains(file.Name, fileName) {
			return bufio.NewScanner(archiveFiles)
		}

	}
}

func ListDir() {
	files, err := os.ReadDir("../../data/")
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		fmt.Printf("listing files: %v, directory?: %v \n", file.Name(), file.IsDir())
	}
}

func Process(outFile string, fileScanner *bufio.Scanner) {
	start := time.Now()

	//write
	fw, err := local.NewLocalFileWriter(outFile)
	if err != nil {
		log.Println("Can't create file", err)
		return
	}
	defer fw.Close()

	pw, err := writer.NewParquetWriter(fw, new(ReviewSchema), 20)
	if err != nil {
		log.Println("Can't create json writer", err)
		return
	}

	ch := make(chan ReviewSchema, 10)
	doneCh := make(chan struct{})

	go func() {
		read(ch, doneCh, fileScanner, writeStopRow)
	}()

L:
	for {

		select {
		case <-doneCh:
			if pw.WriteStop() != nil {
				fmt.Println("write completed")
				break L
			}

		case item := <-ch:
			err = pw.Write(item)
			if err != nil {
				log.Panic(err)
			}
		}

	}

	fmt.Printf("process completed in %v\n", time.Since(start))
}

func ReadParquet(fileName string, earlyStop int64) {
	fr, err := local.NewLocalFileReader(fileName)
	if err != nil {
		log.Println("Can't open file", err.Error())
		return
	}
	defer fr.Close()
	// var test map[string]interface{}

	pr, err := reader.NewParquetReader(fr, new(ReviewSchema), 1)
	if err != nil {
		log.Println("Can't create parquet reader", err)
		return
	}

	n := pr.GetNumRows()
	stop := int64(
		math.Min(float64(n), float64(earlyStop)),
	)
	s := make([]ReviewSchema, stop)
	err = pr.Read(&s)
	if err != nil {
		log.Println("Can't read", err)
		return
	}

	spew.Dump("read files", s)
}

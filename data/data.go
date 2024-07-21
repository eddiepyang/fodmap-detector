package data

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"log"
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
	archive  = "../torch-sentiment/torch_sentiment/data/archive.zip"
	fileName = "test1.parquet"
	stop     = 500000
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

func read(ch chan ReviewSchema, doneCh chan struct{}, z zip.ReadCloser) {
	defer close(ch)
	defer close(doneCh)

	pattern := regexp.MustCompile(`[a-z0-9'-]+`)

	for _, file := range z.File {
		if strings.Contains(file.FileHeader.Name, "review") {
			spew.Dump(file.FileHeader)
			rc, err := file.Open()
			if err != nil {
				log.Panic(err)
			}
			defer rc.Close()
			scanner := bufio.NewScanner(rc)

			for counter := 0; scanner.Scan(); counter++ {
				var jsonl ReviewSchema
				if err := json.Unmarshal(scanner.Bytes(), &jsonl); err != nil {
					log.Panic(err, "unmarshalling err")
				}
				jsonl.ParsedText = pattern.FindAllString(jsonl.Text, -1)
				ch <- jsonl
				// spew.Dump(jsonl)
				if err != nil {
					log.Panic(err)
				}

				if counter >= stop && stop != 0 {
					doneCh <- struct{}{}
					return
				}
			}

			if err := scanner.Err(); err != nil {
				fmt.Fprintln(os.Stderr, "reading standard input:", err)
			}
		}

	}
	doneCh <- struct{}{}

}

func process() {
	start := time.Now()
	zipFile, err := zip.OpenReader(archive)
	if err != nil {
		log.Panic(err)
	}
	defer zipFile.Close()

	//write
	fw, err := local.NewLocalFileWriter(fileName)
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

	ch := make(chan ReviewSchema, 100)
	doneCh := make(chan struct{})

	go func() {
		read(ch, doneCh, *zipFile)
	}()

L:
	for {

		select {
		case <-doneCh:
			if err := pw.WriteStop(); err != nil {
				log.Panic(err)
			}
			break L
		case item := <-ch:
			err = pw.Write(item)
			if err != nil {
				log.Panic(err)
			}
		}

	}

	fmt.Printf("process completed in %v", time.Since(start))
}

func ReadParquet(filename string) {
	fr, err := local.NewLocalFileReader(fileName)
	if err != nil {
		log.Println("Can't open file")
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
	for i := int64(0); i < n; i++ {
		s := make([]ReviewSchema, 1)
		err = pr.Read(&s)
		if err != nil {
			log.Println("Can't read", err)
			return
		}

		spew.Dump("read files", s)
	}
}

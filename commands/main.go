package main

import (
	"fodmap/data"
	"log"
)

func main() {

	// data.ListDir()
	fileScanner := data.GetArchive("review")
	log.Printf("created fileScanner")
	data.Process("test.parquet", fileScanner)
	log.Printf("created file")
	data.ReadParquet("test.parquet", 5)
	// files, err := os.ReadDir("../data")
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// for _, file := range files {
	// 	log.Println(file.Name(), file.IsDir())
	// }
}

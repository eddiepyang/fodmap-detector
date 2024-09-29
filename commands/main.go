package main

import (
	"test-server/data"
)

func main() {

	// data.ListDir()
	// fileScanner := data.GetArchive("review")

	// data.Process("test.parquet", fileScanner)
	data.ReadParquet("test.parquet", 5)
	// files, err := os.ReadDir("../data")
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// for _, file := range files {
	// 	fmt.Println(file.Name(), file.IsDir())
	// }
}

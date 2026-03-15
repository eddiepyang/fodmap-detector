package main

import (
	"log/slog"

	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/reader"
	"github.com/xitongsys/parquet-go/writer"
)

type Student struct {
	NameIn  string
	Age     int32
	Id      int64
	Weight  float32
	Sex     bool
	Classes []string
	Scores  map[string][]float32
	Ignored string

	Friends []struct {
		Name string
		Id   int64
	}
	Teachers []struct {
		Name string
		Id   int64
	}
}

var jsonSchema string = `
{
  "Tag": "name=parquet_go_root, repetitiontype=REQUIRED",
  "Fields": [
    {"Tag": "name=name, inname=NameIn, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},
    {"Tag": "name=age, inname=Age, type=INT32, repetitiontype=REQUIRED"},
    {"Tag": "name=id, inname=Id, type=INT64, repetitiontype=REQUIRED"},
    {"Tag": "name=weight, inname=Weight, type=FLOAT, repetitiontype=REQUIRED"},
    {"Tag": "name=sex, inname=Sex, type=BOOLEAN, repetitiontype=REQUIRED"},

    {"Tag": "name=classes, inname=Classes, type=LIST, repetitiontype=REQUIRED",
     "Fields": [{"Tag": "name=element, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"}]
    },

    {
      "Tag": "name=scores, inname=Scores, type=MAP, repetitiontype=REQUIRED",
      "Fields": [
        {"Tag": "name=key, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},
        {"Tag": "name=value, type=LIST, repetitiontype=REQUIRED",
         "Fields": [{"Tag": "name=element, type=FLOAT, repetitiontype=REQUIRED"}]
        }
      ]
    },

    {
      "Tag": "name=friends, inname=Friends, type=LIST, repetitiontype=REQUIRED",
      "Fields": [
       {"Tag": "name=element, repetitiontype=REQUIRED",
        "Fields": [
         {"Tag": "name=name, inname=Name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},
         {"Tag": "name=id, inname=Id, type=INT64, repetitiontype=REQUIRED"}
        ]}
      ]
    },

    {
      "Tag": "name=teachers, inname=Teachers, repetitiontype=REPEATED",
      "Fields": [
        {"Tag": "name=name, inname=Name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},
        {"Tag": "name=id, inname=Id, type=INT64, repetitiontype=REQUIRED"}
      ]
    }
  ]
}
`

func main() {
	var err error
	fw, err := local.NewLocalFileWriter("json_schema.parquet")
	if err != nil {
		slog.Error("can't create local file", "error", err)
		return
	}

	pw, err := writer.NewParquetWriter(fw, jsonSchema, 4)
	if err != nil {
		slog.Error("can't create parquet writer", "error", err)
		return
	}

	pw.RowGroupSize = 128 * 1024 * 1024
	pw.CompressionType = parquet.CompressionCodec_SNAPPY
	num := 10
	for i := 0; i < num; i++ {
		stu := Student{
			NameIn:  "StudentName",
			Age:     int32(20 + i%5),
			Id:      int64(i),
			Weight:  float32(50.0 + float32(i)*0.1),
			Sex:     bool(i%2 == 0),
			Classes: []string{"Math", "Physics"},
			Scores: map[string][]float32{
				"Math":    {89.5, 99.4},
				"Physics": {100.0, 95.3},
			},

			Friends: []struct {
				Name string
				Id   int64
			}{
				{Name: "Jack", Id: 1},
			},

			Teachers: []struct {
				Name string
				Id   int64
			}{
				{Name: "Tom", Id: 2},
			},
		}
		if err = pw.Write(stu); err != nil {
			slog.Error("write error", "error", err)
			return
		}
	}
	if err = pw.WriteStop(); err != nil {
		slog.Error("write stop error", "error", err)
		return
	}
	slog.Info("write finished")
	if err = fw.Close(); err != nil {
		slog.Error("close error", "error", err)
	}

	fr, err := local.NewLocalFileReader("json_schema.parquet")
	if err != nil {
		slog.Error("can't open file", "error", err)
		return
	}

	pr, err := reader.NewParquetReader(fr, jsonSchema, 4)
	if err != nil {
		slog.Error("can't create parquet reader", "error", err)
		return
	}

	num = int(pr.GetNumRows())
	for i := 0; i < num; i++ {
		stus := make([]Student, 1)
		if err = pr.Read(&stus); err != nil {
			slog.Error("read error", "error", err)
			return
		}
		slog.Info("row", "data", stus)
	}

	pr.ReadStop()
	if err = fr.Close(); err != nil {
		slog.Error("close error", "error", err)
	}
}

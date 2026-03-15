package io

import (
	"io"
	"log/slog"
	"os"

	"github.com/hamba/avro/v2/ocf"
)

type EventWriter struct {
	encoder *ocf.Encoder
	closer  io.Closer
}

func NewEventWriter(w io.WriteCloser, outputSchema string) (*EventWriter, error) {
	enc, err := ocf.NewEncoder(outputSchema, w)
	if err != nil {
		_ = w.Close()
		return nil, err
	}

	return &EventWriter{encoder: enc, closer: w}, nil
}

func (w *EventWriter) Write(record map[string]any) error {
	for k, v := range record {
		if f, ok := v.(float64); ok {
			record[k] = float32(f)
		}
	}
	slog.Debug("record to be written")
	return w.encoder.Encode(record)
}

func (w *EventWriter) Close() error {
	return w.closer.Close()
}

func ReadFile(filePath string) error {
	avroFile, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := avroFile.Close(); err != nil {
			slog.Error("close error", "error", err)
		}
	}()

	decoder, err := ocf.NewDecoder(avroFile)
	if err != nil {
		return err
	}

	for decoder.HasNext() {
		var datum any
		if err := decoder.Decode(&datum); err != nil {
			return err
		}
		slog.Debug("decoded avro record")
	}

	return decoder.Error()
}

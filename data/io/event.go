package io

import (
	"io"
	"log/slog"
	"os"

	"github.com/hamba/avro/v2/ocf"
)

// EventWriter writes Avro OCF records to an underlying write closer.
type EventWriter struct {
	encoder *ocf.Encoder
	closer  io.Closer
}

// NewEventWriter creates an EventWriter that encodes records using the given
// Avro schema string.
func NewEventWriter(w io.WriteCloser, outputSchema string) (*EventWriter, error) {
	enc, err := ocf.NewEncoder(outputSchema, w)
	if err != nil {
		_ = w.Close()
		return nil, err
	}

	return &EventWriter{encoder: enc, closer: w}, nil
}

// Write encodes a single record into the Avro OCF stream.
func (w *EventWriter) Write(record map[string]any) error {
	for k, v := range record {
		if f, ok := v.(float64); ok {
			record[k] = float32(f)
		}
	}
	slog.Debug("record to be written")
	return w.encoder.Encode(record)
}

// WriteRaw encodes a record directly without float64->float32 coercion.
func (w *EventWriter) WriteRaw(record any) error {
	return w.encoder.Encode(record)
}

// Close closes the underlying writer.
func (w *EventWriter) Close() error {
	return w.closer.Close()
}

// ReadFile decodes all records from an Avro OCF file for inspection.
func ReadFile(filePath string) error {
	avroFile, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = avroFile.Close() }()

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

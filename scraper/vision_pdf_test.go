package scraper

import (
	"context"
	"strings"
	"testing"
)

func TestExtractPDFText_InvalidPDF(t *testing.T) {
	_, err := ExtractPDFText([]byte("not a pdf"), false)
	if err != ErrNeedVision {
		t.Errorf("expected ErrNeedVision, got %v", err)
	}
}

func TestExtractPDFText_WithPdftotext(t *testing.T) {
	// Without pdftotext installed, or with invalid PDF, it should fail
	_, err := ExtractPDFText([]byte("not a pdf"), true)
	if err != ErrNeedVision {
		t.Errorf("expected ErrNeedVision, got %v", err)
	}
}

func TestRenderPDFPages_InvalidPDF(t *testing.T) {
	_, err := RenderPDFPages([]byte("not a pdf"))
	if err == nil || !strings.Contains(err.Error(), "pdfcpu extract images") {
		t.Errorf("expected pdfcpu error, got %v", err)
	}
}

func TestExtractPDFVision_InvalidPDF(t *testing.T) {
	ext, err := NewOpenAICompatExtractor("http://localhost/v1", "test", "", "none")
	if err != nil {
		t.Fatalf("NewOpenAICompatExtractor: %v", err)
	}
	_, err = ExtractPDFVision(context.Background(), []byte("not a pdf"), ext)
	if err == nil || !strings.Contains(err.Error(), "rendering PDF") {
		t.Errorf("expected rendering error, got %v", err)
	}
}

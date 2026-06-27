package scraper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// ExtractPDFText attempts to extract text from a PDF using a cascade:
//  1. ledongthuc/pdf text-layer extraction (fast, pure Go)
//  2. system pdftotext (if usePdftotext is true and binary is found)
//  3. returns ("", ErrNeedVision) signalling the caller should use the vision path
//
// If the text layer yields ≥ minPDFTextChars, it is returned immediately.
func ExtractPDFText(pdfBytes []byte, usePdftotext bool) (string, error) {
	// 1. Try text-layer extraction.
	text, err := extractTextLayer(pdfBytes)
	if err == nil && len([]rune(text)) >= minPDFTextChars {
		return text, nil
	}

	// 2. Try system pdftotext.
	if usePdftotext {
		if t, err := runPdftotext(pdfBytes); err == nil && len([]rune(t)) >= minPDFTextChars {
			return t, nil
		}
	}

	return "", ErrNeedVision
}

// ErrNeedVision is returned when no text-layer PDF extraction succeeded and
// the caller should fall back to the vision LLM path.
var ErrNeedVision = fmt.Errorf("PDF has no usable text layer; vision LLM required")

// extractTextLayer uses ledongthuc/pdf to read embedded text.
func extractTextLayer(pdfBytes []byte) (string, error) {
	r := bytes.NewReader(pdfBytes)
	reader, err := pdf.NewReader(r, int64(len(pdfBytes)))
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		t, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		sb.WriteString(t)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

// runPdftotext invokes the system pdftotext binary (poppler).
// Returns an error if the binary is not found.
func runPdftotext(pdfBytes []byte) (string, error) {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdftotext not found in PATH: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-layout", "-", "-")
	cmd.Stdin = bytes.NewReader(pdfBytes)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	return string(out), nil
}

// RenderPDFPages extracts embedded images from a PDF using pdfcpu's
// ExtractImages API. This works for PDFs with embedded raster images but will
// fail for PDFs that use vector/drawn content or text-only pages. For those,
// an external tool like pdftoppm (poppler) is needed as a fallback.
func RenderPDFPages(pdfBytes []byte) ([][]byte, error) {
	rs := bytes.NewReader(pdfBytes)
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	var pages [][]byte

	err := api.ExtractImages(rs, nil, func(img model.Image, singleImgPerPage bool, pageNr int) error {
		data, rerr := io.ReadAll(img)
		if rerr != nil {
			return nil
		}
		pages = append(pages, data)
		return nil
	}, conf)
	if err != nil {
		return nil, fmt.Errorf("pdfcpu extract images: %w", err)
	}

	if len(pages) == 0 {
		return nil, fmt.Errorf("pdfcpu: no embedded images found in PDF; consider --pdftotext or --enable-vision for text-only or vector PDFs")
	}
	return pages, nil
}

// ExtractPDFVision renders the PDF to images and calls the vision LLM on each
// page, merging the results.
func ExtractPDFVision(ctx context.Context, pdfBytes []byte, ex *OpenAICompatExtractor) (MenuExtractionResult, error) {
	pages, err := RenderPDFPages(pdfBytes)
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("rendering PDF: %w", err)
	}

	var merged MenuExtractionResult
	for i, page := range pages {
		result, err := ex.ExtractImage(ctx, page, "")
		if err != nil {
			return MenuExtractionResult{}, fmt.Errorf("vision extraction page %d: %w", i+1, err)
		}
		if merged.RestaurantName == "" {
			merged.RestaurantName = result.RestaurantName
			merged.City = result.City
			merged.State = result.State
		}
		merged.Items = append(merged.Items, result.Items...)
	}
	return merged, nil
}

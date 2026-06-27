package scraper

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "golang.org/x/image/webp"
)

// newTestExtractor builds an extractor pointed at srv.URL+"/v1" so the appended
// "/chat/completions" suffix produces the correct path that httptest expects.
func newTestExtractor(t *testing.T, baseURL, model, apiKey string) *OpenAICompatExtractor {
	t.Helper()
	ex, err := NewOpenAICompatExtractor(baseURL+"/v1", model, apiKey, "none")
	if err != nil {
		t.Fatalf("NewOpenAICompatExtractor: %v", err)
	}
	return ex
}

func makeChoiceResp(content, reasoningContent, reasoning, finishReason string) chatResponse {
	r := chatResponse{}
	r.Choices = append(r.Choices, struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			Reasoning        string `json:"reasoning,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	}{
		Message: struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			Reasoning        string `json:"reasoning,omitempty"`
		}{
			Content:          content,
			ReasoningContent: reasoningContent,
			Reasoning:        reasoning,
		},
		FinishReason: finishReason,
	})
	return r
}

func TestOpenAICompatExtractor_Extract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		resp := makeChoiceResp(`{"restaurant_name":"","items":[{"dish":"pizza","description":"cheese","stated_ingredients":[],"has_full_ingredients":false}]}`, "", "", "stop")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "test-key")
	res, err := ext.Extract(context.Background(), "pizza menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(res.Items))
	}
	if res.Items[0].DishName != "pizza" {
		t.Errorf("expected pizza, got %s", res.Items[0].DishName)
	}
}

func TestOpenAICompatExtractor_RequestPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.ResponseFormat == nil {
			t.Fatal("ResponseFormat is nil")
		}
		if req.ResponseFormat.Type != "json_schema" {
			t.Errorf("response_format.type = %q; want json_schema", req.ResponseFormat.Type)
		}
		if req.ResponseFormat.JSONSchema == nil {
			t.Fatal("ResponseFormat.JSONSchema is nil")
		}
		if !req.ResponseFormat.JSONSchema.Strict {
			t.Error("json_schema.strict should be true")
		}
		// Schema should contain our key fields.
		schemaStr := string(req.ResponseFormat.JSONSchema.Schema)
		for _, field := range []string{"restaurant_name", "items"} {
			if !strings.Contains(schemaStr, field) {
				t.Errorf("schema missing field %q", field)
			}
		}
		if req.ReasoningEffort != "none" {
			t.Errorf("reasoning_effort = %q; want none", req.ReasoningEffort)
		}
		resp := makeChoiceResp(`{"restaurant_name":"","items":[]}`, "", "", "stop")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	_, err := ext.Extract(context.Background(), "menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICompatExtractor_ReasoningContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := makeChoiceResp(
			`{"restaurant_name":"Joe's","items":[{"dish":"pizza","description":"cheese","stated_ingredients":[],"has_full_ingredients":false}]}`,
			"<think>Let me extract the menu...</think>",
			"",
			"stop",
		)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	res, err := ext.Extract(context.Background(), "pizza menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RestaurantName != "Joe's" {
		t.Errorf("expected Joe's, got %q", res.RestaurantName)
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "pizza" {
		t.Errorf("unexpected items: %+v", res.Items)
	}
}

func TestOpenAICompatExtractor_ReasoningFieldFallback(t *testing.T) {
	// vLLM uses "reasoning" not "reasoning_content" after their rename.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := makeChoiceResp(
			`{"restaurant_name":"","items":[{"dish":"burger","description":"beef","stated_ingredients":["beef"],"has_full_ingredients":true}]}`,
			"",
			"I thought about this carefully...",
			"stop",
		)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	res, err := ext.Extract(context.Background(), "burger menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "burger" {
		t.Errorf("unexpected items: %+v", res.Items)
	}
}

func TestOpenAICompatExtractor_EmptyContentReasoningOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := makeChoiceResp("", "all my thoughts here", "", "stop")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	_, err := ext.Extract(context.Background(), "menu")
	if err == nil {
		t.Fatal("expected error for empty content with reasoning")
	}
	if !strings.Contains(err.Error(), "--llm-reasoning-effort") {
		t.Errorf("error should mention --llm-reasoning-effort, got: %v", err)
	}
}

func TestOpenAICompatExtractor_ExtractImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode err: %v", err)
		}
		if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
			t.Fatalf("unexpected message format")
		}
		if req.Messages[0].Content[1].Type != "image_url" {
			t.Errorf("expected image_url content part")
		}
		// ExtractImage normalizes input to PNG; the data URL must be image/png.
		if !strings.HasPrefix(req.Messages[0].Content[1].ImageURL.URL, "data:image/png;base64,") {
			t.Errorf("expected data:image/png base64 URL, got %q", req.Messages[0].Content[1].ImageURL.URL)
		}
		resp := makeChoiceResp(`{"restaurant_name":"","items":[]}`, "", "", "stop")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	res, err := ext.ExtractImage(context.Background(), encodePNG(t, 2, 2), "image/png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("expected 0 items")
	}
}

// encodePNG encodes a small solid-color image as PNG bytes for test fixtures.
func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

// encodeJPEG encodes a small solid-color image as JPEG bytes for test fixtures.
func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	return buf.Bytes()
}

// decodeWebpFixture returns real PNG bytes. image.Decode sniffs the actual
// content (not the caller's MIME label), so PNG bytes survive the webp-MIME
// normalization path — proving the MIME-spoofing case where a server labels a
// PNG as image/webp. A genuine webp would need the golang.org/x/image/webp
// decoder (registered via blank import in openai_extractor.go); this fixture
// exercises the same code path without bundling a binary webp fixture.
func decodeWebpFixture(t *testing.T) []byte {
	t.Helper()
	return encodePNG(t, 2, 2)
}

func TestOpenAICompatExtractor_ExtractImage_ConvertsJPEGToPNGDataUrl(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode err: %v", err)
		}
		if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
			t.Fatalf("expected 1 message with 2 content parts, got %d messages", len(req.Messages))
		}
		gotURL = req.Messages[0].Content[1].ImageURL.URL
		_ = json.NewEncoder(w).Encode(makeChoiceResp(
			`{"restaurant_name":"Cafe","items":[{"dish":"Latte","description":"","stated_ingredients":[],"has_full_ingredients":false}]}`,
			"", "", "stop"))
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	jpegBytes := encodeJPEG(t, 4, 4)
	res, err := ext.ExtractImage(context.Background(), jpegBytes, "image/jpeg")
	if err != nil {
		t.Fatalf("ExtractImage: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "Latte" {
		t.Errorf("parsed result wrong: %+v", res)
	}
	// The sent data URL must be a PNG (normalization happened), not a JPEG.
	if !strings.HasPrefix(gotURL, "data:image/png;base64,") {
		t.Errorf("expected data:image/png base64 URL after normalization, got prefix %q", gotURL[:min(len(gotURL), 40)])
	}
	// And the payload must actually decode as a PNG.
	b64 := strings.TrimPrefix(gotURL, "data:image/png;base64,")
	dec, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if _, ct, err := image.DecodeConfig(bytes.NewReader(dec)); err != nil || ct != "png" {
		t.Errorf("decoded payload is not a PNG: err=%v ct=%q", err, ct)
	}
}

func TestOpenAICompatExtractor_ExtractImage_EmptyMimePngBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Messages[0].Content) < 2 {
			t.Fatalf("expected image content part")
		}
		if !strings.HasPrefix(req.Messages[0].Content[1].ImageURL.URL, "data:image/png;base64,") {
			t.Errorf("expected PNG data URL for empty MIME with PNG bytes")
		}
		_ = json.NewEncoder(w).Encode(makeChoiceResp(`{"restaurant_name":"","items":[]}`, "", "", "stop"))
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	pngBytes := encodePNG(t, 2, 2)
	_, err := ext.ExtractImage(context.Background(), pngBytes, "")
	if err != nil {
		t.Fatalf("ExtractImage empty mime: %v", err)
	}
}

func TestOpenAICompatExtractor_ExtractImage_WebpBytesNormalized(t *testing.T) {
	// Webp bytes (here represented by decodeable PNG bytes, since image.Decode
	// sniffs content — proves the MIME-spoofing case: a server labels a PNG as
	// image/webp; the normalizer decodes the real bytes and re-encodes PNG).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !strings.HasPrefix(req.Messages[0].Content[1].ImageURL.URL, "data:image/png;base64,") {
			t.Errorf("expected PNG data URL even when MIME claims webp")
		}
		_ = json.NewEncoder(w).Encode(makeChoiceResp(`{"restaurant_name":"","items":[]}`, "", "", "stop"))
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	webpBytes := decodeWebpFixture(t)
	if _, err := ext.ExtractImage(context.Background(), webpBytes, "image/webp"); err != nil {
		t.Fatalf("ExtractImage webp: %v", err)
	}
}

func TestOpenAICompatExtractor_ExtractImage_UndecodableReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(makeChoiceResp(`{"restaurant_name":"","items":[]}`, "", "", "stop"))
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	if _, err := ext.ExtractImage(context.Background(), []byte("not-an-image"), "image/png"); err == nil {
		t.Fatal("expected error for undecodable image bytes")
	}
}

func TestOpenAICompatExtractor_ImplementsImageExtractor(t *testing.T) {
	var _ ImageExtractor = (*OpenAICompatExtractor)(nil)
}

func TestOpenAICompatExtractor_Errors(t *testing.T) {
	t.Run("HTTP 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		ext := newTestExtractor(t, srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("Empty response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := makeChoiceResp("", "", "", "length")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		ext := newTestExtractor(t, srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("No choices", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(chatResponse{})
		}))
		defer srv.Close()

		ext := newTestExtractor(t, srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := makeChoiceResp("{invalid json}", "", "", "stop")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		ext := newTestExtractor(t, srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})
}

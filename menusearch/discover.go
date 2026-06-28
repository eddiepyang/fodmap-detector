package menusearch

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"google.golang.org/genai"
)

type DiscoverMenuURLWorker struct {
	river.WorkerDefaults[DiscoverMenuURLArgs]
	Store       *Store
	GenAIClient *genai.Client
	RiverClient *river.Client[pgx.Tx]
	AvroDestDir string
	GeminiModel string
}

func (w *DiscoverMenuURLWorker) Work(ctx context.Context, job *river.Job[DiscoverMenuURLArgs]) error {
	args := job.Args
	logger := slog.With("job", job.ID, "camis", args.CAMIS, "dba", args.DBA)
	logger.Info("starting discovery job")

	prompt := fmt.Sprintf(
		"Find the official website URL for the restaurant %q at %s. "+
			"Return only the URL(s), one per line.",
		args.DBA, args.Address)

	tool := &genai.Tool{
		GoogleSearch: &genai.GoogleSearch{},
	}

	res, err := w.GenAIClient.Models.GenerateContent(ctx, w.GeminiModel, genai.Text(prompt), &genai.GenerateContentConfig{
		Tools: []*genai.Tool{tool},
	})

	if err != nil {
		logger.Error("gemini request failed", "error", err)
		return err
	}

	if len(res.Candidates) == 0 {
		return fmt.Errorf("gemini returned no candidates")
	}

	text := ""
	for _, p := range res.Candidates[0].Content.Parts {
		if p.Text != "" {
			text += p.Text
		}
	}

	lines := strings.Split(text, "\n")
	var foundURLs []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if u, err := url.ParseRequestURI(line); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			foundURLs = append(foundURLs, line)
		}
	}

	eventID := uuid.NewString()
	record := GeminiDiscoveryRecord{
		CAMIS:        args.CAMIS,
		DBA:          args.DBA,
		Prompt:       prompt,
		ResponseText: text,
		SourceURLs:   foundURLs,
		Model:        w.GeminiModel,
		EventID:      eventID,
		JobID:        fmt.Sprintf("%d", job.ID),
		Attempt:      args.Attempt,
	}

	avroDest := filepath.Join(w.AvroDestDir, fmt.Sprintf("%s.avro", eventID))
	if err := WriteGeminiDiscoveryAvro(ctx, avroDest, record); err != nil {
		logger.Error("failed to write avro", "error", err)
	}

	if len(foundURLs) > 0 {
		menuURL := foundURLs[0]
		logger.Info("found URL", "url", menuURL)

		err = w.Store.UpdateMenuURL(ctx, args.CAMIS, menuURL, "gemini_discovery")
		if err != nil {
			return fmt.Errorf("update menu url: %w", err)
		}

		_, err = w.RiverClient.Insert(ctx, ScrapeMenuArgs{
			CAMIS:   args.CAMIS,
			URL:     menuURL,
			DBA:     args.DBA,
			Attempt: 1,
		}, nil)
		if err != nil {
			return fmt.Errorf("enqueue scrape: %w", err)
		}
	} else {
		logger.Info("no URL found")
		err = w.Store.UpdateMenuURL(ctx, args.CAMIS, "", "gemini_discovery")
		if err != nil {
			return fmt.Errorf("update menu url: %w", err)
		}
	}

	return nil
}

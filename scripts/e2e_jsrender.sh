#!/usr/bin/env bash
#
# e2e_jsrender.sh — run the FODMAP detector's generic JS render-and-re-cascade
# path against JS-rendered NYC restaurants from
# docs/plans/vision-extraction-gaps-plan.md (Phase 3).
#
# SLOW + network-dependent (~3-5 min per site). NOT part of `make check`. Needs
# Google Chrome / Chromium installed (chromedp finds it automatically) and a
# running OpenAI-compatible LLM (vLLM qwen3-vl by default). Does NOT need
# Weaviate or the Python scraper service.
#
# Usage:
#   scripts/e2e_jsrender.sh [--llm-url URL] [--llm-model MODEL] [--timeout 40m] [--v]
#
# Exit 0 = all sites met expectations; 1 = at least one failed.
set -euo pipefail

cd "$(dirname "$0")/.."

go run ./scripts/e2e_jsrender --timeout 40m "$@"
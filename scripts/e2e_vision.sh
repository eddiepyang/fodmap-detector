#!/usr/bin/env bash
#
# e2e_vision.sh — run the FODMAP detector's Phase C vision extraction path
# against the 9 live NYC restaurants from docs/plans/vision-extraction-gaps-plan.md.
#
# This is a SLOW, network-dependent e2e test (single-image vision is ~100-180s
# per site; the full run is ~15-25 min). It is NOT part of `make check`. It
# requires a running OpenAI-compatible vision LLM (vLLM/vllm-metal with
# qwen3-vl 8-bit, by default) but does NOT require Weaviate or the Python
# scraper service.
#
# Usage:
#   scripts/e2e_vision.sh [--llm-url URL] [--llm-model MODEL] [--timeout 40m] [--v]
#
# Exit code 0 = all sites met expectations; 1 = at least one failed.
#
# See docs/guides/vision-extraction.md for background and design notes.
set -euo pipefail

cd "$(dirname "$0")/.."

# Pass through all args to the Go program; default to a generous timeout so a
# cold first run (model load) doesn't trip the default.
go run ./scripts/e2e_vision --timeout 40m "$@"
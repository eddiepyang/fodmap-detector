# LLM Serving Plan

> **Scope:** Companion to [scraper-pipeline-plan.md](scraper-pipeline-plan.md). Documents how to serve the **single vision-capable LLM** the scraper uses for both menu extraction (text) and PDF/image OCR (vision). The pipeline is platform-agnostic — only the `--llm-url` and `--llm-model` flags change.

## Two supported dev configurations

This project is developed on two machines with very different VRAM budgets, so we plan for both explicitly.

| Machine | Recommended config | Why |
|---|---|---|
| **Mac M2 (64 GB unified)** | Ollama + `qwen3.6:35b-mlx` | One install (Ollama). 22 GB model, leaves ~28 GB usable. MLX-optimized. Vision + tools + thinking in one model. |
| **Linux + NVIDIA 5080 (16 GB VRAM)** | vLLM + `Qwen/Qwen3-VL-8B-Instruct-AWQ` | AWQ 4-bit quant ≈ 6 GB weights, leaves ~8 GB for KV cache. Best vision quality that fits the card. Blackwell needs CUDA 12.6+. |

Both expose `POST /v1/chat/completions`; the Go pipeline uses one OpenAI-compatible HTTP client — backend choice is purely the `--llm-url` you pass (must include the version segment, e.g. `/v1`).

## Mac M2 setup — Ollama (recommended default)

```bash
# Install Ollama (one-time): https://ollama.com/download
ollama pull qwen3.6:35b-mlx        # 22 GB
# --reasoning-parser routes <think> tokens into the reasoning_content response
# field so strict JSON output works with reasoning models.
ollama serve --reasoning-parser deepseek_r1   # default port :11434
```

Point the scraper:

```bash
go run . scrape <url> \
  --llm-url http://localhost:11434/v1 \
  --llm-model qwen3.6:35b-mlx \
  --llm-reasoning-effort none \
  --enable-vision
```

**Sanity check the model is loaded and vision works:**
```bash
curl http://localhost:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.6:35b-mlx",
    "messages": [{"role": "user", "content": "What model are you? Reply in one word."}]
  }'
```

**Notes:**
- 64 GB unified memory lets you keep this model resident across many requests without swapping. First-load is ~30 seconds; subsequent calls are warm.
- Throughput on M2 is typically 15–25 tok/sec for this MoE; plenty for one-off menu scrapes.
- Ollama handles vision via the OpenAI `image_url` content-part wire format — confirmed against Ollama's own docs example, which uses `qwen3-vl:8b` with `image_url` content parts. Note: must be base64 data-URL; remote URLs not supported.

## Mac M2 setup — vllm-metal (alternative)

vLLM has an official Apple Silicon plugin: [`vllm-project/vllm-metal`](https://github.com/vllm-project/vllm-metal). Actively maintained (v0.2.0 April 2026), 1.3k stars, loads MLX-format models natively. Worth using over Ollama if you specifically want vLLM's API surface (paged attention, prefix caching, OpenAI feature parity) or higher throughput for batch workloads.

**Support matrix (from their supported_models.md):**
- Qwen3.5 / Qwen3.6 text-only: ✅ **Supported** (note: matrix lists "Qwen3.5 / 3.6 ✅ Hybrid SDPA + GDN linear (3.6 adds MoE)")
- Qwen3-VL multimodal: 🔵 **Experimental** (image input only, no video; example: `mlx-community/Qwen3-VL-4B-Instruct-4bit`)
- Other ✅ models: Qwen3, Gemma 3, Llama 3, Mistral-7B, DeepSeek-R1-Distill-Qwen

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/vllm-project/vllm-metal/main/install.sh | bash

# Serve Qwen3.6 text-only (matches the Qwen3.5/3.6 ✅ row in their matrix)
vllm serve <upstream-HF-checkpoint-name>     # e.g. mlx-community/<exact-tag>

# Serve Qwen3-VL for vision (experimental support)
vllm serve mlx-community/Qwen3-VL-4B-Instruct-4bit
```

Then point the scraper at `--llm-url http://localhost:8000/v1` with the matching `--llm-model`.

**Caveats:**
- You load the **upstream HuggingFace MLX checkpoint** (e.g. from `mlx-community/`), not Ollama's bundled blob. Ollama stores weights in its own layout that vllm-metal doesn't read.
- For *vision* on Mac, Ollama is currently more polished. vllm-metal's Qwen3-VL is 🔵 experimental and confined to image-only requests on the paged backend.
- If you only need text extraction (e.g. you've configured the pipeline without `--enable-vision`), vllm-metal serving Qwen3.6 is a fine choice.

## Linux + 5080 setup

The 5080 (Blackwell, 16 GB) needs CUDA 12.6+ and a recent vLLM build (≥0.7).

```bash
# Pull and run vLLM with the AWQ-quantized Qwen3-VL-8B
docker run --gpus all --rm -p 8000:8000 \
  --ipc=host \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  vllm/vllm-openai:latest \
  --model Qwen/Qwen3-VL-8B-Instruct-AWQ \
  --quantization awq_marlin \
  --max-model-len 8192 \
  --gpu-memory-utilization 0.85
```

Why these flags:
- `--quantization awq_marlin` — AWQ 4-bit, ~6 GB weights, fast on Blackwell
- `--max-model-len 8192` — bounds KV cache so it fits in remaining VRAM
- `--gpu-memory-utilization 0.85` — leaves headroom for OS / display server
- `--ipc=host` — required for shared memory IPC inside Docker

Point the scraper:

```bash
go run . scrape <url> \
  --llm-url http://localhost:8000/v1 \
  --llm-model Qwen/Qwen3-VL-8B-Instruct-AWQ \
  --llm-reasoning-effort none \
  --enable-vision
```

**Sanity check:**
```bash
curl http://localhost:8000/v1/models    # Should list the loaded model
```

**Notes:**
- First run downloads ~6 GB of weights from HuggingFace into `~/.cache/huggingface`. Subsequent runs are fast.
- vLLM on Blackwell still has occasional rough edges as of 2026 — if you hit a kernel crash, try `--enforce-eager` to disable CUDA graphs (slower but more reliable).
- Throughput is much higher than Ollama on Mac (~100+ tok/sec). For batch scraping, Linux is the better target.

## Makefile targets

```make
# Pull and start Mac stack (Ollama must be installed)
.PHONY: llm-mac-up llm-mac-down llm-mac-status
llm-mac-up:
	@ollama pull qwen3.6:35b-mlx
	@pgrep -x ollama > /dev/null || (ollama serve --reasoning-parser deepseek_r1 > tmp/ollama.log 2>&1 &)
	@echo "Ollama serving qwen3.6:35b-mlx on :11434"
llm-mac-status:
	@curl -s http://localhost:11434/api/tags | head -c 200

# Pull and start Linux+5080 stack (Docker + NVIDIA Container Toolkit required)
.PHONY: llm-linux-up llm-linux-down llm-linux-status
LLM_MODEL_LINUX ?= Qwen/Qwen3-VL-8B-Instruct-AWQ
llm-linux-up:
	@docker run -d --name fodmap-llm --gpus all -p 8000:8000 \
	  --ipc=host -v $$HOME/.cache/huggingface:/root/.cache/huggingface \
	  vllm/vllm-openai:latest --model $(LLM_MODEL_LINUX) \
	  --quantization awq_marlin --max-model-len 8192 --gpu-memory-utilization 0.85
llm-linux-down:
	@docker stop fodmap-llm && docker rm fodmap-llm
llm-linux-status:
	@curl -s http://localhost:8000/v1/models
```

## Quick reference: pointing the scraper at each backend

`--llm-url` must include the version segment. All backends accept `--llm-reasoning-effort` (default `none`).

| Backend | `--llm-url` | `--llm-model` | `--llm-api-key` |
|---|---|---|---|
| Ollama (Mac) | `http://localhost:11434/v1` | `qwen3.6:35b-mlx` | — |
| vLLM (Linux 5080) | `http://localhost:8000/v1` | `Qwen/Qwen3-VL-8B-Instruct-AWQ` | — |
| OpenAI (cloud) | `https://api.openai.com/v1` | `gpt-4o-mini` | required |
| Gemini (cloud) | `https://generativelanguage.googleapis.com/v1beta/openai` | `gemini-2.5-flash` | required |

**Gemini note:** Gemini accepts `reasoning_effort` but never returns a `reasoning_content` field — thinking tokens are billed silently. `--llm-reasoning-effort=none` is cost-optimal for the Gemini path. Gemini 3.x at `effort=low` can spend ~12× more tokens than `none` on a single extraction.

## Known Limitations

- **5080 + Qwen3-VL-8B-AWQ quality trails Chandra by ~21 points on olmOCR-Bench** (academic-document OCR). Acceptable for menus (less dense than papers) and required by VRAM constraints (Chandra + a text LLM doesn't fit in 16 GB). A future option is documented in scraper-pipeline-plan.md Known Limitations for >24 GB GPUs.
- **MLX vs CUDA quality drift**: `qwen3.6:35b-mlx` (Ollama, Mac) is a Qwen3.5 MoE checkpoint, while `Qwen3-VL-8B-Instruct-AWQ` (vLLM, Linux) is a dense Qwen3-VL model. Same family, different sizes, may produce slightly different extraction quality on the same input. Watch for drift via the prompt-regression test (see scraper-pipeline-plan.md Test Strategy).
- **Cold-start latency**: ~30 seconds on Mac (Ollama load), ~60 seconds on Linux (vLLM startup + weight download on first run). Keep the server warm during dev.
- **Production deployment**: out of scope for this doc. Production should run vLLM on a dedicated GPU host with a load balancer in front; that lives in the deploy pipeline, not the dev workflow.

## Related Plans

- [scraper-pipeline-plan.md](scraper-pipeline-plan.md) — the scraper pipeline this LLM serves
- [python-extractor-service-plan.md](python-extractor-service-plan.md) — **ARCHIVED.** Considered alternative.

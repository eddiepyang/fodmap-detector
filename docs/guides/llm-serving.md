# LLM Serving Plan

> **Scope:** Companion to [scraper-pipeline-plan.md](scraper-pipeline-plan.md). Documents how to serve the **single vision-capable LLM** the scraper uses for both menu extraction (text) and PDF/image OCR (vision). The pipeline is platform-agnostic — only the `--llm-url` and `--llm-model` flags change.

## Two supported dev configurations

This project is developed on two machines with very different VRAM budgets, so we plan for both explicitly.

| Machine | Recommended config | Why |
|---|---|---|
| **Mac M2 (64 GB unified)** | vllm-metal + `mlx-community/Qwen3-VL-8B-Instruct-8bit` | **Validated.** 8-bit MLX ≈ near-lossless; serves structured output via `response_format: json_schema` (enforced — unlike Ollama's MLX engine, which silently ignores it). ~8.5 GB weights, plenty of headroom. ~170 s/full-page menu on M2. |
| **Linux + NVIDIA 5080 (16 GB VRAM)** | vLLM + `Qwen/Qwen3-VL-8B-Instruct-FP8` | FP8 ≈ near-lossless (metrics ~identical to BF16), the CUDA equivalent of the Mac's MLX-8bit. ~8 GB weights + ~1–2 GB vision encoder + KV — fits 16 GB at bounded `--max-model-len`. Blackwell needs CUDA 12.6+. NVFP4 is a future pilot (see 5080 section). |

Both expose `POST /v1/chat/completions`; the Go pipeline uses one OpenAI-compatible HTTP client — backend choice is purely the `--llm-url` you pass (must include the version segment, e.g. `/v1`).

## Mac M2 setup — Ollama (chat only — NOT for structured menu extraction)

> ⚠ Ollama's MLX engine does not enforce JSON-schema output (`response_format` and even its native `format` field are ignored on MLX builds), so menu extraction degrades to prompt-only JSON and fails under retries. Use **vllm-metal** (above) for the extraction path. Ollama is fine for free-form chat or as a quick vision *read* check.

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

## Mac M2 setup — vllm-metal (recommended for vision + structured output)

vLLM has an official Apple Silicon plugin: [`vllm-project/vllm-metal`](https://github.com/vllm-project/vllm-metal). Actively maintained (v0.23, June 2026), loads MLX-format models natively.

**Use this, not Ollama, for the menu-extraction path.** Ollama's MLX engine **silently ignores `response_format: {type:"json_schema"}`** and does not enforce schema-constrained decoding at all (even its native `format` field is ignored on MLX builds), so structured extraction falls back to prompt-only JSON and breaks under retries. vllm-metal enforces the schema via xgrammar. Verified: `mlx-community/Qwen3-VL-8B-Instruct-8bit` does one-shot image→schema-valid JSON over `response_format: json_schema`.

**Support matrix (from their supported_models.md):**
- Qwen3.5 / Qwen3.6 text-only: ✅ **Supported** (note: matrix lists "Qwen3.5 / 3.6 ✅ Hybrid SDPA + GDN linear (3.6 adds MoE)")
- Qwen3-VL multimodal: 🔵 **Experimental** (image input only, no video; example: `mlx-community/Qwen3-VL-4B-Instruct-4bit`)
- Other ✅ models: Qwen3, Gemma 3, Llama 3, Mistral-7B, DeepSeek-R1-Distill-Qwen

```bash
# Install (one-time) into a dedicated venv, e.g. ~/.venv-vllm-metal
curl -fsSL https://raw.githubusercontent.com/vllm-project/vllm-metal/main/install.sh | bash

# Serve Qwen3-VL-8B 8-bit for vision + text (validated default)
vllm serve mlx-community/Qwen3-VL-8B-Instruct-8bit \
  --served-model-name qwen3-vl --port 8000 --trust-remote-code
```

Point the scraper:

```bash
go run . scrape <url> \
  --llm-url http://localhost:8000/v1 \
  --llm-model qwen3-vl \
  --llm-reasoning-effort none \
  --enable-vision
```

**Notes:**
- You load the **upstream HuggingFace MLX checkpoint** (`mlx-community/...`), not Ollama's bundled blob. Ollama stores weights in its own layout that vllm-metal doesn't read.
- **8-bit over 4-bit:** on a dense full-page menu, 4-bit (`...-4bit`) hallucinated a section header as the restaurant name and fabricated ingredient cross-attributions; 8-bit got the name right, captured more items, and (with the anti-cross-attribution prompt) produced zero fabricated ingredients. 8-bit is ~1.7× slower (~170 s vs ~100 s/full-page menu) — fine for one-off scrapes. The 4-bit `mlx-community/Qwen3-VL-4B-Instruct-4bit` remains an option for lower-memory / faster, lower-fidelity use.
- First load fetches ~8.5 GB into `~/.cache/huggingface`; subsequent starts are warm.

## Linux + 5080 setup

The 5080 (Blackwell, 16 GB) needs CUDA 12.6+ and a recent vLLM build (≥0.7).

```bash
# Pull and run vLLM with the FP8 Qwen3-VL-8B (near-lossless, ~8-bit class)
docker run --gpus all --rm -p 8000:8000 \
  --ipc=host \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  vllm/vllm-openai:latest \
  --model Qwen/Qwen3-VL-8B-Instruct-FP8 \
  --max-model-len 8192 \
  --gpu-memory-utilization 0.85
```

Why these flags:
- `Qwen/Qwen3-VL-8B-Instruct-FP8` — official FP8 quant, metrics ~identical to BF16; the CUDA equivalent of the Mac's MLX-8bit. ~8 GB weights + ~1–2 GB vision encoder; fits the 16 GB card with `--max-model-len` bounded. vLLM detects the FP8 checkpoint, so no explicit `--quantization` flag needed.
- `--max-model-len 8192` — bounds KV cache so weights + encoder + KV fit in 16 GB
- `--gpu-memory-utilization 0.85` — leaves headroom for OS / display server
- `--ipc=host` — required for shared memory IPC inside Docker

Point the scraper:

```bash
go run . scrape <url> \
  --llm-url http://localhost:8000/v1 \
  --llm-model Qwen/Qwen3-VL-8B-Instruct-FP8 \
  --llm-reasoning-effort none \
  --enable-vision
```

**FP8 vs INT4 vs NVFP4 on the 5080.** FP8 is near-lossless and the safest production precision as of mid-2026. AWQ-INT4 (`Qwen/Qwen3-VL-8B-Instruct-AWQ`, ~6 GB) is a valid lower-VRAM fallback but shows measurable degradation on reasoning (less on pure OCR). **NVFP4** — Blackwell's native 4-bit float (FP8 micro-scales on 16-value blocks + FP32 global scale, ~88% lower quant error than MXFP4/INT4) — retains ~FP8 quality (<1% degradation) at INT4 footprint and ~2.3× throughput, and is the better long-term 4-bit choice on the 5080 (sm_120). It's a **pilot, not the default**: there is no published Qwen3-VL-8B NVFP4 checkpoint yet (you'd self-quantize via llm-compressor/ModelOpt), and FP4 tooling for VL models is still maturing in vLLM. Start FP8; trial NVFP4 behind a task-specific eval.

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
# Pull and start Mac stack (vllm-metal installed in a venv, e.g. ~/.venv-vllm-metal)
.PHONY: llm-mac-up llm-mac-down llm-mac-status
LLM_MODEL_MAC ?= mlx-community/Qwen3-VL-8B-Instruct-8bit
VLLM_METAL_BIN ?= $$HOME/.venv-vllm-metal/bin/vllm
llm-mac-up:
	@pgrep -f "vllm serve" > /dev/null || ($(VLLM_METAL_BIN) serve $(LLM_MODEL_MAC) \
	  --served-model-name qwen3-vl --port 8000 --trust-remote-code > tmp/vllm-metal.log 2>&1 &)
	@echo "vllm-metal serving $(LLM_MODEL_MAC) as qwen3-vl on :8000"
llm-mac-status:
	@curl -s http://localhost:8000/v1/models | head -c 200

# Pull and start Linux+5080 stack (Docker + NVIDIA Container Toolkit required)
.PHONY: llm-linux-up llm-linux-down llm-linux-status
LLM_MODEL_LINUX ?= Qwen/Qwen3-VL-8B-Instruct-FP8
llm-linux-up:
	@docker run -d --name fodmap-llm --gpus all -p 8000:8000 \
	  --ipc=host -v $$HOME/.cache/huggingface:/root/.cache/huggingface \
	  vllm/vllm-openai:latest --model $(LLM_MODEL_LINUX) \
	  --max-model-len 8192 --gpu-memory-utilization 0.85
llm-linux-down:
	@docker stop fodmap-llm && docker rm fodmap-llm
llm-linux-status:
	@curl -s http://localhost:8000/v1/models
```

## Quick reference: pointing the scraper at each backend

`--llm-url` must include the version segment. All backends accept `--llm-reasoning-effort` (default `none`).

| Backend | `--llm-url` | `--llm-model` | `--llm-api-key` |
|---|---|---|---|
| vllm-metal (Mac) | `http://localhost:8000/v1` | `qwen3-vl` (served-model-name for `mlx-community/Qwen3-VL-8B-Instruct-8bit`) | — |
| vLLM (Linux 5080) | `http://localhost:8000/v1` | `Qwen/Qwen3-VL-8B-Instruct-FP8` | — |
| Ollama (Mac, chat only) | `http://localhost:11434/v1` | `qwen3.6:35b-mlx` | — |
| OpenAI (cloud) | `https://api.openai.com/v1` | `gpt-4o-mini` | required |
| Gemini (cloud) | `https://generativelanguage.googleapis.com/v1beta/openai` | `gemini-2.5-flash` | required |

**Gemini note:** Gemini accepts `reasoning_effort` but never returns a `reasoning_content` field — thinking tokens are billed silently. `--llm-reasoning-effort=none` is cost-optimal for the Gemini path. Gemini 3.x at `effort=low` can spend ~12× more tokens than `none` on a single extraction.

## Known Limitations

- **Dense full-page menus stress the 8B VLM.** On an options-heavy menu (categories of flavors/toppings/syrups as bare names), extraction is reliable for item names but `stated_ingredients` is best kept conservative — see the anti-cross-attribution + sibling-list rules in `scraper/scrape-prompt-vision.txt`. With those, 8-bit produces zero fabricated ingredients; without them the model copies neighboring item names into ingredients (a FODMAP-safety risk). Quantization matters: 4-bit fabricated ingredients and misread the restaurant name where 8-bit did not.
- **MLX vs CUDA quality drift**: the Mac runs `mlx-community/Qwen3-VL-8B-Instruct-8bit` (MLX 8-bit) and the 5080 runs `Qwen/Qwen3-VL-8B-Instruct-FP8` (FP8) — same dense Qwen3-VL-8B model, two near-lossless 8-bit quant schemes. Expect close but not bit-identical output; watch for drift via the prompt-regression test (see scraper-pipeline-plan.md Test Strategy).
- **Cold-start latency**: ~60–90 s on Mac (vllm-metal MLX load), ~60 s on Linux (vLLM startup + weight download on first run). Per-request vision latency is ~170 s/full-page menu on M2 at 8-bit. Keep the server warm during dev.
- **Production deployment**: out of scope for this doc. Production should run vLLM on a dedicated GPU host with a load balancer in front; that lives in the deploy pipeline, not the dev workflow.

## Related Plans

- [scraper-pipeline-plan.md](scraper-pipeline-plan.md) — the scraper pipeline this LLM serves
- [python-extractor-service-plan.md](python-extractor-service-plan.md) — **ARCHIVED.** Considered alternative.

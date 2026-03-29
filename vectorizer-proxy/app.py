import asyncio
import os
import struct
import time
from contextlib import asynccontextmanager
from typing import Any, Optional

import numpy as np
import torch
from fastapi import FastAPI, Request, Response, status
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer


model: SentenceTransformer

# Request batching: accumulate single /vectors requests and encode together.
_batch_queue: asyncio.Queue
_BATCH_MAX_SIZE = int(os.getenv("BATCH_MAX_SIZE", "64"))
_BATCH_TIMEOUT = float(os.getenv("BATCH_TIMEOUT", "0.01"))  # 10ms window


@asynccontextmanager
async def lifespan(_app: FastAPI):
    global model, _batch_queue
    model_name = os.getenv("MODEL_NAME", "sentence-transformers/multi-qa-MiniLM-L6-cos-v1")
    device = "cpu"
    if os.getenv("ENABLE_CUDA", "0") in ("1", "true") and torch.cuda.is_available():
        device = "cuda:0"
    elif torch.backends.mps.is_available() and torch.backends.mps.is_built():
        device = "mps"
    model = SentenceTransformer(model_name, device=device, local_files_only=False)
    if device.startswith("cuda"):
        model.half()
    model.eval()
    _batch_queue = asyncio.Queue()
    task = asyncio.create_task(_batch_worker())
    yield
    task.cancel()


async def _batch_worker():
    """Collect single-vectorization requests into batches for GPU efficiency."""
    loop = asyncio.get_event_loop()
    while True:
        # Wait for at least one request.
        text, future = await _batch_queue.get()
        batch_texts = [text]
        batch_futures = [future]

        # Collect more requests within the timeout window.
        deadline = time.monotonic() + _BATCH_TIMEOUT
        while len(batch_texts) < _BATCH_MAX_SIZE:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            try:
                text, future = await asyncio.wait_for(
                    _batch_queue.get(), timeout=remaining,
                )
                batch_texts.append(text)
                batch_futures.append(future)
            except asyncio.TimeoutError:
                break

        # Encode the whole batch on GPU in one call.
        try:
            vectors = await loop.run_in_executor(
                None,
                lambda: model.encode(
                    batch_texts,
                    batch_size=len(batch_texts),
                    normalize_embeddings=True,
                    convert_to_numpy=True,
                    show_progress_bar=False,
                ),
            )
            for i, fut in enumerate(batch_futures):
                if not fut.cancelled():
                    fut.set_result(vectors[i].tolist())
        except Exception as e:
            for fut in batch_futures:
                if not fut.cancelled():
                    fut.set_exception(e)


app = FastAPI(lifespan=lifespan)


class BatchRequest(BaseModel):
    texts: list[str]
    normalize: Optional[bool] = True


@app.post("/vectors/batch")
def vectorize_batch(req: BatchRequest, response: Response):
    try:
        vectors = model.encode(
            req.texts,
            batch_size=len(req.texts),
            normalize_embeddings=req.normalize,
            convert_to_numpy=True,
            show_progress_bar=False,
        )
        rows, dim = vectors.shape
        header = struct.pack("<II", rows, dim)
        body = header + vectors.astype(np.float32).tobytes()
        return Response(content=body, media_type="application/octet-stream")
    except Exception as e:
        response.status_code = status.HTTP_500_INTERNAL_SERVER_ERROR
        return {"error": str(e)}


@app.post("/vectors")
@app.post("/vectors/")
async def vectorize_single(request: Request, response: Response):
    try:
        body: dict[str, Any] = await request.json()
        text = body.get("text", "")
        loop = asyncio.get_event_loop()
        future = loop.create_future()
        await _batch_queue.put((text, future))
        v = await future
        return {"text": text, "vector": v, "dim": len(v), "error": ""}
    except Exception as e:
        response.status_code = status.HTTP_500_INTERNAL_SERVER_ERROR
        return {"error": str(e)}


@app.get("/.well-known/ready", response_class=Response)
@app.get("/.well-known/live", response_class=Response)
def health(response: Response):
    response.status_code = status.HTTP_204_NO_CONTENT

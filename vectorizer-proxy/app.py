import os
import struct
from contextlib import asynccontextmanager
from typing import Optional

import numpy as np
import torch
from fastapi import FastAPI, Response, status
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer


model: SentenceTransformer


@asynccontextmanager
async def lifespan(_app: FastAPI):
    global model
    model_name = os.getenv("MODEL_NAME", "sentence-transformers/multi-qa-MiniLM-L6-cos-v1")
    cuda = os.getenv("ENABLE_CUDA", "0") in ("1", "true")
    device = "cuda:0" if cuda and torch.cuda.is_available() else "cpu"
    model = SentenceTransformer(model_name, device=device, local_files_only=True)
    model.eval()
    yield


app = FastAPI(lifespan=lifespan)


class BatchRequest(BaseModel):
    texts: list[str]
    normalize: Optional[bool] = True


class SingleRequest(BaseModel):
    text: str


@app.post("/vectors/batch")
def vectorize_batch(req: BatchRequest, response: Response):
    try:
        vectors = model.encode(
            req.texts,
            batch_size=min(len(req.texts), 256),
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
def vectorize_single(req: SingleRequest, response: Response):
    try:
        vector = model.encode(
            [req.text],
            normalize_embeddings=True,
            convert_to_numpy=True,
            show_progress_bar=False,
        )
        return {"text": req.text, "vector": vector[0].tolist(), "dim": len(vector[0])}
    except Exception as e:
        response.status_code = status.HTTP_500_INTERNAL_SERVER_ERROR
        return {"error": str(e)}


@app.get("/.well-known/ready", response_class=Response)
@app.get("/.well-known/live", response_class=Response)
def health(response: Response):
    response.status_code = status.HTTP_204_NO_CONTENT

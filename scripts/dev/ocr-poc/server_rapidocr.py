"""RapidOCR variant of the OCR PoC server (see server.py for the API).

Same PP-OCR model family as the PaddleOCR server, run on ONNXRuntime via
the rapidocr v3 package (bundles PP-OCRv4 mobile det/rec) — no Paddle
framework dependency. Benchmarks the CPU execution path (onnxruntime CPU
measured ~10x faster than tesseract per band state) and, with
RAPIDOCR_USE_CUDA=1 plus onnxruntime-gpu installed, the CUDA EP.

Note: CoreML (Apple Silicon GPU) was measured and ruled out — the PP-OCR
models use dynamic input shapes that CoreML cannot compile; partial
fallback makes it ~1.75x SLOWER than pure CPU.

Run locally without Docker (CPU):

    python -m venv .venv && .venv/bin/pip install rapidocr fastapi uvicorn
    .venv/bin/uvicorn server_rapidocr:app --host 0.0.0.0 --port 8869

On a CUDA host use Dockerfile.rapidocr.
"""

import os
import time

import cv2
import numpy as np
from fastapi import FastAPI, Request, Response
from rapidocr import RapidOCR

app = FastAPI()

USE_CUDA = os.environ.get("RAPIDOCR_USE_CUDA", "") == "1"
if USE_CUDA:
    import onnxruntime

    available = onnxruntime.get_available_providers()
    if "CUDAExecutionProvider" not in available:
        raise RuntimeError(
            f"RAPIDOCR_USE_CUDA=1 but CUDAExecutionProvider is unavailable "
            f"(providers: {available}) — benchmark numbers would silently be CPU"
        )
DEVICE = "onnxruntime-cuda" if USE_CUDA else "onnxruntime-cpu"

# Configuration pitfalls (cost us a debugging session — do not regress):
#   - Global.width_height_ratio=-1: by default RapidOCR SKIPS text detection
#     on images wider than 8:1 and treats them as a single text line. Our
#     full-width screen bands are ~13:1 — without this the engine returns
#     nothing (very fast garbage).
#   - Det.limit_type=max + a large side len: a 'min' limit UPSCALES short
#     bands by ~9x before detection (1.3s/state).
#   - Global.use_cls=False: screen captures are upright, angle
#     classification is waste.
ENGINE = RapidOCR(
    params={
        "Global.use_cls": False,
        "Global.width_height_ratio": -1,
        "Det.limit_type": "max",
        "Det.limit_side_len": 2048,
        "EngineConfig.onnxruntime.use_cuda": USE_CUDA,
    }
)


@app.get("/healthz")
def healthz():
    return {"status": "ok", "device": DEVICE}


@app.post("/ocr")
async def ocr(request: Request):
    body = await request.body()
    img = cv2.imdecode(np.frombuffer(body, dtype=np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        return Response(status_code=400, content="undecodable image")

    start = time.perf_counter()
    result = ENGINE(img)
    duration_ms = (time.perf_counter() - start) * 1000.0

    words = []
    if result is not None and result.boxes is not None:
        for box, text, conf in zip(result.boxes, result.txts, result.scores):
            xs = [p[0] for p in box]
            ys = [p[1] for p in box]
            x, y = int(min(xs)), int(min(ys))
            words.append(
                {
                    "text": text,
                    "conf": float(conf),
                    "x": x,
                    "y": y,
                    "w": int(max(xs)) - x,
                    "h": int(max(ys)) - y,
                }
            )
    return {"duration_ms": duration_ms, "words": words}


# Warm up the models so the first benchmark request is not an outlier.
_warm = np.full((64, 320, 3), 255, dtype=np.uint8)
cv2.putText(_warm, "warmup 123", (4, 40), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
ENGINE(_warm)

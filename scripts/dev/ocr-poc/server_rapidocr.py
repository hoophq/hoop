"""RapidOCR variant of the OCR PoC server (see server.py for the API).

Same PP-OCR models as the PaddleOCR server, converted to ONNX and run on
ONNXRuntime — no Paddle framework dependency. Useful to benchmark the
CPU execution path (onnxruntime CPU is typically faster than Paddle CPU)
and alternative providers (CUDA, TensorRT, CoreML) with identical models.

Run locally without Docker:

    python -m venv .venv && .venv/bin/pip install rapidocr-onnxruntime fastapi uvicorn
    .venv/bin/uvicorn server_rapidocr:app --host 0.0.0.0 --port 8869
"""

import time

import cv2
import numpy as np
from fastapi import FastAPI, Request, Response
from rapidocr_onnxruntime import RapidOCR

app = FastAPI()

# Configuration pitfalls (cost us a debugging session — do not regress):
#   - width_height_ratio=-1: by default RapidOCR SKIPS text detection on
#     images wider than 8:1 and treats them as a single text line. Our
#     full-width screen bands are ~13:1 — without this the engine returns
#     nothing (very fast garbage).
#   - det_limit_type=max + a large side len: the default (min/736) UPSCALES
#     a short band by ~9x before detection (1.3s/state).
#   - det_model_path=None: the kwargs updater requires the key to be
#     present to fall back to the bundled model.
#   - use_cls=False per call: screen captures are upright, angle
#     classification is waste.
ENGINE = RapidOCR(
    width_height_ratio=-1,
    det_model_path=None,
    det_limit_type="max",
    det_limit_side_len=2048,
)
DEVICE = "onnxruntime-cpu"


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
    result, _ = ENGINE(img, use_cls=False)
    duration_ms = (time.perf_counter() - start) * 1000.0

    words = []
    for box, text, conf in result or []:
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
ENGINE(_warm, use_cls=False)

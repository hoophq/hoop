"""RapidOCR variant of the OCR PoC server (see server.py for the API).

Same PP-OCR model family as the PaddleOCR server, run on ONNXRuntime via
the rapidocr v3 package (bundles PP-OCRv4 mobile det/rec) — no Paddle
framework dependency. Ships as two image flavors from this one source:

  - Dockerfile.rapidocr      (CPU: slim python base, onnxruntime)
  - Dockerfile.rapidocr-gpu  (CUDA base, onnxruntime-gpu)

The device choice is determined at runtime — by detection, not an env
toggle — and then passed into RapidOCR's ONNXRuntime configuration when
constructing the engine: CUDA is enabled when the onnxruntime build has
the CUDA provider AND a GPU is actually visible (libcuda is only injected
into the container when one is passed). A GPU-flavored image with no
visible GPU refuses to start — silently falling back to CPU would burn
CPU and quietly miss latency targets; set OCR_ALLOW_CPU_FALLBACK=1 to
permit it (loudly logged). /healthz always reports the device actually
in use.

Note: CoreML (Apple Silicon GPU) was measured and ruled out — the PP-OCR
models use dynamic input shapes that CoreML cannot compile; partial
fallback makes it ~1.75x SLOWER than pure CPU.

Run locally without Docker (CPU):

    python -m venv .venv && .venv/bin/pip install rapidocr fastapi uvicorn
    .venv/bin/uvicorn server_rapidocr:app --host 0.0.0.0 --port 8869
"""

import logging
import os
import time

import cv2
import numpy as np
import onnxruntime
from fastapi import FastAPI, Request, Response
from rapidocr import RapidOCR

from device_policy import cuda_device_count, resolve_device

app = FastAPI()
logger = logging.getLogger("uvicorn.error")

DEVICE = resolve_device(
    cuda_compiled="CUDAExecutionProvider" in onnxruntime.get_available_providers(),
    cuda_devices=cuda_device_count(),
    allow_cpu_fallback=os.environ.get("OCR_ALLOW_CPU_FALLBACK", "") == "1",
)
USE_CUDA = DEVICE == "onnxruntime-cuda"

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


def warmup() -> None:
    """Warm up the models so the first benchmark request is not an outlier.
    Logged explicitly: provider initialization failures (especially CUDA EP)
    surface here, and the operator should see the device they failed on."""
    warm = np.full((64, 320, 3), 255, dtype=np.uint8)
    cv2.putText(warm, "warmup 123", (4, 40), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
    try:
        ENGINE(warm)
    except Exception:
        logger.exception("RapidOCR warmup failed on %s", DEVICE)
        raise
    logger.info("RapidOCR warmup completed on %s", DEVICE)


warmup()

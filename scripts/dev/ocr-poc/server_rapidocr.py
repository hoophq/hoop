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

import asyncio
import base64
import hashlib
import logging
import os
import time

import cv2
import numpy as np
import onnxruntime
from fastapi import FastAPI, Request, Response
from rapidocr import RapidOCR
from rapidocr.ch_ppocr_rec.typings import TextRecInput
from rapidocr.utils.process_img import get_rotate_crop_image

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


def _run_ocr(img) -> dict:
    """Synchronous inference + box extraction. Runs in a thread executor so it
    never blocks the event loop (RapidOCR/ONNXRuntime is blocking CPU work);
    otherwise a single in-flight inference stalls the whole worker and the
    agent's concurrent chunk requests queue until they time out."""
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


@app.post("/ocr")
async def ocr(request: Request):
    body = await request.body()
    img = cv2.imdecode(np.frombuffer(body, dtype=np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        return Response(status_code=400, content="undecodable image")

    loop = asyncio.get_running_loop()
    return await loop.run_in_executor(None, _run_ocr, img)


def _run_det(img) -> dict:
    """Detection only: return the text-line quad boxes for an image, each with a
    content hash of the EXACT pixels recognition will see. The agent uses these
    to decide which lines changed (per-line OCR cache) and re-runs recognition
    only on the changed ones via /rec.

    Boxes are returned as 4-point quads in full-image pixel coordinates, exactly
    as RapidOCR's detector produces them, so /rec can reproduce the identical
    crop with get_rotate_crop_image (no fidelity loss versus the combined /ocr
    path).

    crop_hash is the SHA-256 of the recognition crop produced by
    get_rotate_crop_image for this box — i.e. the very pixels /rec will feed to
    the recognizer. The agent keys its per-line cache on this hash, so a line is
    reused ONLY when the exact recognition input recurs. Hashing the perspective
    crop (not an axis-aligned bbox) closes the gap where a changed quad could
    share a bbox: if recognition would see different pixels, the hash differs
    and the line is re-recognized. SHA-256 makes a stale-reuse collision
    cryptographically infeasible, upholding the guard's invariant."""
    start = time.perf_counter()
    result = ENGINE.text_det(img)
    duration_ms = (time.perf_counter() - start) * 1000.0
    boxes = []
    if getattr(result, "boxes", None) is not None:
        for box in result.boxes:
            arr = np.array(box, dtype=np.float32)
            crop = get_rotate_crop_image(img, arr)
            crop_hash = hashlib.sha256(np.ascontiguousarray(crop).tobytes()).hexdigest()
            boxes.append(
                {
                    "quad": [[float(p[0]), float(p[1])] for p in box],
                    "crop_hash": crop_hash,
                }
            )
    return {"duration_ms": duration_ms, "boxes": boxes}


def _run_rec(img, boxes) -> dict:
    """Recognition only: crop each provided quad box from the image (using the
    same perspective crop as the combined pipeline) and recognize it. Returns
    one word per box, in full-image coordinates, in the SAME order as `boxes`.

    The agent calls this only for the boxes whose pixels changed since the last
    frame; unchanged lines are served from its cache. Cropping happens here (not
    on the agent) so the recognized text is byte-for-byte what /ocr would have
    produced for the same box."""
    start = time.perf_counter()
    words = []
    if boxes:
        crops = []
        bounds = []
        for box in boxes:
            arr = np.array(box, dtype=np.float32)
            crops.append(get_rotate_crop_image(img, arr))
            xs = [p[0] for p in box]
            ys = [p[1] for p in box]
            bounds.append((int(min(xs)), int(min(ys)), int(max(xs)), int(max(ys))))
        rec_out = ENGINE.text_rec(TextRecInput(img=crops))
        txts = rec_out.txts or []
        scores = rec_out.scores or []
        for i, (x0, y0, x1, y1) in enumerate(bounds):
            text = txts[i] if i < len(txts) else ""
            conf = float(scores[i]) if i < len(scores) else 0.0
            words.append(
                {
                    "text": text,
                    "conf": conf,
                    "x": x0,
                    "y": y0,
                    "w": x1 - x0,
                    "h": y1 - y0,
                }
            )
    duration_ms = (time.perf_counter() - start) * 1000.0
    return {"duration_ms": duration_ms, "words": words}


@app.post("/det")
async def det(request: Request):
    body = await request.body()
    img = cv2.imdecode(np.frombuffer(body, dtype=np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        return Response(status_code=400, content="undecodable image")

    loop = asyncio.get_running_loop()
    return await loop.run_in_executor(None, _run_det, img)


@app.post("/rec")
async def rec(request: Request):
    # JSON: { "image": "<base64 of the same image det was run on>",
    #         "boxes": [ [[x,y],[x,y],[x,y],[x,y]], ... ] }
    payload = await request.json()
    image_b64 = payload.get("image")
    boxes = payload.get("boxes") or []
    if not image_b64:
        return Response(status_code=400, content="missing image")
    try:
        raw = base64.b64decode(image_b64)
    except Exception:
        return Response(status_code=400, content="invalid base64 image")
    img = cv2.imdecode(np.frombuffer(raw, dtype=np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        return Response(status_code=400, content="undecodable image")

    loop = asyncio.get_running_loop()
    return await loop.run_in_executor(None, _run_rec, img, boxes)


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

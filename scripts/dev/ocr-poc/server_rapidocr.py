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


# Detection-only downscale. RapidOCR's detector normalizes the WHOLE input in
# NumPy on the CPU before inference; on a full 1920x1080 frame that single
# normalize (~100ms) costs MORE than the GPU inference, and the detector does
# not actually honor limit_side_len for our wide frames. We therefore downscale
# the image ONLY for the detection pass (finding where text lines are tolerates
# a smaller image), then crop and RECOGNIZE from the FULL-RESOLUTION original —
# so recognition accuracy on small fonts is unaffected. Measured on a T4:
# det@0.67 stays accurate down to ~8px glyphs while cutting OCR ~1.6x; det@0.5
# is accurate to ~10px and ~2.4x. 0.67 is the conservative default (protects
# the smallest realistic on-screen text).
#
# A hard floor (DET_MIN_SIDE) prevents downscaling already-small images, where
# the normalize is already cheap and shrinking further would only hurt det
# recall for no latency benefit.
DET_DOWNSCALE = float(os.environ.get("OCR_DET_DOWNSCALE", "0.67"))
DET_MIN_SIDE = 640


def _det_boxes_downscaled(img):
    """Runs text detection on a downscaled copy of `img` (cheaper CPU normalize
    + inference), returning line-quad boxes in FULL-RESOLUTION coordinates.
    Falls back to full-res detection when the image is small or downscaling is
    disabled, so tiny frames keep full recall."""
    h, w = img.shape[:2]
    scale = DET_DOWNSCALE
    if scale >= 1.0 or min(h, w) * scale < DET_MIN_SIDE:
        det = ENGINE.text_det(img)
        return det.boxes
    small = cv2.resize(
        img, (max(1, int(w * scale)), max(1, int(h * scale))), interpolation=cv2.INTER_AREA
    )
    det = ENGINE.text_det(small)
    if det.boxes is None:
        return None
    inv = 1.0 / scale
    # Scale each quad's points back to full-resolution coordinates.
    return [np.asarray(box, dtype=np.float32) * inv for box in det.boxes]


@app.get("/healthz")
def healthz():
    return {"status": "ok", "device": DEVICE}


def _run_ocr(img) -> dict:
    """Synchronous inference + box extraction. Runs in a thread executor so it
    never blocks the event loop (RapidOCR/ONNXRuntime is blocking CPU work);
    otherwise a single in-flight inference stalls the whole worker and the
    agent's concurrent chunk requests queue until they time out.

    Detection runs on a downscaled copy (cheap), recognition on full-resolution
    crops (accurate) — see `_det_boxes_downscaled`. The recognized text is
    therefore identical to a full-resolution pipeline; only the detector sees a
    smaller image. Word boxes are returned in full-resolution coordinates."""
    start = time.perf_counter()

    boxes = _det_boxes_downscaled(img)
    words = []
    if boxes is not None and len(boxes) > 0:
        # Recognize each detected line from the FULL-RES original (perspective
        # crop, exactly as the combined pipeline would), so small fonts are read
        # at full fidelity.
        crops = [get_rotate_crop_image(img, np.asarray(b, dtype=np.float32)) for b in boxes]
        rec = ENGINE.text_rec(TextRecInput(img=crops))
        txts = rec.txts or []
        scores = rec.scores or []
        for i, box in enumerate(boxes):
            text = txts[i] if i < len(txts) else ""
            if not text:
                continue
            xs = [p[0] for p in box]
            ys = [p[1] for p in box]
            x, y = int(min(xs)), int(min(ys))
            words.append(
                {
                    "text": text,
                    "conf": float(scores[i]) if i < len(scores) else 0.0,
                    "x": x,
                    "y": y,
                    "w": int(max(xs)) - x,
                    "h": int(max(ys)) - y,
                }
            )

    duration_ms = (time.perf_counter() - start) * 1000.0
    return {"duration_ms": duration_ms, "words": words}


@app.post("/ocr")
async def ocr(request: Request):
    body = await request.body()
    img = cv2.imdecode(np.frombuffer(body, dtype=np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        return Response(status_code=400, content="undecodable image")

    loop = asyncio.get_running_loop()
    return await loop.run_in_executor(None, _run_ocr, img)


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

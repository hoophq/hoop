"""GPU OCR PoC server for the RDP PII guard benchmark.

Wraps PaddleOCR (PP-OCR) behind a minimal HTTP API shaped like the
gateway's tesseract TSV path, so rdpbench can swap engines with a URL:

    POST /ocr   body: image bytes (BMP/PNG)  ->  JSON:
      {
        "duration_ms": float,        # server-side OCR time
        "words": [ {"text": str, "conf": float,
                    "x": int, "y": int, "w": int, "h": int} ]
      }
    GET /healthz -> {"status": "ok", "device": "..."}

This is a benchmark prototype, NOT the production sidecar: no auth, no
TLS, single in-process model. Run it on a trusted network only.
"""

import time

import cv2
import numpy as np
import paddle
from fastapi import FastAPI, Request, Response
from paddleocr import PaddleOCR

app = FastAPI()

# Report the device actually usable at runtime, not just the build flavor:
# a GPU paddle build with no visible CUDA device silently runs on CPU, which
# would invalidate the benchmark numbers.
HAS_GPU = paddle.device.is_compiled_with_cuda() and paddle.device.cuda.device_count() > 0
DEVICE = "gpu" if HAS_GPU else "cpu"

# det+rec only: screen captures are upright, angle classification is waste.
ENGINE = PaddleOCR(use_angle_cls=False, lang="en", show_log=False, use_gpu=HAS_GPU)


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
    result = ENGINE.ocr(img, cls=False)
    duration_ms = (time.perf_counter() - start) * 1000.0

    words = []
    for page in result or []:
        for box, (text, conf) in page or []:
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


# Warm up the model so the first benchmark request is not an outlier.
_warm = np.full((64, 320, 3), 255, dtype=np.uint8)
cv2.putText(_warm, "warmup 123", (4, 40), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
ENGINE.ocr(_warm, cls=False)

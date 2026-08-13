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
from contextlib import asynccontextmanager, suppress
from concurrent.futures import ThreadPoolExecutor
import copy
import logging
import os
import pathlib
import time

import cv2
import numpy as np
import onnxruntime
import rapidocr
from fastapi import FastAPI, Request, Response
from rapidocr import RapidOCR
from rapidocr.ch_ppocr_rec.typings import TextRecInput
from rapidocr.inference_engine.onnxruntime.main import OrtInferSession
from rapidocr.utils.process_img import get_rotate_crop_image
from rapidocr.utils.typings import LangRec

from raw_image import (
    FRAME_SEQUENCE_HEADER,
    FRAME_SESSION_HEADER,
    HEIGHT_HEADER,
    WIDTH_HEADER,
    RawImageError,
    decode_frame_request,
    decode_rdp_frame_request,
    decode_rgba,
    parse_geometry,
    read_bounded_request_body,
    read_exact_request_body,
    rgba_body_length,
    validate_content_type,
    validate_frame_content_type,
    validate_rdp_frame_content_type,
)

from bucket_rec import (
    BUCKET_WIDTHS,
    MAX_BATCH,
    REC_H,
    BucketDet,
    BucketRec,
    filter_and_scale_boxes,
    pad_det_input,
)
from device_policy import cuda_device_count, resolve_device, resolve_worker_concurrency

@asynccontextmanager
async def _lifespan(_app):
    reaper = None
    if RESIDENT_STORE is not None:
        reaper = asyncio.create_task(
            _reap_resident_frames(), name="resident-frame-reaper"
        )
    try:
        yield
    finally:
        if reaper is not None:
            reaper.cancel()
            with suppress(asyncio.CancelledError):
                await reaper


app = FastAPI(lifespan=_lifespan)
logger = logging.getLogger("uvicorn.error")

DEVICE = resolve_device(
    cuda_compiled="CUDAExecutionProvider" in onnxruntime.get_available_providers(),
    cuda_devices=cuda_device_count(),
    allow_cpu_fallback=os.environ.get("OCR_ALLOW_CPU_FALLBACK", "") == "1",
)
USE_CUDA = DEVICE == "onnxruntime-cuda"
GPU_PREPROCESS = os.environ.get("OCR_GPU_PREPROCESS", "") == "1"
resolve_worker_concurrency(os.environ.get("WEB_CONCURRENCY"), GPU_PREPROCESS)

# Recognition language. The default ch model reads Chinese+English with a
# 6,625-class output head; the en model reads Latin/digits only with a ~96
# class head — meaningfully less compute and fewer confusable glyphs on
# Latin-only screens. Deployments serving CJK desktops MUST keep ch: the en
# dictionary turns CJK text into garbage tokens, which means CJK PII would
# never reach Presidio. This is a per-deployment product decision, so it is
# an env knob and never auto-detected.
REC_LANG = os.environ.get("OCR_REC_LANG", "ch")
if REC_LANG not in ("ch", "en"):
    raise RuntimeError(f"OCR_REC_LANG must be 'ch' or 'en', got {REC_LANG!r}")

# Recognition runtime. 'fp16' runs recognition on an fp16-converted model
# through fixed-shape bucket sessions (see BucketRec below) — measured 1.8x
# end-to-end on a T4 with byte-identical text output. It requires CUDA (fp16
# has no fast CPU kernels) and the converted model produced at image build
# time. 'fp32' is the stock RapidOCR recognition path, byte-identical to the
# previous releases of this server.
REC_PRECISION = os.environ.get("OCR_REC_PRECISION", "fp32")
if REC_PRECISION not in ("fp32", "fp16"):
    raise RuntimeError(f"OCR_REC_PRECISION must be 'fp32' or 'fp16', got {REC_PRECISION!r}")
if REC_PRECISION == "fp16" and not USE_CUDA:
    raise RuntimeError(
        "OCR_REC_PRECISION=fp16 requires the CUDA execution provider: fp16 "
        "models have no fast CPU kernels and would run slower than fp32. "
        "Unset OCR_REC_PRECISION or run on a GPU."
    )

# Configuration pitfalls (cost us a debugging session — do not regress):
#   - Global.width_height_ratio=-1: by default RapidOCR SKIPS text detection
#     on images wider than 8:1 and treats them as a single text line. Our
#     full-width screen bands are ~13:1 — without this the engine returns
#     nothing (very fast garbage).
#   - Det.limit_type=max + a large side len: a 'min' limit UPSCALES short
#     bands by ~9x before detection (1.3s/state).
#   - Global.use_cls=False: screen captures are upright, angle
#     classification is waste.
#   - cudnn_conv_algo_search stays at RapidOCR's EXHAUSTIVE default:
#     HEURISTIC crashes at runtime on Turing (CUDNN_FE failure 11 under
#     concurrent load). The retune cost EXHAUSTIVE incurs on varying input
#     shapes is instead eliminated structurally by BucketRec's fixed shapes.
# ONNX Runtime thread bounds. Left at ORT's default, each InferenceSession sizes
# its intra-op thread pool to the host's logical core count. With WEB_CONCURRENCY
# uvicorn workers, each holding the RapidOCR engine plus many fixed-shape bucket
# sessions (one per width bucket), that default multiplies: on a high-core host
# (e.g. a 192-vCPU GPU node) it becomes tens of thousands of threads and exhausts
# the process/cgroup thread limit at session init (pthread_create EAGAIN),
# crashing startup. The GPU does the heavy compute and serving throughput comes
# from concurrent workers, not from intra-op parallelism within one inference, so
# a small bounded pool is both sufficient and necessary. Applied to EVERY session
# the server creates (the engine below, the bucket-rec sessions, and the per-
# bucket det sessions). Overridable per deployment.
def _env_threads(name: str, default: int) -> int:
    try:
        v = int(os.environ.get(name, str(default)))
    except ValueError as exc:
        raise RuntimeError(f"{name} must be an integer >= 1") from exc
    if v < 1:
        raise RuntimeError(f"{name} must be >= 1, got {v}")
    return v


OCR_INTRA_OP_THREADS = _env_threads("OCR_INTRA_OP_THREADS", 4)
OCR_INTER_OP_THREADS = _env_threads("OCR_INTER_OP_THREADS", 1)

# CUDA arena growth strategy for every session's GPU memory pool. The default
# 'kNextPowerOfTwo' rounds each arena up to a power of two, which is fast but
# over-allocates: with many bucket sessions per worker it inflates per-worker
# VRAM (~4.8GB on an 80GB H100), capping how many uvicorn workers — and thus how
# many concurrent inferences — fit on the card, leaving the GPU underfed.
# 'kSameAsRequested' grows the arena by exactly what each allocation needs:
# smaller per-worker footprint (more workers fit) at the cost of more frequent
# allocations. Tunable per deployment / GPU memory budget.
OCR_CUDA_ARENA_STRATEGY = os.environ.get("OCR_CUDA_ARENA_STRATEGY", "kNextPowerOfTwo")
if OCR_CUDA_ARENA_STRATEGY not in ("kNextPowerOfTwo", "kSameAsRequested"):
    raise RuntimeError(
        "OCR_CUDA_ARENA_STRATEGY must be 'kNextPowerOfTwo' or 'kSameAsRequested', "
        f"got {OCR_CUDA_ARENA_STRATEGY!r}"
    )


def _bounded_session_options() -> onnxruntime.SessionOptions:
    """SessionOptions with the intra/inter-op thread pools bounded (see above)
    and rapidocr's usual quiet logging."""
    opts = onnxruntime.SessionOptions()
    opts.log_severity_level = 4
    opts.intra_op_num_threads = OCR_INTRA_OP_THREADS
    opts.inter_op_num_threads = OCR_INTER_OP_THREADS
    return opts


_ENGINE_PARAMS = {
    "Global.use_cls": False,
    "Global.width_height_ratio": -1,
    "Det.limit_type": "max",
    "Det.limit_side_len": 2048,
    "EngineConfig.onnxruntime.use_cuda": USE_CUDA,
    # Bound the engine's own det/rec/cls session thread pools (see above).
    "EngineConfig.onnxruntime.intra_op_num_threads": OCR_INTRA_OP_THREADS,
    "EngineConfig.onnxruntime.inter_op_num_threads": OCR_INTER_OP_THREADS,
    # ...and give those sessions the same arena strategy as the ones built
    # below, so the knob covers every session rather than only the bucket
    # ones. The engine's det/rec sessions hold VRAM even in fp16 mode (where
    # BucketDet/BucketRec serve the actual inferences), so they count toward
    # the per-worker footprint the strategy is meant to control.
    "EngineConfig.onnxruntime.cuda_ep_cfg.arena_extend_strategy": OCR_CUDA_ARENA_STRATEGY,
}
if REC_LANG == "en":
    _ENGINE_PARAMS["Rec.lang_type"] = LangRec.EN
ENGINE = RapidOCR(params=_ENGINE_PARAMS)


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
try:
    DET_DOWNSCALE = float(os.environ.get("OCR_DET_DOWNSCALE", "0.67"))
except ValueError as exc:
    raise RuntimeError("OCR_DET_DOWNSCALE must be a float in (0, 1]") from exc
if not 0.0 < DET_DOWNSCALE <= 1.0:
    raise RuntimeError(f"OCR_DET_DOWNSCALE must be in (0, 1], got {DET_DOWNSCALE!r}")
DET_MIN_SIDE = 640


# Detection input shape buckets (fp16 runtime only). The detection session
# has the same cuDNN-FE single-shape conv cache problem BucketRec solves for
# recognition: production dirty bands arrive at ever-varying heights (and are
# usually below DET_MIN_SIDE, so they skip the downscale entirely), and every
# new (h, w) rebuilds + EXHAUSTIVE-retunes the det conv graphs — measured
# ~500ms per shape change on an A100 vs ~30ms steady-state. Padding the det
# input to bucketed dimensions (BORDER_REPLICATE) caps the distinct shapes
# the session ever sees; boxes falling entirely inside the padding are
# phantoms and dropped, boxes crossing into it are clipped to the real area.
#
# Gated to the fp16 runtime: replicated border content can shift det box
# edges marginally near image boundaries, so the stock fp32 path stays
# byte-identical to previous releases.
def _make_det_detector_factory(base_det, model_path: str):
    """Builds the per-bucket detector factory for BucketDet.

    Each detector is a shallow copy of the stock TextDetector with its own
    dedicated fp32 ORT session (det stays fp32 — fp16 det measurably changes
    box geometry, which is the product's redaction rectangles). The copy
    shares the stock pre/postprocess ops; TextDetector assigns
    self.preprocess_op per call, which is safe here because each copy only
    ever sees one input shape (see BucketDet's docstring for the analysis).
    The raw session is injected through OrtInferSession's 'session' cfg key
    (rapidocr PR #451). device_id 0 matches rapidocr's own cuda_ep_cfg
    default; the deployment contract passes exactly one GPU per container.
    """
    if not os.path.exists(model_path):
        raise RuntimeError(f"det model not found at {model_path}")

    def factory():
        cuda_opts = {
            "device_id": 0,
            "arena_extend_strategy": OCR_CUDA_ARENA_STRATEGY,
            "cudnn_conv_algo_search": "EXHAUSTIVE",
            "do_copy_in_default_stream": True,
        }
        sess_opts = _bounded_session_options()
        raw = onnxruntime.InferenceSession(
            model_path,
            sess_options=sess_opts,
            providers=[("CUDAExecutionProvider", cuda_opts), "CPUExecutionProvider"],
        )
        det = copy.copy(base_det)
        det.session = OrtInferSession({"session": raw})
        return det

    return factory


def _det_boxes_downscaled(img):
    """Runs text detection on a downscaled copy of `img` (cheaper CPU normalize
    + inference), returning line-quad boxes in FULL-RESOLUTION coordinates.
    Falls back to full-res detection when the image is small or downscaling is
    disabled, so tiny frames keep full recall. In the fp16 runtime the det
    input is additionally padded to bucketed dimensions (see above)."""
    h, w = img.shape[:2]
    scale = DET_DOWNSCALE
    if scale >= 1.0 or min(h, w) * scale < DET_MIN_SIDE:
        det_input, scale = img, 1.0
    else:
        det_input = cv2.resize(
            img,
            (max(1, int(w * scale)), max(1, int(h * scale))),
            interpolation=cv2.INTER_AREA,
        )

    if BUCKET_DET is not None:
        det_input, real_h, real_w = pad_det_input(det_input)
        det = BUCKET_DET(det_input)
    else:
        real_h, real_w = det_input.shape[:2]
        det = ENGINE.text_det(det_input)
    if det.boxes is None:
        return None
    return filter_and_scale_boxes(det.boxes, real_h, real_w, 1.0 / scale)


# --- fp16 fixed-shape recognition runtime ----------------------------------
#
# See bucket_rec.py for the full rationale (cuDNN-FE single-shape conv cache,
# why HEURISTIC is not an option, padding semantics matching the stock path).
#
# VRAM: the rec model is ~5MB in fp16; len(BUCKET_WIDTHS) sessions plus
# per-shape arenas measure ~600MB per process on a T4. Resident preprocessing
# enforces one process; nonresident deployments must budget each worker copy.
def _build_bucket_rec(model_path: str) -> BucketRec:
    if not os.path.exists(model_path):
        raise RuntimeError(
            f"fp16 recognition model not found at {model_path}; it is "
            "produced at image build time (see Dockerfile.agent-ocr-gpu). "
            "Unset OCR_REC_PRECISION to use the stock fp32 path."
        )
    cuda_opts = {
        "device_id": 0,
        "arena_extend_strategy": OCR_CUDA_ARENA_STRATEGY,
        "cudnn_conv_algo_search": "EXHAUSTIVE",
        "do_copy_in_default_stream": True,
    }
    sess_opts = _bounded_session_options()
    sessions = {
        w: onnxruntime.InferenceSession(
            model_path,
            sess_options=sess_opts,
            providers=[("CUDAExecutionProvider", cuda_opts), "CPUExecutionProvider"],
        )
        for w in BUCKET_WIDTHS
    }
    return BucketRec(
        sessions=sessions,
        input_name=sessions[BUCKET_WIDTHS[0]].get_inputs()[0].name,
        # Reuse the engine's CTC decoder so text/conf decoding (including the
        # language dictionary) is identical to the stock path.
        decode=ENGINE.text_rec.postprocess_op,
    )


_FP16_MODEL_PATH = os.environ.get(
    "OCR_REC_FP16_MODEL", f"/opt/ocr/models/{REC_LANG}_rec_fp16.onnx"
)
BUCKET_REC = _build_bucket_rec(_FP16_MODEL_PATH) if REC_PRECISION == "fp16" else None

# Per-bucket detection sessions (see BucketDet). Detection always runs the
# fp32 det model — fp16 det measurably changes box geometry (merging), which
# is the product's redaction rectangles; only the SESSION layout changes.
_DET_MODEL_PATH = os.environ.get(
    "OCR_DET_MODEL",
    str(pathlib.Path(str(rapidocr.__file__)).parent / "models" / "ch_PP-OCRv4_det_mobile.onnx"),
)
BUCKET_DET = (
    BucketDet(ENGINE.text_det, _make_det_detector_factory(ENGINE.text_det, _DET_MODEL_PATH))
    if REC_PRECISION == "fp16"
    else None
)

if GPU_PREPROCESS:
    if not USE_CUDA or REC_PRECISION != "fp16":
        raise RuntimeError(
            "OCR_GPU_PREPROCESS=1 requires CUDA and OCR_REC_PRECISION=fp16"
        )
    from gpu_pipeline import (
        GpuOcrPipeline,
        GpuResidentStore,
        ResidentCapacityError,
        ResidentSequenceError,
        UnsupportedGpuShape,
    )

    GPU_PIPELINE = GpuOcrPipeline(
        ENGINE.text_det,
        BUCKET_DET,
        BUCKET_REC,
        DET_DOWNSCALE,
        DET_MIN_SIDE,
    )
else:
    GPU_PIPELINE = None


def _positive_int_env(name, default):
    try:
        value = int(os.environ.get(name, str(default)), 10)
    except ValueError as exc:
        raise RuntimeError(f"{name} must be a positive integer") from exc
    if value < 1:
        raise RuntimeError(f"{name} must be a positive integer")
    return value


def _positive_float_env(name, default):
    try:
        value = float(os.environ.get(name, str(default)))
    except ValueError as exc:
        raise RuntimeError(f"{name} must be positive") from exc
    if value <= 0:
        raise RuntimeError(f"{name} must be positive")
    return value


if GPU_PIPELINE is not None:
    RESIDENT_STORE = GpuResidentStore(
        GPU_PIPELINE,
        max_sessions=_positive_int_env("OCR_RESIDENT_MAX_SESSIONS", 32),
        max_bytes=_positive_int_env("OCR_RESIDENT_VRAM_MB", 2048) << 20,
        ttl_seconds=_positive_float_env("OCR_RESIDENT_TTL_SECONDS", 300),
        max_cache_entries=_positive_int_env("OCR_RESIDENT_CACHE_ENTRIES", 128),
        max_recognition_cache_entries=_positive_int_env(
            "OCR_RESIDENT_RECOGNITION_CACHE_ENTRIES", 512
        ),
    )
else:
    RESIDENT_STORE = None

# Resident framebuffers are process-local, so GPU preprocessing enforces one
# Uvicorn worker. This makes POST/DELETE ownership deterministic and prevents a
# non-owner from falsely acknowledging cleanup while another process retains
# pixels in VRAM. Serialize requests inside that worker as well so bursts
# cannot multiply pinned host/device allocations in its executor.
RAW_OCR_EXECUTOR = ThreadPoolExecutor(max_workers=1, thread_name_prefix="raw-ocr")
RAW_OCR_ADMISSION = asyncio.Semaphore(1)

async def _reap_resident_frames():
    interval = min(30.0, max(0.1, RESIDENT_STORE.ttl_seconds / 2))
    while True:
        await asyncio.sleep(interval)
        try:
            async with RAW_OCR_ADMISSION:
                loop = asyncio.get_running_loop()
                await loop.run_in_executor(
                    RAW_OCR_EXECUTOR, RESIDENT_STORE.evict_expired
                )
        except asyncio.CancelledError:
            raise
        except Exception:
            logger.exception("resident framebuffer expiry failed")


@app.get("/healthz")
def healthz():
    return {
        "status": "ok",
        "device": DEVICE,
        "rec_lang": REC_LANG,
        "rec_precision": REC_PRECISION,
        "raw_rgba": True,
        "gpu_preprocess": GPU_PIPELINE is not None,
        "resident_rgba": RESIDENT_STORE is not None,
        "resident_cache_max_entries": (
            RESIDENT_STORE.max_cache_entries if RESIDENT_STORE is not None else 0
        ),
        "resident_recognition_cache_max_entries": (
            RESIDENT_STORE.max_recognition_cache_entries
            if RESIDENT_STORE is not None
            else 0
        ),
    }


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
        #
        # Degenerate (zero-area) crops are filtered out BEFORE recognition so
        # both runtimes expose the same contract: the DB postprocess enforces
        # a minimum box size so these should not occur, but a malformed quad
        # must not crash one path (resize of an empty image) while the other
        # silently skips it. Filtering boxes and crops together keeps the
        # box/text index alignment below.
        pairs = []
        for b in boxes:
            crop = get_rotate_crop_image(img, np.asarray(b, dtype=np.float32))
            if crop.shape[0] > 0 and crop.shape[1] > 0:
                pairs.append((b, crop))
        boxes = [b for b, _ in pairs]
        crops = [c for _, c in pairs]
        if BUCKET_REC is not None:
            txts, scores = BUCKET_REC(crops)
        else:
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


async def _run_in_executor(img, request_started: float, decode_started: float):
    decode_ms = (time.perf_counter() - decode_started) * 1000.0
    loop = asyncio.get_running_loop()
    result = await loop.run_in_executor(None, _run_ocr, img)
    result["request_ms"] = (time.perf_counter() - request_started) * 1000.0
    result["stages"] = {"transport_decode_ms": decode_ms}
    return result

def _run_raw_ocr(rgba):
    if GPU_PIPELINE is not None:
        try:
            return GPU_PIPELINE(rgba)
        except UnsupportedGpuShape as exc:
            # The fixed bucket grid covers normal RDP bands and 1080p frames.
            # Preserve correctness for larger desktops while Phase 2 defines
            # the resident-framebuffer shape policy.
            img = cv2.cvtColor(rgba, cv2.COLOR_RGBA2BGR)
            result = _run_ocr(img)
            result["gpu_fallback"] = str(exc)
            return result
    return _run_ocr(cv2.cvtColor(rgba, cv2.COLOR_RGBA2BGR))


async def _run_raw_in_executor(rgba, request_started: float, decode_started: float):
    decode_ms = (time.perf_counter() - decode_started) * 1000.0
    loop = asyncio.get_running_loop()
    result = await loop.run_in_executor(RAW_OCR_EXECUTOR, _run_raw_ocr, rgba)
    result["request_ms"] = (time.perf_counter() - request_started) * 1000.0
    result.setdefault("stages", {})["transport_decode_ms"] = decode_ms
    return result


@app.post("/ocr")
async def ocr(request: Request):
    request_started = time.perf_counter()
    body = await request.body()
    decode_started = time.perf_counter()
    img = cv2.imdecode(np.frombuffer(body, dtype=np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        return Response(status_code=400, content="undecodable image")
    return await _run_in_executor(img, request_started, decode_started)


@app.post("/ocr/rgba")
async def ocr_rgba(request: Request):
    """Lower-copy OCR boundary: exact top-down RGBA8, no image codec."""
    request_started = time.perf_counter()
    try:
        validate_content_type(request.headers.get("content-type"))
    except RawImageError as exc:
        return Response(status_code=415, content=str(exc))

    width_header = request.headers.get(WIDTH_HEADER)
    height_header = request.headers.get(HEIGHT_HEADER)
    try:
        width, height = parse_geometry(width_header, height_header)
    except RawImageError as exc:
        return Response(status_code=400, content=str(exc))

    # Admit before collecting the body. Waiting requests therefore retain no
    # 64 MiB framebuffer allocation and cannot queue work inside the executor.
    async with RAW_OCR_ADMISSION:
        try:
            body = await read_exact_request_body(
                request, rgba_body_length(width, height)
            )
            decode_started = time.perf_counter()
            rgba = decode_rgba(body, width_header, height_header)
        except RawImageError as exc:
            return Response(status_code=400, content=str(exc))
        return await _run_raw_in_executor(rgba, request_started, decode_started)


def _resident_session(value):
    if (
        value is None
        or not 2 <= len(value) <= 512
        or len(value) % 2 != 0
        or any(char not in "0123456789abcdef" for char in value)
    ):
        raise RawImageError("invalid resident frame session header")
    return value


def _resident_sequence(value):
    if value is None:
        raise RawImageError("missing resident frame sequence header")
    try:
        sequence = int(value, 10)
    except ValueError as exc:
        raise RawImageError("invalid resident frame sequence header") from exc
    if not 0 <= sequence <= (1 << 64) - 1:
        raise RawImageError("invalid resident frame sequence header")
    return sequence


def _sequence_conflict(exc):
    return Response(
        status_code=409,
        content=str(exc),
        headers={FRAME_SEQUENCE_HEADER: str(exc.expected)},
    )


@app.post("/ocr/frame/rgba")
async def ocr_frame_rgba(request: Request):
    """Composites decoded patches into a per-session GPU framebuffer."""
    request_started = time.perf_counter()
    if RESIDENT_STORE is None:
        return Response(
            status_code=503,
            content="resident RGBA requires CUDA GPU preprocessing",
        )
    try:
        validate_frame_content_type(request.headers.get("content-type"))
    except RawImageError as exc:
        return Response(status_code=415, content=str(exc))
    try:
        width, height = parse_geometry(
            request.headers.get(WIDTH_HEADER),
            request.headers.get(HEIGHT_HEADER),
        )
        session_key = _resident_session(request.headers.get(FRAME_SESSION_HEADER))
        sequence = _resident_sequence(request.headers.get(FRAME_SEQUENCE_HEADER))
    except RawImageError as exc:
        return Response(status_code=400, content=str(exc))

    async with RAW_OCR_ADMISSION:
        try:
            body = await read_bounded_request_body(request)
            decode_started = time.perf_counter()
            frame_request = decode_frame_request(body, width, height)
        except RawImageError as exc:
            return Response(status_code=400, content=str(exc))
        decode_ms = (time.perf_counter() - decode_started) * 1000.0
        loop = asyncio.get_running_loop()
        try:
            result = await loop.run_in_executor(
                RAW_OCR_EXECUTOR,
                RESIDENT_STORE.process,
                session_key,
                sequence,
                width,
                height,
                frame_request,
            )
        except ResidentSequenceError as exc:
            return _sequence_conflict(exc)
        except ResidentCapacityError as exc:
            return Response(status_code=507, content=str(exc))
        except (ValueError, OverflowError) as exc:
            return Response(status_code=400, content=str(exc))
        result["request_ms"] = (time.perf_counter() - request_started) * 1000.0
        result.setdefault("stages", {})["transport_decode_ms"] = decode_ms
        return result


@app.post("/ocr/frame/rdp")
async def ocr_frame_rdp(request: Request):
    """Decodes RDP bitmap orders directly into a resident GPU framebuffer."""
    request_started = time.perf_counter()
    if RESIDENT_STORE is None:
        return Response(
            status_code=503,
            content="resident RDP frames require CUDA GPU preprocessing",
        )
    try:
        validate_rdp_frame_content_type(request.headers.get("content-type"))
    except RawImageError as exc:
        return Response(status_code=415, content=str(exc))
    try:
        width, height = parse_geometry(
            request.headers.get(WIDTH_HEADER),
            request.headers.get(HEIGHT_HEADER),
        )
        session_key = _resident_session(request.headers.get(FRAME_SESSION_HEADER))
        sequence = _resident_sequence(request.headers.get(FRAME_SEQUENCE_HEADER))
    except RawImageError as exc:
        return Response(status_code=400, content=str(exc))

    async with RAW_OCR_ADMISSION:
        try:
            body = await read_bounded_request_body(request)
            decode_started = time.perf_counter()
            frame_request = decode_rdp_frame_request(body, width, height)
        except RawImageError as exc:
            return Response(status_code=400, content=str(exc))
        decode_ms = (time.perf_counter() - decode_started) * 1000.0
        loop = asyncio.get_running_loop()
        try:
            result = await loop.run_in_executor(
                RAW_OCR_EXECUTOR,
                RESIDENT_STORE.process,
                session_key,
                sequence,
                width,
                height,
                frame_request,
            )
        except ResidentSequenceError as exc:
            return _sequence_conflict(exc)
        except ResidentCapacityError as exc:
            return Response(status_code=507, content=str(exc))
        except (ValueError, OverflowError) as exc:
            return Response(status_code=400, content=str(exc))
        result["request_ms"] = (time.perf_counter() - request_started) * 1000.0
        result.setdefault("stages", {})["transport_decode_ms"] = decode_ms
        return result


@app.delete("/ocr/frame/rgba")
@app.delete("/ocr/frame/rdp")
async def release_ocr_frame(request: Request):
    """Idempotently releases one worker-local resident framebuffer."""
    if RESIDENT_STORE is None:
        return Response(status_code=204)
    try:
        session_key = _resident_session(request.headers.get(FRAME_SESSION_HEADER))
        sequence = _resident_sequence(request.headers.get(FRAME_SEQUENCE_HEADER))
    except RawImageError as exc:
        return Response(status_code=400, content=str(exc))
    async with RAW_OCR_ADMISSION:
        loop = asyncio.get_running_loop()
        try:
            await loop.run_in_executor(
                RAW_OCR_EXECUTOR,
                RESIDENT_STORE.release,
                session_key,
                sequence,
            )
        except ResidentSequenceError as exc:
            return _sequence_conflict(exc)
    return Response(status_code=204)


def warmup() -> None:
    """Warm up the models so the first benchmark request is not an outlier.
    Logged explicitly: provider initialization failures (especially CUDA EP)
    surface here, and the operator should see the device they failed on.

    In fp16 mode the stock recognition sessions are NOT warmed (they are
    never used for live recognition); instead detection is warmed alone and
    every bucket session pays its one-time cuDNN EXHAUSTIVE tuning here, at
    startup, instead of on the first live frame that happens to hit its
    width bucket. With WEB_CONCURRENCY workers this cost repeats per worker
    process — measured ~2.2s for 10 buckets on a T4."""
    warm = np.full((64, 320, 3), 255, dtype=np.uint8)
    cv2.putText(warm, "warmup 123", (4, 40), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
    try:
        if BUCKET_REC is None:
            ENGINE(warm)
        else:
            ENGINE.text_det(warm)
        if BUCKET_REC is not None:
            start = time.perf_counter()
            for width, sess in BUCKET_REC.sessions.items():
                zeros = np.zeros((MAX_BATCH, 3, REC_H, width), dtype=np.float32)
                sess.run(None, {BUCKET_REC.input_name: zeros})
            logger.info(
                "fp16 bucket sessions tuned in %.0fms (%d buckets)",
                (time.perf_counter() - start) * 1000.0,
                len(BUCKET_REC.sessions),
            )
        if BUCKET_DET is not None:
            # Pre-tune the det bucket sessions production traffic hits most:
            # dirty bands are full-width strips up to MAX_CHUNK_ROWS+padding
            # tall, plus the downscaled full-frame shape. Remaining bucket
            # combos tune lazily on first hit (one-time cost per bucket).
            start = time.perf_counter()
            for dh, dw in ((64, 1920), (128, 1920), (192, 1920), (256, 1920), (320, 1920), (896, 1440)):
                BUCKET_DET(np.full((dh, dw, 3), 245, dtype=np.uint8))
            logger.info(
                "det bucket sessions tuned in %.0fms (%d sessions)",
                (time.perf_counter() - start) * 1000.0,
                len(BUCKET_DET._dets),
            )
        if GPU_PIPELINE is not None:
            rgba = cv2.cvtColor(warm, cv2.COLOR_BGR2RGBA)
            result = RAW_OCR_EXECUTOR.submit(GPU_PIPELINE, rgba).result()
            logger.info(
                "GPU preprocessing warmed in %.0fms (%d words)",
                result["duration_ms"],
                len(result["words"]),
            )
    except Exception:
        logger.exception("RapidOCR warmup failed on %s", DEVICE)
        raise
    logger.info(
        "RapidOCR warmup completed on %s (rec_lang=%s, rec_precision=%s)",
        DEVICE,
        REC_LANG,
        REC_PRECISION,
    )


warmup()

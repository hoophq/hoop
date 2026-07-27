"""Fixed-shape bucketed text recognition for the OCR server.

ONNXRuntime 1.20 uses the cuDNN frontend for convolutions, and each conv
node caches exactly ONE built execution graph — the one for the LAST input
shape it saw. RapidOCR's stock recognition batches crops by aspect ratio, so
batch widths vary within a single frame; every width change rebuilds and
(with EXHAUSTIVE search) re-tunes the conv graphs. Measured cost: ~60ms per
frame on a T4, ~1.2s per frame on an A100 — the tax GROWS with the GPU's
kernel catalog size, so a faster GPU gets SLOWER. (Fixing it with HEURISTIC
search instead is not an option: it selects unvalidated cuDNN engines that
fail at runtime on Turing — CUDNN_FE failure 11 under concurrent load.)

BucketRec eliminates the shape variance structurally: one dedicated
InferenceSession per width bucket, every batch padded to its bucket's exact
(MAX_BATCH, 3, 48, width) shape. A session only ever sees one shape, so its
conv graphs are built and tuned exactly once per process lifetime. The
padding overhead is only economical with an fp16 model (~half the compute
per padded pixel); measured on a T4 the full stack is 1.8x end-to-end with
text output byte-identical to the stock path.

Concurrency: sessions are shared across the server's executor threads, the
same model the stock path already uses for its engine sessions —
InferenceSession.Run() is documented concurrent-safe by ONNXRuntime.

Sessions and the CTC decoder are injected so the batching/index logic is
unit-testable without onnxruntime or model files (see test_bucket_rec.py).
"""

import logging
import threading
from collections import defaultdict

import cv2
import numpy as np

logger = logging.getLogger("uvicorn.error")

REC_H = 48
BUCKET_WIDTHS = (192, 288, 384, 576, 768, 960, 1152, 1344, 1536, 1920)
MAX_BATCH = 6

# Detection input shape buckets. The det session family has the same
# single-shape conv-cache constraint as recognition: production dirty bands
# arrive at ever-varying heights, and every new (h, w) rebuilds + re-tunes
# the det conv graphs (~500ms per shape change on an A100). Inputs are
# padded to these buckets and each bucket shape gets a dedicated session
# (see the server's BucketDet).
DET_H_BUCKETS = (64, 128, 192, 256, 320, 448, 640, 896, 1088)
DET_W_BUCKETS = (512, 1024, 1440, 1920)


def pad_det_input(img):
    """Pads img to the enclosing (height, width) det bucket with
    BORDER_REPLICATE. Returns (padded, real_h, real_w) so callers can filter
    and clip detection boxes back to the real area. Images larger than the
    biggest bucket are returned unchanged (rare: >4K-wide frames)."""
    h, w = img.shape[:2]
    bh = next((b for b in DET_H_BUCKETS if h <= b), None)
    bw = next((b for b in DET_W_BUCKETS if w <= b), None)
    if bh is None or bw is None or (bh == h and bw == w):
        return img, h, w
    padded = cv2.copyMakeBorder(img, 0, bh - h, 0, bw - w, cv2.BORDER_REPLICATE)
    return padded, h, w


def filter_and_scale_boxes(boxes, real_h, real_w, inv_scale):
    """Post-processes det quads from a padded input: drops phantom boxes
    lying entirely inside the padding, clips boxes crossing into it to the
    real area, and scales coordinates back to full resolution. Returns a
    list of float32 quads, or None when nothing survives.

    Clipping to real_w/real_h (not -1) matches rapidocr's own DB
    postprocess convention (ch_ppocr_det/utils.py clips to dest_width)."""
    out = []
    for box in boxes:
        arr = np.asarray(box, dtype=np.float32)
        if arr.ndim != 2 or arr.shape[0] == 0 or arr.shape[1] != 2:
            logger.warning("dropping malformed det box with shape %s", arr.shape)
            continue
        if arr[:, 0].min() >= real_w or arr[:, 1].min() >= real_h:
            continue  # phantom box entirely inside the padding
        arr = arr.copy()
        arr[:, 0] = np.clip(arr[:, 0], 0.0, float(real_w))
        arr[:, 1] = np.clip(arr[:, 1], 0.0, float(real_h))
        out.append(arr * inv_scale)
    return out or None


class BucketDet:
    """Routes detection to one dedicated detector per padded input shape.

    A single det session re-tunes its cuDNN-FE conv graphs whenever
    consecutive inputs alternate shapes (the cache holds exactly one built
    graph per node), so each bucket shape gets its own detector — created
    lazily on first hit via the injected factory, tuned once, cached for the
    process lifetime. Shapes outside the bucket grid (inputs larger than the
    biggest bucket) run on the shared base detector and are never cached, so
    the cache size is bounded by len(DET_H_BUCKETS) * len(DET_W_BUCKETS).

    Thread-safety: creation is guarded by double-checked locking. Note that
    rapidocr's TextDetector assigns self.preprocess_op per call
    (ch_ppocr_det/main.py) — a benign write here because every call to a
    given bucket detector carries the same padded shape, so concurrent
    writes are idempotent. The stock shared-detector path performs the same
    per-call write with VARYING shapes, so this layout is strictly safer
    than the stock concurrency contract, not looser.

    base_det: fallback detector callable for out-of-grid shapes.
    detector_factory: callable () -> detector; must return a detector whose
        session is exclusively owned by that instance.
    """

    def __init__(self, base_det, detector_factory):
        self._base = base_det
        self._factory = detector_factory
        self._dets = {}
        self._lock = threading.Lock()
        self._grid = {(bh, bw) for bh in DET_H_BUCKETS for bw in DET_W_BUCKETS}

    def __call__(self, det_input):
        key = det_input.shape[:2]
        if key not in self._grid:
            return self._base(det_input)
        det = self._dets.get(key)
        if det is None:
            with self._lock:
                det = self._dets.get(key)
                if det is None:
                    det = self._factory()
                    self._dets[key] = det
        return det(det_input)


class BucketRec:
    """Text recognition through per-width-bucket fixed-shape sessions.

    sessions: dict {bucket_width: session} where session has
        run(None, {input_name: batch}) -> [preds]  (ORT InferenceSession API)
    input_name: model input tensor name.
    decode: CTC decoder callable, preds -> ([(text, conf), ...], ...) — the
        engine's own postprocess_op, so text/conf decoding (including the
        language dictionary) is identical to the stock path.
    """

    def __init__(self, sessions, input_name, decode):
        missing = [w for w in BUCKET_WIDTHS if w not in sessions]
        if missing:
            raise ValueError(f"sessions missing bucket widths: {missing}")
        self.sessions = sessions
        self.input_name = input_name
        self.decode = decode

    @staticmethod
    def bucket_for(scaled_w: int) -> int:
        for b in BUCKET_WIDTHS:
            if scaled_w <= b:
                return b
        return BUCKET_WIDTHS[-1]

    def _preprocess(self, crop, bucket_w: int):
        """Resize a crop to model height and pad to the bucket width.

        Padding is zeros in post-normalize space — EXACTLY what RapidOCR's
        stock recognizer does (rapidocr/ch_ppocr_rec/main.py,
        resize_norm_img: `padding_im = np.zeros(...)` after the /255, -0.5,
        /0.5 normalize), so padded pixels are byte-identical between this
        path and the stock path. Crops wider than the largest bucket are
        squeezed to fit — same behavior as the stock path's max-ratio clamp.
        """
        h, w = crop.shape[:2]
        new_w = min(max(1, int(round(w * REC_H / float(h)))), bucket_w)
        resized = cv2.resize(crop, (new_w, REC_H), interpolation=cv2.INTER_LINEAR)
        x = resized.astype(np.float32)
        x /= 255.0
        x -= 0.5
        x /= 0.5
        x = x.transpose(2, 0, 1)
        if new_w < bucket_w:
            padded = np.zeros((3, REC_H, bucket_w), dtype=np.float32)
            padded[:, :, :new_w] = x
            x = padded
        return x

    def __call__(self, crops):
        """Recognize crops; returns (texts, confs) index-aligned with crops.

        Callers must not pass degenerate (zero-area) crops — the server
        filters those out before EITHER recognition path (see _run_ocr), so
        fp32 and fp16 expose the same contract.
        """
        groups = defaultdict(list)
        squeezed = 0
        for i, crop in enumerate(crops):
            h, w = crop.shape[:2]
            scaled_w = int(round(w * REC_H / float(h)))
            if scaled_w > BUCKET_WIDTHS[-1]:
                squeezed += 1
            groups[self.bucket_for(scaled_w)].append(i)
        if squeezed:
            # If this fires often, the validated latency/accuracy envelope no
            # longer holds and BUCKET_WIDTHS needs a wider tail.
            logger.debug("bucket_rec: %d crop(s) wider than max bucket, squeezed", squeezed)
        texts = [""] * len(crops)
        confs = [0.0] * len(crops)
        for bucket_w, idxs in groups.items():
            sess = self.sessions[bucket_w]
            for start in range(0, len(idxs), MAX_BATCH):
                chunk = idxs[start : start + MAX_BATCH]
                batch = np.stack([self._preprocess(crops[i], bucket_w) for i in chunk])
                if len(chunk) < MAX_BATCH:
                    # Pad the batch dimension too: a constant batch size keeps
                    # the session single-shape (the whole point).
                    pad = np.zeros(
                        (MAX_BATCH - len(chunk), 3, REC_H, bucket_w), dtype=np.float32
                    )
                    batch = np.concatenate([batch, pad])
                preds = sess.run(None, {self.input_name: batch})[0]
                decoded = self.decode(preds[: len(chunk)])[0]
                for j, i in enumerate(chunk):
                    texts[i] = decoded[j][0]
                    confs[i] = float(decoded[j][1])
        return texts, confs

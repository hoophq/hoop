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
from collections import defaultdict

import cv2
import numpy as np

logger = logging.getLogger("uvicorn.error")

REC_H = 48
BUCKET_WIDTHS = (192, 288, 384, 576, 768, 960, 1152, 1344, 1536, 1920)
MAX_BATCH = 6


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

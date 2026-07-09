"""Live concurrency test for BucketDet against the REAL rapidocr detector.

Proves the property the unit tests cannot: many threads invoking the SAME
cached per-bucket detector concurrently produce deterministic boxes and no
exceptions, despite TextDetector assigning self.preprocess_op per call
(benign for a single-shape detector; see BucketDet's docstring).

Requires rapidocr + onnxruntime (GPU optional — CPU provider works, slower).
Run inside the OCR image:

    python -m pytest test_bucket_det_live.py -q
"""

import copy
import threading

import numpy as np
import pytest

cv2 = pytest.importorskip("cv2")
onnxruntime = pytest.importorskip("onnxruntime")
rapidocr_pkg = pytest.importorskip("rapidocr")

import pathlib

from rapidocr import RapidOCR
from rapidocr.inference_engine.onnxruntime.main import OrtInferSession

from bucket_rec import BucketDet

DET_MODEL = str(
    pathlib.Path(str(rapidocr_pkg.__file__)).parent / "models" / "ch_PP-OCRv4_det_mobile.onnx"
)

THREADS = 8
ITERATIONS = 12


@pytest.fixture(scope="module")
def engine():
    return RapidOCR(
        params={
            "Global.use_cls": False,
            "Global.width_height_ratio": -1,
            "Det.limit_type": "max",
            "Det.limit_side_len": 2048,
            "EngineConfig.onnxruntime.use_cuda": "CUDAExecutionProvider"
            in onnxruntime.get_available_providers(),
        }
    )


def _factory(engine):
    def make():
        sess_opts = onnxruntime.SessionOptions()
        sess_opts.log_severity_level = 4
        raw = onnxruntime.InferenceSession(
            DET_MODEL,
            sess_options=sess_opts,
            providers=engine.text_det.session.session.get_providers(),
        )
        det = copy.copy(engine.text_det)
        det.session = OrtInferSession({"session": raw})
        return det

    return make


def test_concurrent_calls_through_same_cached_detector(engine):
    img = np.full((256, 1920, 3), 245, dtype=np.uint8)
    for i, text in enumerate(
        ("alpha bravo charlie 123", "user: bob@hoop.dev", "the quick brown fox")
    ):
        cv2.putText(img, text, (40, 60 + i * 70), cv2.FONT_HERSHEY_SIMPLEX, 0.8,
                    (20, 20, 20), 1, cv2.LINE_AA)

    bd = BucketDet(engine.text_det, _factory(engine))
    baseline = bd(img)  # prime: creates + tunes the (256, 1920) detector
    assert baseline.boxes is not None and len(baseline.boxes) == 3
    assert len(bd._dets) == 1

    barrier = threading.Barrier(THREADS)
    results, errors = [], []
    lock = threading.Lock()

    def hammer():
        try:
            barrier.wait(timeout=30)
            for _ in range(ITERATIONS):
                res = bd(img)
                boxes = np.asarray(res.boxes)
                with lock:
                    results.append(boxes)
        except Exception as exc:  # pragma: no cover - failure reporting
            with lock:
                errors.append(exc)

    threads = [threading.Thread(target=hammer) for _ in range(THREADS)]
    for t in threads:
        t.start()
    for t in threads:
        t.join(timeout=300)

    assert not errors, f"concurrent detection raised: {errors[:3]}"
    assert len(results) == THREADS * ITERATIONS
    ref = np.asarray(baseline.boxes)
    for boxes in results:
        assert boxes.shape == ref.shape, "nondeterministic box count under concurrency"
        assert np.allclose(boxes, ref, atol=1.0), "nondeterministic box geometry under concurrency"
    # still exactly one cached detector — no duplicate creations under load
    assert len(bd._dets) == 1

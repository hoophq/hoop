"""Unit tests for BucketRec batching/indexing with stubbed sessions.

No onnxruntime or model files needed: sessions and the CTC decoder are
injected. Run inside the OCR image (or any env with numpy+opencv):

    python -m pytest test_bucket_rec.py -q
"""

import numpy as np
import pytest

from bucket_rec import BUCKET_WIDTHS, MAX_BATCH, REC_H, BucketRec


class FakeSession:
    """Records every batch it receives; 'recognizes' a crop as its mean pixel
    value so tests can map outputs back to inputs deterministically."""

    def __init__(self, bucket_w):
        self.bucket_w = bucket_w
        self.batches = []

    def run(self, _outputs, feeds):
        batch = feeds["x"]
        assert batch.shape == (MAX_BATCH, 3, REC_H, self.bucket_w), (
            f"session for width {self.bucket_w} got shape {batch.shape}"
        )
        self.batches.append(batch.copy())
        # One "pred" per batch row: the row's mean, so decode can label it.
        return [np.array([row.mean() for row in batch])]


def fake_decode(preds):
    # Mimics rapidocr's CTCLabelDecode return shape: (results, ...) where
    # results = [(text, conf), ...].
    return ([(f"m{p:.4f}", 0.5) for p in preds],)


def make_rec():
    sessions = {w: FakeSession(w) for w in BUCKET_WIDTHS}
    return BucketRec(sessions=sessions, input_name="x", decode=fake_decode), sessions


def crop(w, h=REC_H, value=128):
    return np.full((h, w, 3), value, dtype=np.uint8)


def test_missing_bucket_session_rejected():
    with pytest.raises(ValueError, match="missing bucket widths"):
        BucketRec(sessions={192: FakeSession(192)}, input_name="x", decode=fake_decode)


def test_empty_crop_list():
    rec, sessions = make_rec()
    texts, confs = rec([])
    assert texts == [] and confs == []
    assert all(not s.batches for s in sessions.values())


def test_single_crop_exact_bucket_width():
    rec, sessions = make_rec()
    texts, confs = rec([crop(384)])
    assert len(texts) == 1 and texts[0].startswith("m")
    assert confs == [0.5]
    assert len(sessions[384].batches) == 1
    assert all(not s.batches for w, s in sessions.items() if w != 384)


def test_bucket_selection_rounds_up():
    rec, sessions = make_rec()
    # width 200 at h=48 scales to 200 -> bucket 288 (first >= 200)
    rec([crop(200)])
    assert len(sessions[288].batches) == 1


def test_output_order_preserved_across_buckets():
    rec, _ = make_rec()
    # Alternate narrow/wide so bucket grouping reorders processing; distinct
    # pixel values let us verify each output slot maps to its input crop.
    crops = [crop(100, value=10), crop(1000, value=250), crop(120, value=40),
             crop(900, value=200)]
    texts, _ = rec(crops)
    assert len(texts) == 4
    # Narrow crops (values 10, 40) are darker -> lower mean than wide bright
    # ones in THEIR OWN slots; verify relative ordering per slot pair.
    vals = [float(t[1:]) for t in texts]
    assert vals[0] < vals[1] and vals[2] < vals[3]


def test_partial_batch_padded_to_constant_shape():
    rec, sessions = make_rec()
    # 8 same-bucket crops -> one full batch of 6 + one partial of 2, both
    # delivered at the constant (MAX_BATCH, ...) shape (FakeSession asserts).
    texts, _ = rec([crop(300)] * 8)
    assert len(texts) == 8 and all(t for t in texts)
    assert len(sessions[384].batches) == 2
    # Padded rows are zeros: rows 2..5 of the second batch must be all-zero.
    second = sessions[384].batches[1]
    assert np.all(second[2:] == 0.0)


def test_crop_wider_than_max_bucket_squeezed():
    rec, sessions = make_rec()
    texts, _ = rec([crop(4000)])
    assert len(texts) == 1 and texts[0]
    assert len(sessions[BUCKET_WIDTHS[-1]].batches) == 1


def test_padding_is_zeros_after_normalize():
    """The padded tail must be zeros in post-normalize space — the exact
    semantics of rapidocr's resize_norm_img (ch_ppocr_rec/main.py), which
    zero-fills padding_im AFTER the /255, -0.5, /0.5 normalize. This is what
    makes fp16 output byte-identical to the stock path."""
    rec, sessions = make_rec()
    rec([crop(100, value=255)])  # white crop, scaled width 100 -> bucket 192
    batch = sessions[192].batches[0]
    # Content region: white -> +1.0 after normalize.
    assert np.allclose(batch[0, :, :, :100], 1.0)
    # Padded region: zeros (NOT background white).
    assert np.all(batch[0, :, :, 100:] == 0.0)

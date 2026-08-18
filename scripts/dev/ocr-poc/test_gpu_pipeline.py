"""GPU-resident OCR cache contract tests.

Run inside the CUDA OCR image:

    python -m pytest test_gpu_pipeline.py -q
"""

import collections
import struct
import threading
import types

import cupy as cp
import gpu_pipeline as gpu_pipeline_module
import numpy as np
import pytest
from gpu_pipeline import (
    GpuOcrPipeline,
    GpuResidentStore,
    ResidentSequenceError,
    _CachedRecognition,
)
from raw_image import (
    FRAME_FULL_SNAPSHOT,
    RDP_FRAME_MAGIC,
    RDP_FRAME_VERSION,
    RDP_PATCH_COMPRESSED,
    FrameChunk,
    FramePatch,
    FrameRequest,
    decode_rdp_frame_request,
)


class StubPipeline:
    def __init__(self):
        self.calls = []
        self.scrubs = 0

    @staticmethod
    def _upload(host):
        return cp.asarray(host), 0.0

    def process_device(self, frame, **_kwargs):
        marker = int(frame[0, 0, 0].item())
        self.calls.append(marker)
        return {
            "duration_ms": 1.0,
            "words": [
                {
                    "text": str(marker),
                    "conf": 1.0,
                    "x": 0,
                    "y": 0,
                    "w": 1,
                    "h": 1,
                }
            ],
            "stages": {"det_infer_ms": 1.0},
        }

    def scrub_sensitive_scratch(self):
        self.scrubs += 1


class FailingPipeline(StubPipeline):
    def __init__(self):
        super().__init__()
        self.retained_frame = None

    def process_device(self, frame, **_kwargs):
        self.retained_frame = frame
        raise RuntimeError("recognition failed")


class FailAfterOnePipeline(StubPipeline):
    def process_device(self, frame, **kwargs):
        if self.calls:
            raise RuntimeError("recognition failed")
        return super().process_device(frame, **kwargs)


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def advance(self, seconds):
        self.now += seconds


def frame_request(value, width=4, height=2, *, full_snapshot=False):
    pixel = bytes((value, 0, 0, 255))
    patch = FramePatch(0, 0, width, height, memoryview(pixel * width * height))
    chunk = FrameChunk(0, height, 0, height)
    return FrameRequest(
        FRAME_FULL_SNAPSHOT if full_snapshot else 0,
        (patch,),
        (chunk,),
    )


def process(store, sequence, value, *, full_snapshot=False):
    return store.process(
        "session",
        sequence,
        4,
        2,
        frame_request(value, full_snapshot=full_snapshot),
    )


class RdpStubPipeline(StubPipeline):
    def __init__(self):
        super().__init__()
        self._tls = threading.local()

    _state = GpuOcrPipeline._state
    _upload_bytes = GpuOcrPipeline._upload_bytes
    _rdp_buffers = GpuOcrPipeline._rdp_buffers
    scrub_sensitive_scratch = GpuOcrPipeline.scrub_sensitive_scratch


def rdp_request(
    width, height, bits_per_pixel, compressed, pixels, *, chunks=()
):
    flags = RDP_PATCH_COMPRESSED if compressed else 0
    body = b"".join(
        (
            struct.pack(
                "<4sHHHH",
                RDP_FRAME_MAGIC,
                RDP_FRAME_VERSION,
                0,
                1,
                len(chunks),
            ),
            struct.pack(
                "<HHHHHHI",
                0,
                0,
                width,
                height,
                bits_per_pixel,
                flags,
                len(pixels),
            ),
            pixels,
            b"".join(
                struct.pack(
                    "<HHHH",
                    chunk.win_y0,
                    chunk.win_y1,
                    chunk.own_y0,
                    chunk.own_y1,
                )
                for chunk in chunks
            ),
        )
    )
    return decode_rdp_frame_request(body, width, height)


def apply_rdp(width, height, bits_per_pixel, compressed, pixels):
    pipeline = RdpStubPipeline()
    store = GpuResidentStore(pipeline)
    result = store.process(
        "rdp-session",
        0,
        width,
        height,
        rdp_request(width, height, bits_per_pixel, compressed, pixels),
    )
    frame = cp.asnumpy(store._sessions["rdp-session"].frame)
    store.release("rdp-session", 1)
    return frame, result


def wire24(value):
    return int(value).to_bytes(3, "little")


def rgba24(value):
    return [
        (value >> 16) & 0xFF,
        (value >> 8) & 0xFF,
        value & 0xFF,
        255,
    ]


@pytest.mark.parametrize(
    ("bits_per_pixel", "pixels"),
    [
        (
            16,
            b"".join(
                value.to_bytes(2, "little")
                for value in (0xF800, 0x07E0, 0x001F, 0xFFFF)
            ),
        ),
        (
            24,
            b"".join(
                wire24(value)
                for value in (0xFF0000, 0x00FF00, 0x0000FF, 0xFFFFFF)
            ),
        ),
        (
            32,
            b"".join(
                value.to_bytes(4, "little")
                for value in (
                    0x11FF0000,
                    0x2200FF00,
                    0x330000FF,
                    0x44FFFFFF,
                )
            ),
        ),
    ],
)
def test_cuda_uncompressed_rdp_depths_are_top_down_rgba(
    bits_per_pixel, pixels
):
    frame, result = apply_rdp(2, 2, bits_per_pixel, False, pixels)

    assert frame.tolist() == [
        [[0, 0, 255, 255], [255, 255, 255, 255]],
        [[255, 0, 0, 255], [0, 255, 0, 255]],
    ]
    assert result["frame_changed"]
    assert result["stages"]["frame_decode_composite_ms"] >= 0.0


@pytest.mark.parametrize(
    ("stream", "expected"),
    [
        (
            bytes((0x84,))
            + b"".join(wire24(value) for value in (1, 2, 3, 4)),
            (1, 2, 3, 4),
        ),
        (
            bytes((0xF4, 4, 0))
            + b"".join(wire24(value) for value in (1, 2, 3, 4)),
            (1, 2, 3, 4),
        ),
        (bytes((0x64,)) + wire24(0x123456), (0x123456,) * 4),
        (
            bytes((0xF3, 4, 0)) + wire24(0x123456),
            (0x123456,) * 4,
        ),
        (
            bytes((0xC4,)) + wire24(0x123456),
            (0x123456,) * 4,
        ),
        (
            bytes((0xF6, 4, 0)) + wire24(0x123456),
            (0x123456,) * 4,
        ),
        (
            bytes((0x41, 0x05)),
            (0xFFFFFF, 0, 0xFFFFFF, 0, 0, 0, 0, 0),
        ),
        (
            bytes((0xF2, 8, 0, 0x05)),
            (0xFFFFFF, 0, 0xFFFFFF, 0, 0, 0, 0, 0),
        ),
        (
            bytes((0xD1,)) + wire24(0x123456) + bytes((0x05,)),
            (0x123456, 0, 0x123456, 0, 0, 0, 0, 0),
        ),
        (
            bytes((0xF7, 8, 0)) + wire24(0x123456) + bytes((0x05,)),
            (0x123456, 0, 0x123456, 0, 0, 0, 0, 0),
        ),
        (
            bytes((0xE2,))
            + wire24(0x123456)
            + wire24(0xABCDEF),
            (0x123456, 0xABCDEF, 0x123456, 0xABCDEF),
        ),
        (
            bytes((0xF8, 2, 0))
            + wire24(0x123456)
            + wire24(0xABCDEF),
            (0x123456, 0xABCDEF, 0x123456, 0xABCDEF),
        ),
        (
            bytes((0xF9,)),
            (0xFFFFFF, 0xFFFFFF, 0, 0, 0, 0, 0, 0),
        ),
        (
            bytes((0xFA,)),
            (0xFFFFFF, 0, 0xFFFFFF, 0, 0, 0, 0, 0),
        ),
        (
            bytes((0xFD, 0xFE)),
            (0xFFFFFF, 0),
        ),
    ],
)
def test_cuda_rdp_decoder_matches_order_semantics(stream, expected):
    frame, _result = apply_rdp(len(expected), 1, 24, True, stream)

    assert frame[0].tolist() == [rgba24(value) for value in expected]


def test_cuda_rdp_background_orders_copy_prior_scanline():
    bottom = (0x112233, 0x445566, 0x778899, 0xAABBCC)
    stream = (
        bytes((0x84,))
        + b"".join(wire24(value) for value in bottom)
        + bytes((0x04,))
    )

    frame, _result = apply_rdp(4, 2, 24, True, stream)

    expected = [rgba24(value) for value in bottom]
    assert frame[0].tolist() == expected
    assert frame[1].tolist() == expected

@pytest.mark.parametrize(
    ("stream", "expected_top", "expected_bottom"),
    [
        (
            bytes((0x61,)) + wire24(0xFF0000) + bytes((0xF0, 3, 0)),
            (0xFF0000, 0),
            (0xFF0000, 0),
        ),
        (
            bytes((0x61,))
            + wire24(0xFF0000)
            + bytes((0xC3,))
            + wire24(0x0000FF),
            (0xFF00FF, 0),
            (0xFF0000, 0x0000FF),
        ),
        (
            bytes((0x61,))
            + wire24(0xFF0000)
            + bytes((0xF7, 3, 0))
            + wire24(0x0000FF)
            + bytes((0x05,)),
            (0xFF0000, 0),
            (0xFF0000, 0x0000FF),
        ),
    ],
)
def test_cuda_rdp_orders_crossing_scanline_match_canonical_semantics(
    stream, expected_top, expected_bottom
):
    frame, _result = apply_rdp(2, 2, 24, True, stream)

    assert frame[0].tolist() == [rgba24(value) for value in expected_top]
    assert frame[1].tolist() == [rgba24(value) for value in expected_bottom]


def test_cuda_rdp_decoder_rejects_truncated_order_and_drops_frame():
    pipeline = RdpStubPipeline()
    store = GpuResidentStore(pipeline)
    request = rdp_request(1, 1, 24, True, bytes((0x61, 0x00)))

    with pytest.raises(ValueError, match="CUDA decoder error 2"):
        store.process("bad-rdp", 0, 1, 1, request)

    assert "bad-rdp" not in store._sessions


def test_cuda_rdp_decoder_rejects_incomplete_output_and_drops_frame():
    pipeline = RdpStubPipeline()
    store = GpuResidentStore(pipeline)
    request = rdp_request(2, 1, 24, True, bytes((0xFD,)))

    with pytest.raises(ValueError, match="CUDA decoder error 6"):
        store.process("short-rdp", 0, 2, 1, request)

    assert "short-rdp" not in store._sessions


def test_alternating_exact_pixel_state_reuses_ocr_words():
    pipeline = StubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)

    first = process(store, 0, 10, full_snapshot=True)
    second = process(store, 1, 20)
    repeated = process(store, 2, 10)

    assert pipeline.calls == [10, 20]
    assert [first["ocr_cache_misses"], second["ocr_cache_misses"]] == [1, 1]
    assert repeated["ocr_cache_hits"] == 1
    assert repeated["ocr_cache_misses"] == 0
    assert repeated["ocr_cache_entries"] == 2
    assert repeated["words"] == first["words"]
    assert "det_infer_ms" not in repeated["stages"]
    assert repeated["stages"]["frame_hash_ms"] >= 0.0

    store.release("session", 3)

def test_rdp_final_slice_with_chunks_ocrs_even_when_slice_is_identical():
    pipeline = RdpStubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)
    pixels = bytes((0, 0, 10))
    initial = rdp_request(1, 1, 24, False, pixels)
    first = store.process("split-rdp", 0, 1, 1, initial)
    assert first["frame_changed"]
    assert first["words"] == []

    chunk = FrameChunk(0, 1, 0, 1)
    final = rdp_request(1, 1, 24, False, pixels, chunks=(chunk,))
    result = store.process("split-rdp", 1, 1, 1, final)

    assert not result["frame_changed"]
    assert [word["text"] for word in result["words"]] == ["10"]
    assert pipeline.calls == [10]
    store.release("split-rdp", 2)


@pytest.mark.parametrize(
    "pixel",
    [
        bytes((11, 0, 0, 255)),
        bytes((10, 1, 0, 255)),
        bytes((10, 0, 1, 255)),
        bytes((10, 0, 0, 254)),
    ],
)
def test_one_pixel_channel_change_forces_fresh_ocr(pixel):
    pipeline = StubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)

    process(store, 0, 10, full_snapshot=True)
    one_pixel = FramePatch(3, 1, 1, 1, memoryview(pixel))
    chunk = FrameChunk(0, 2, 0, 2)
    changed = store.process(
        "session", 1, 4, 2, FrameRequest(0, (one_pixel,), (chunk,))
    )

    # The marker pixel used by the stub stayed unchanged. A second inference
    # proves the device fingerprint covers the changed pixel at the other end.
    assert pipeline.calls == [10, 10]
    assert changed["ocr_cache_hits"] == 0
    assert changed["ocr_cache_misses"] == 1

    store.release("session", 2)


def test_cache_lru_bound_evicts_oldest_exact_state():
    pipeline = StubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=2)

    process(store, 0, 10, full_snapshot=True)
    first_cached_text = next(iter(store._sessions["session"].ocr_cache.values()))._words[0][0]
    process(store, 1, 20)
    third = process(store, 2, 30)
    assert not any(first_cached_text)
    evicted = process(store, 3, 10)

    assert third["ocr_cache_entries"] == 2
    assert evicted["ocr_cache_hits"] == 0
    assert evicted["ocr_cache_misses"] == 1
    assert pipeline.calls == [10, 20, 30, 10]

    store.release("session", 4)


def test_chunk_ownership_change_misses_cache_and_filters_words():
    pipeline = StubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)

    first = frame_request(10, full_snapshot=True)
    first = FrameRequest(first.flags, first.patches, (FrameChunk(0, 2, 0, 1),))
    assert store.process("session", 0, 4, 2, first)["words"]
    process(store, 1, 20)
    repeated = frame_request(10)
    repeated = FrameRequest(
        repeated.flags, repeated.patches, (FrameChunk(0, 2, 1, 2),)
    )
    result = store.process("session", 2, 4, 2, repeated)

    assert result["ocr_cache_hits"] == 0
    assert result["ocr_cache_misses"] == 1
    assert result["words"] == []
    assert pipeline.calls == [10, 20, 10]

    store.release("session", 3)


def test_full_snapshot_discards_prior_cache_entries():
    pipeline = StubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)

    process(store, 0, 10, full_snapshot=True)
    process(store, 1, 20)
    old_state = store._sessions["session"]
    cached_text = next(iter(old_state.ocr_cache.values()))._words[0][0]
    reset = process(store, 2, 10, full_snapshot=True)

    assert pipeline.calls == [10, 20, 10]
    assert reset["ocr_cache_hits"] == 0
    assert reset["ocr_cache_misses"] == 1
    assert reset["ocr_cache_entries"] == 1
    assert not cp.any(old_state.frame)
    assert not old_state.ocr_cache
    assert not old_state.recognition_cache
    assert not any(cached_text)

    store.release("session", 3)


def test_cache_bound_must_be_positive():
    with pytest.raises(ValueError, match="cache entries must be positive"):
        GpuResidentStore(StubPipeline(), max_cache_entries=0)
    with pytest.raises(ValueError, match="recognition cache entries must be positive"):
        GpuResidentStore(StubPipeline(), max_recognition_cache_entries=0)


def test_release_discards_cached_words():
    pipeline = StubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)

    process(store, 0, 10, full_snapshot=True)
    old_state = store._sessions["session"]
    cached_text = next(iter(old_state.ocr_cache.values()))._words[0][0]
    recognition = _CachedRecognition("secret", 1.0)
    recognition_text = recognition._text
    old_state.recognition_cache["line"] = recognition
    store.release("session", 1)
    assert not cp.any(old_state.frame)
    assert not old_state.ocr_cache
    assert not old_state.recognition_cache
    assert not any(cached_text)
    assert not any(recognition_text)
    recreated = process(store, 0, 10, full_snapshot=True)

    assert recreated["ocr_cache_hits"] == 0
    assert recreated["ocr_cache_misses"] == 1
    assert pipeline.calls == [10, 10]

    store.release("session", 1)


def test_idle_expiry_forces_incremental_request_to_resync():
    pipeline = StubPipeline()
    clock = FakeClock()
    store = GpuResidentStore(
        pipeline, ttl_seconds=5.0, max_cache_entries=4, clock=clock
    )

    process(store, 0, 10, full_snapshot=True)
    old_state = store._sessions["session"]
    cached_text = next(iter(old_state.ocr_cache.values()))._words[0][0]
    clock.advance(5.0)
    store.evict_expired()
    assert not cp.any(old_state.frame)
    assert not old_state.ocr_cache
    assert not old_state.recognition_cache
    assert not any(cached_text)

    with pytest.raises(ResidentSequenceError) as error:
        process(store, 1, 10)
    assert error.value.expected == 0
    assert pipeline.calls == [10]


def test_reusable_scratch_is_scrubbed_before_session_ownership_changes():
    pipeline = StubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)

    store.process("session-a", 0, 4, 2, frame_request(10, full_snapshot=True))
    scrubs_before_switch = pipeline.scrubs
    store.process("session-b", 0, 4, 2, frame_request(20, full_snapshot=True))

    assert pipeline.scrubs == scrubs_before_switch + 1
    assert store._scratch_owner == "session-b"
    store.release("session-a", 1)
    assert store._scratch_owner == "session-b"
    store.release("session-b", 1)
    assert store._scratch_owner is None


def test_recognition_failure_scrubs_and_releases_partial_resident_frame():
    pipeline = FailingPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)

    with pytest.raises(RuntimeError, match="recognition failed"):
        process(store, 0, 10, full_snapshot=True)

    assert pipeline.retained_frame is not None
    assert not cp.any(pipeline.retained_frame)
    assert "session" not in store._sessions
    assert pipeline.scrubs >= 1


def test_recognition_failure_wipes_preexisting_cached_text():
    pipeline = FailAfterOnePipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)
    process(store, 0, 10, full_snapshot=True)
    state = store._sessions["session"]
    cached_text = next(iter(state.ocr_cache.values()))._words[0][0]
    recognition = _CachedRecognition("secret", 1.0)
    recognition_text = recognition._text
    state.recognition_cache["line"] = recognition

    with pytest.raises(RuntimeError, match="recognition failed"):
        process(store, 1, 20)

    assert not any(cached_text)
    assert not any(recognition_text)
    assert "session" not in store._sessions


def test_detected_line_cache_hashes_only_its_exact_gpu_rectangle():
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    recognition_calls = []

    def recognize_crops(
        _self,
        boxes,
        crops,
        timings,
        _started,
        crop_download_ms=0.0,
        line_hash_ms=0.0,
        texts=None,
        scores=None,
    ):
        recognition_calls.append(len(crops))
        for index, _crop in crops:
            texts[index] = "cached line"
            scores[index] = 0.99
        timings.update(
            {
                "crop_rec_preprocess_ms": 0.0,
                "crop_download_ms": crop_download_ms,
                "line_hash_ms": line_hash_ms,
                "rec_upload_ms": 0.0,
                "rec_infer_ms": 1.0 if crops else 0.0,
                "ctc_reduce_decode_ms": 0.0,
            }
        )
        return texts, scores

    pipeline._recognize_crops = types.MethodType(recognize_crops, pipeline)
    host = np.zeros((40, 64, 4), dtype=np.uint8)
    host[:, :, 3] = 255
    frame = cp.asarray(host)
    box = np.asarray(
        [[20, 10], [30, 10], [30, 18], [20, 18]], dtype=np.float32
    )
    cache = collections.OrderedDict()
    hash_output = cp.empty(2, dtype=cp.uint64)

    first = pipeline._recognize_device(
        frame, [box], {}, cache, hash_output, max_cache_entries=1
    )
    first_cached_text = next(iter(cache.values()))._text
    repeated = pipeline._recognize_device(
        frame, [box], {}, cache, hash_output, max_cache_entries=1
    )
    frame[0, 0, 1] = 1
    outside_change = pipeline._recognize_device(
        frame, [box], {}, cache, hash_output, max_cache_entries=1
    )
    frame[10, 20, 1] = 1
    inside_change = pipeline._recognize_device(
        frame, [box], {}, cache, hash_output, max_cache_entries=1
    )

    assert first[2:] == (0, 1)
    assert repeated[2:] == (1, 0)
    assert outside_change[2:] == (1, 0)
    assert inside_change[2:] == (0, 1)
    assert recognition_calls == [1, 0, 0, 1]
    assert first[:2] == repeated[:2] == outside_change[:2] == inside_change[:2]
    assert not any(first_cached_text)


def test_tight_device_crops_match_full_host_crops_at_edges_and_rotation():
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    captured = []

    def capture_crops(
        _self,
        boxes,
        crops,
        _timings,
        _started,
        **_kwargs,
    ):
        captured.append([crop.copy() for _index, crop in crops])
        return [""] * len(boxes), [0.0] * len(boxes)

    pipeline._recognize_crops = types.MethodType(capture_crops, pipeline)
    rng = np.random.default_rng(102)
    host = rng.integers(0, 256, size=(40, 64, 4), dtype=np.uint8)
    boxes = [
        np.asarray([[0, 0], [18, 0], [18, 12], [0, 12]], dtype=np.float32),
        np.asarray([[45, 25], [63, 24], [63, 39], [44, 39]], dtype=np.float32),
        np.asarray([[15, 10], [35, 7], [37, 23], [17, 26]], dtype=np.float32),
    ]

    pipeline._recognize_host(host, boxes, {})
    pipeline._recognize_device(cp.asarray(host), boxes, {})

    assert len(captured) == 2
    assert len(captured[0]) == len(captured[1]) == len(boxes)
    for full_crop, tight_crop in zip(captured[0], captured[1], strict=True):
        assert np.array_equal(full_crop, tight_crop)

def test_dynamic_detector_shapes_do_not_accumulate_tls_or_pool_allocations():
    class FakeBinding:
        def bind_input(self, **_kwargs):
            pass

        def bind_output(self, **_kwargs):
            pass

        def synchronize_inputs(self):
            pass

        def synchronize_outputs(self):
            pass

    class FakeSession:
        @staticmethod
        def io_binding():
            return FakeBinding()

        @staticmethod
        def get_inputs():
            return [types.SimpleNamespace(name="input")]

        @staticmethod
        def get_outputs():
            return [types.SimpleNamespace(name="output")]

        @staticmethod
        def run_with_iobinding(_binding):
            pass

    detector = types.SimpleNamespace(
        session=types.SimpleNamespace(session=FakeSession()),
        postprocess_op=lambda _prediction, _shape: (None, None),
    )
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    pipeline.det_downscale = 1.0
    pipeline.det_min_side = 1
    pipeline.base_detector = types.SimpleNamespace(
        mean=(0.485, 0.456, 0.406),
        std=(0.229, 0.224, 0.225),
    )
    pipeline.bucket_det = types.SimpleNamespace(
        detector_for_shape=lambda _shape: detector
    )
    device_pool = cp.get_default_memory_pool()
    pinned_pool = cp.get_default_pinned_memory_pool()
    device_pool.free_all_blocks()
    pinned_pool.free_all_blocks()
    initial_device_bytes = device_pool.total_bytes()

    for width in range(1952, 2209, 32):
        frame = cp.zeros((32, width, 4), dtype=cp.uint8)
        assert pipeline._detect(frame, {}, allow_dynamic=True) is None
        del frame

    device_pool.free_all_blocks()
    pinned_pool.free_all_blocks()
    state = pipeline._state()
    assert not state["det_inputs"]
    assert not state["det_bindings"]
    assert device_pool.total_bytes() <= initial_device_bytes


def test_dynamic_detector_input_is_scrubbed_when_binding_creation_fails():
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    pipeline.det_downscale = 1.0
    pipeline.det_min_side = 1
    pipeline.base_detector = types.SimpleNamespace(
        mean=(0.485, 0.456, 0.406),
        std=(0.229, 0.224, 0.225),
    )
    pipeline.bucket_det = types.SimpleNamespace(
        detector_for_shape=lambda _shape: types.SimpleNamespace(
            session=types.SimpleNamespace(session=object())
        )
    )
    captured = []
    detector_input = pipeline._detector_input

    def capture_input(_self, *args, **kwargs):
        result = detector_input(*args, **kwargs)
        captured.append(result[0])
        return result

    def fail_binding(_self, *_args, **_kwargs):
        raise RuntimeError("bind failed")

    pipeline._detector_input = types.MethodType(capture_input, pipeline)
    pipeline._detector_binding = types.MethodType(fail_binding, pipeline)
    frame = cp.full((32, 1952, 4), 255, dtype=cp.uint8)

    with pytest.raises(RuntimeError, match="bind failed"):
        pipeline._detect(frame, {}, allow_dynamic=True)

    assert captured
    assert not cp.any(captured[0])


def test_dynamic_detector_preprocess_failure_scrubs_allocated_input(monkeypatch):
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    pipeline.det_downscale = 1.0
    pipeline.det_min_side = 1
    pipeline.base_detector = types.SimpleNamespace(
        mean=(0.485, 0.456, 0.406),
        std=(0.229, 0.224, 0.225),
    )
    allocated = []
    cupy_empty = cp.empty

    def capture_empty(*args, **kwargs):
        value = cupy_empty(*args, **kwargs)
        allocated.append(value)
        return value

    def fail_kernel(*_args, **_kwargs):
        raise RuntimeError("kernel failed")

    monkeypatch.setattr(gpu_pipeline_module.cp, "empty", capture_empty)
    monkeypatch.setattr(gpu_pipeline_module, "_DET_KERNEL", fail_kernel)
    frame = cp.full((32, 1952, 4), 255, dtype=cp.uint8)

    with pytest.raises(RuntimeError, match="kernel failed"):
        pipeline._detector_input(frame, allow_dynamic=True)

    assert allocated
    assert not cp.any(allocated[-1])
def test_dynamic_pinned_staging_allocation_failure_scrubs_device_output(
    monkeypatch,
):
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    device_allocations = []
    cupy_empty = cp.empty

    def capture_device(*args, **kwargs):
        value = cupy_empty(*args, **kwargs)
        device_allocations.append(value)
        return value

    def fail_pinned(*_args, **_kwargs):
        raise RuntimeError("pinned allocation failed")

    monkeypatch.setattr(gpu_pipeline_module.cp, "empty", capture_device)
    monkeypatch.setattr(
        gpu_pipeline_module.cupyx, "empty_pinned", fail_pinned
    )
    det_input = cp.zeros((1, 3, 32, 1952), dtype=cp.float32)

    with pytest.raises(RuntimeError, match="pinned allocation failed"):
        pipeline._detector_binding(
            object(), det_input, (32, 1952), cacheable=False
        )

    assert device_allocations
    assert not cp.any(device_allocations[-1])




@pytest.mark.parametrize("cacheable", [False, True])
def test_detector_binding_failure_scrubs_allocated_buffers(monkeypatch, cacheable):
    class FailingBinding:
        def bind_input(self, **_kwargs):
            pass

        def bind_output(self, **_kwargs):
            raise RuntimeError("output bind failed")

    session = types.SimpleNamespace(
        io_binding=lambda: FailingBinding(),
        get_inputs=lambda: [types.SimpleNamespace(name="input")],
        get_outputs=lambda: [types.SimpleNamespace(name="output")],
    )
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    device_allocations = []
    host_allocations = []
    cupy_empty = cp.empty
    pinned_empty = gpu_pipeline_module.cupyx.empty_pinned

    def capture_device(*args, **kwargs):
        value = cupy_empty(*args, **kwargs)
        device_allocations.append(value)
        return value

    def capture_host(*args, **kwargs):
        value = pinned_empty(*args, **kwargs)
        host_allocations.append(value)
        return value

    monkeypatch.setattr(gpu_pipeline_module.cp, "empty", capture_device)
    monkeypatch.setattr(
        gpu_pipeline_module.cupyx, "empty_pinned", capture_host
    )
    det_input = cp.zeros((1, 3, 32, 1952), dtype=cp.float32)

    with pytest.raises(RuntimeError, match="output bind failed"):
        pipeline._detector_binding(
            session, det_input, (32, 1952), cacheable=cacheable
        )

    assert device_allocations and host_allocations
    assert not cp.any(device_allocations[-1])
    assert not np.any(host_allocations[-1])



def test_dynamic_detector_inference_failure_scrubs_all_transient_buffers(
    monkeypatch,
):
    class FailingSession:
        def __init__(self):
            self.binding = types.SimpleNamespace(
                bind_input=lambda **_kwargs: None,
                bind_output=lambda **_kwargs: None,
                synchronize_inputs=lambda: None,
                synchronize_outputs=lambda: None,
            )

        def io_binding(self):
            return self.binding

        @staticmethod
        def get_inputs():
            return [types.SimpleNamespace(name="input")]

        @staticmethod
        def get_outputs():
            return [types.SimpleNamespace(name="output")]

        @staticmethod
        def run_with_iobinding(_binding):
            raise RuntimeError("detector inference failed")

    session = FailingSession()
    detector = types.SimpleNamespace(
        session=types.SimpleNamespace(session=session),
        postprocess_op=lambda _prediction, _shape: (None, None),
    )
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    pipeline.det_downscale = 1.0
    pipeline.det_min_side = 1
    pipeline.base_detector = types.SimpleNamespace(
        mean=(0.485, 0.456, 0.406),
        std=(0.229, 0.224, 0.225),
    )
    pipeline.bucket_det = types.SimpleNamespace(
        detector_for_shape=lambda _shape: detector
    )
    device_allocations = []
    host_allocations = []
    cupy_empty = cp.empty
    pinned_empty = gpu_pipeline_module.cupyx.empty_pinned

    def capture_device(*args, **kwargs):
        value = cupy_empty(*args, **kwargs)
        device_allocations.append(value)
        return value

    def capture_host(*args, **kwargs):
        value = pinned_empty(*args, **kwargs)
        host_allocations.append(value)
        return value

    frame = cp.full((32, 1952, 4), 255, dtype=cp.uint8)
    monkeypatch.setattr(gpu_pipeline_module.cp, "empty", capture_device)
    monkeypatch.setattr(
        gpu_pipeline_module.cupyx, "empty_pinned", capture_host
    )

    with pytest.raises(RuntimeError, match="detector inference failed"):
        pipeline._detect(frame, {}, allow_dynamic=True)

    assert device_allocations and host_allocations
    assert all(not cp.any(value) for value in device_allocations)
    assert all(not np.any(value) for value in host_allocations)


def test_device_crop_temporary_is_owned_and_scrubbed(monkeypatch):
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    pipeline._recognize_crops = types.MethodType(
        lambda _self, boxes, _crops, *_args, **_kwargs: (
            [""] * len(boxes),
            [0.0] * len(boxes),
        ),
        pipeline,
    )
    frame = cp.full((16, 32, 4), 255, dtype=cp.uint8)
    box = np.asarray(
        [[0, 0], [31, 0], [31, 15], [0, 15]], dtype=np.float32
    )
    captured = []
    cupy_asnumpy = cp.asnumpy

    def capture_device_crop(value):
        captured.append(value)
        return cupy_asnumpy(value)

    monkeypatch.setattr(gpu_pipeline_module.cp, "asnumpy", capture_device_crop)

    pipeline._recognize_device(frame, [box], {})

    assert len(captured) == 1
    assert not cp.any(captured[0])
    assert cp.all(frame == 255), "scrubbing the crop must not erase the framebuffer"


def _recognition_failure_pipeline(session, decode):
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    pipeline.bucket_rec = types.SimpleNamespace(
        bucket_for=lambda _scaled_width: 32,
        sessions={32: session},
        _preprocess=lambda _crop, bucket_width: np.ones(
            (3, gpu_pipeline_module.REC_H, bucket_width), dtype=np.float32
        ),
        decode=types.SimpleNamespace(decode=decode),
    )
    return pipeline


def test_recognition_inference_failure_scrubs_ort_output(monkeypatch):
    logits = cp.ones((gpu_pipeline_module.MAX_BATCH, 2, 3), dtype=cp.float32)

    class Binding:
        @staticmethod
        def bind_output(*_args, **_kwargs):
            pass

        @staticmethod
        def synchronize_inputs():
            pass

        @staticmethod
        def synchronize_outputs():
            pass

        @staticmethod
        def get_outputs():
            return [object()]

    class Session:
        @staticmethod
        def get_outputs():
            return [types.SimpleNamespace(name="output")]

        @staticmethod
        def run_with_iobinding(_binding):
            raise RuntimeError("recognition inference failed")

    binding = Binding()
    pipeline = _recognition_failure_pipeline(Session(), lambda *_args, **_kwargs: None)
    monkeypatch.setattr(
        gpu_pipeline_module, "_bind_device_input", lambda _session, _batch: binding
    )
    monkeypatch.setattr(gpu_pipeline_module, "_cupy_view", lambda _output: logits)
    crop = np.ones((16, 16, 3), dtype=np.uint8)

    with pytest.raises(RuntimeError, match="recognition inference failed"):
        pipeline._recognize_crops([object()], [(0, crop)], {}, 0.0)

    assert not cp.any(logits)


def test_ctc_failure_scrubs_device_and_host_outputs(monkeypatch):
    logits = cp.ones((gpu_pipeline_module.MAX_BATCH, 2, 3), dtype=cp.float32)

    class Binding:
        @staticmethod
        def bind_output(*_args, **_kwargs):
            pass

        @staticmethod
        def synchronize_inputs():
            pass

        @staticmethod
        def synchronize_outputs():
            pass

        @staticmethod
        def get_outputs():
            return [object()]

    class Session:
        @staticmethod
        def get_outputs():
            return [types.SimpleNamespace(name="output")]

        @staticmethod
        def run_with_iobinding(_binding):
            pass

    def fail_decode(*_args, **_kwargs):
        raise RuntimeError("CTC decode failed")

    binding = Binding()
    pipeline = _recognition_failure_pipeline(Session(), fail_decode)
    monkeypatch.setattr(
        gpu_pipeline_module, "_bind_device_input", lambda _session, _batch: binding
    )
    monkeypatch.setattr(gpu_pipeline_module, "_cupy_view", lambda _output: logits)
    host_outputs = []
    cupy_asnumpy = cp.asnumpy

    def capture_host_output(value):
        output = cupy_asnumpy(value)
        host_outputs.append(output)
        return output

    monkeypatch.setattr(gpu_pipeline_module.cp, "asnumpy", capture_host_output)
    crop = np.ones((16, 16, 3), dtype=np.uint8)

    with pytest.raises(RuntimeError, match="CTC decode failed"):
        pipeline._recognize_crops([object()], [(0, crop)], {}, 0.0)

    token_ids, confidence = next(iter(pipeline._state()["ctc"].values()))
    assert not cp.any(logits)
    assert not cp.any(token_ids)
    assert not cp.any(confidence)
    assert len(host_outputs) == 2
    assert all(not np.any(value) for value in host_outputs)


def test_sensitive_pipeline_scratch_is_zeroed_without_releasing_capacity():
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    state = pipeline._state()
    state["crop_host"] = np.ones(16, dtype=np.uint8)
    state["crop_host_capacity"] = 16
    state["pinned"] = np.ones(16, dtype=np.uint8)
    state["frame"] = cp.ones(16, dtype=cp.uint8)
    state["det_inputs"][(1, 1)] = cp.ones(4, dtype=cp.float32)
    state["det_bindings"][(1, 1)] = (
        object(),
        cp.ones(4, dtype=cp.float32),
        np.ones(4, dtype=np.float32),
    )
    state["rec_inputs"][1] = cp.ones(4, dtype=cp.float32)
    state["rec_staging"][1] = np.ones(4, dtype=np.float32)
    state["ctc"][(1, 1)] = (
        cp.ones(4, dtype=cp.int32),
        cp.ones(4, dtype=cp.float32),
    )

    pipeline.scrub_sensitive_scratch()

    assert not np.any(state["crop_host"])
    assert not np.any(state["pinned"])
    assert not cp.any(state["frame"])
    assert not cp.any(state["det_inputs"][(1, 1)])
    assert not cp.any(state["det_bindings"][(1, 1)][1])
    assert not np.any(state["det_bindings"][(1, 1)][2])
    assert not cp.any(state["rec_inputs"][1])
    assert not np.any(state["rec_staging"][1])
    assert not cp.any(state["ctc"][(1, 1)][0])
    assert not cp.any(state["ctc"][(1, 1)][1])


def test_capacity_growth_scrubs_superseded_pipeline_buffers():
    pipeline = object.__new__(GpuOcrPipeline)
    pipeline._tls = threading.local()
    pipeline._upload(np.full((1, 1, 4), 255, dtype=np.uint8))
    state = pipeline._state()
    old_pinned = state["pinned"]
    old_device = state["frame"]
    old_crop = pipeline._crop_host_canvas(1, 1)
    old_crop.fill(255)

    pipeline._upload(np.full((2, 4, 4), 127, dtype=np.uint8))
    pipeline._crop_host_canvas(4, 8)

    assert not np.any(old_pinned)
    assert not cp.any(old_device)
    assert not np.any(old_crop)
    pipeline.scrub_sensitive_scratch()


def test_resident_frame_growth_scrubs_superseded_device_allocation():
    pipeline = StubPipeline()
    store = GpuResidentStore(pipeline, max_cache_entries=4)
    process(store, 0, 10, full_snapshot=True)
    old_frame = store._sessions["session"].frame
    larger = frame_request(20, width=8, height=2)

    store.process("session", 1, 8, 2, larger)

    assert not cp.any(old_frame)
    store.release("session", 2)

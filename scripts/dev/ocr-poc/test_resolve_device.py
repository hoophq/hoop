"""Unit tests for the device-selection policy (device_policy.py).

Run: python3 -m pytest test_resolve_device.py
"""

import pytest

from device_policy import resolve_device, resolve_worker_concurrency


def test_cpu_build_always_cpu():
    assert resolve_device(False, 0, False) == "onnxruntime-cpu"
    # Device count is irrelevant without the CUDA provider compiled in.
    assert resolve_device(False, 2, False) == "onnxruntime-cpu"


def test_gpu_build_with_device_uses_cuda():
    assert resolve_device(True, 1, False) == "onnxruntime-cuda"
    assert resolve_device(True, 4, True) == "onnxruntime-cuda"


def test_gpu_build_without_device_refuses_to_start():
    with pytest.raises(RuntimeError, match="no CUDA device is visible"):
        resolve_device(True, 0, False)


def test_gpu_build_without_device_with_explicit_fallback():
    assert resolve_device(True, 0, True) == "onnxruntime-cpu"



def test_resident_gpu_store_requires_one_process():
    assert resolve_worker_concurrency(None, resident_enabled=True) == 1
    assert resolve_worker_concurrency("1", resident_enabled=True) == 1
    with pytest.raises(RuntimeError, match="process-local"):
        resolve_worker_concurrency("4", resident_enabled=True)


def test_nonresident_server_allows_multiple_processes():
    assert resolve_worker_concurrency("4", resident_enabled=False) == 4


@pytest.mark.parametrize("value", ["0", "-1", "many"])
def test_worker_concurrency_must_be_positive_integer(value):
    with pytest.raises(RuntimeError, match="positive integer"):
        resolve_worker_concurrency(value, resident_enabled=False)
"""Unit tests for the device-selection policy (device_policy.py).

Run: python3 -m pytest test_resolve_device.py
"""

import pytest

from device_policy import resolve_device


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

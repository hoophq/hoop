"""Runtime device detection and selection policy for the OCR PoC servers.

Separate from server_rapidocr.py so the policy matrix is unit-testable
without CUDA hardware or the inference dependencies (the server module
constructs the OCR engine at import time and fails fast on misconfigured
deployments — by design, but hostile to test imports).
"""

import ctypes
import logging

logger = logging.getLogger("uvicorn.error")

# CUDA driver error code for "no CUDA-capable device": the normal cuInit
# result on a GPU-less host. Anything else non-zero is a runtime problem
# (driver/toolkit mismatch etc.) worth telling the operator about.
CUDA_ERROR_NO_DEVICE = 100


def cuda_device_count() -> int:
    """Count CUDA devices via the driver API. With nvidia-container-toolkit,
    libcuda.so.1 only exists inside the container when a GPU was passed
    (--gpus), so a missing library reliably means "no GPU here". Failure
    modes are classified in logs so a broken GPU runtime is not misread as
    a plain "no GPU" deployment."""
    try:
        cuda = ctypes.CDLL("libcuda.so.1")
    except OSError:
        logger.info("libcuda.so.1 not present; no GPU was injected into this container")
        return 0
    rc = cuda.cuInit(0)
    if rc != 0:
        if rc == CUDA_ERROR_NO_DEVICE:
            logger.info("cuInit: no CUDA-capable device")
        else:
            logger.warning(
                "cuInit(0) failed with CUDA error %d — driver/runtime problem, "
                "treating as no usable GPU", rc,
            )
        return 0
    count = ctypes.c_int(0)
    rc = cuda.cuDeviceGetCount(ctypes.byref(count))
    if rc != 0:
        logger.warning(
            "cuDeviceGetCount failed with CUDA error %d — treating as no usable GPU", rc
        )
        return 0
    return count.value


def resolve_device(cuda_compiled: bool, cuda_devices: int, allow_cpu_fallback: bool) -> str:
    """Pure device-selection policy.

    - CPU build: always CPU (no policy involved).
    - GPU build + visible device: CUDA.
    - GPU build + no device: refuse (deployment error) unless fallback is
      explicitly allowed — a GPU image silently running on CPU would burn
      CPU and quietly miss latency targets.
    """
    if not cuda_compiled:
        return "onnxruntime-cpu"
    if cuda_devices > 0:
        return "onnxruntime-cuda"
    if allow_cpu_fallback:
        logger.warning(
            "GPU-flavored image but no CUDA device is visible; "
            "OCR_ALLOW_CPU_FALLBACK=1 set — running on CPU (slower)"
        )
        return "onnxruntime-cpu"
    raise RuntimeError(
        "this is the GPU image but no CUDA device is visible (did you "
        "pass --gpus all / install nvidia-container-toolkit?). Use the "
        "CPU image instead, or set OCR_ALLOW_CPU_FALLBACK=1 to run "
        "degraded on purpose"
    )

# GPU OCR PoC for the RDP PII guard benchmark (see server.py).
#
# Build & run on a CUDA host (requires nvidia-container-toolkit):
#   docker build -t ocr-poc .
#   docker run -d --name ocr-poc --gpus all -p 8868:8868 ocr-poc
#   curl http://localhost:8868/healthz   # -> {"status":"ok","device":"gpu"}
#
# The base image bundles CUDA/cuDNN; the host only needs a recent NVIDIA
# driver. For a CPU-only comparison run, use base image
# paddlepaddle/paddle:2.6.2 and drop --gpus.
FROM paddlepaddle/paddle:2.6.2-gpu-cuda12.0-cudnn8.9-trt8.6

# numpy<2: the base image's paddle/opencv binaries are built against the
# numpy 1.x C ABI; letting a transitive dep pull numpy 2.x breaks cv2 import.
RUN pip install --no-cache-dir \
    "numpy<2" \
    paddleocr==2.7.3 \
    fastapi==0.115.6 \
    uvicorn==0.34.0

# Pre-download the PP-OCR english det+rec models at build time so the
# container starts serving without network access. use_gpu=False: no GPU is
# attached during docker build — this step only needs to fetch the models.
RUN python -c "from paddleocr import PaddleOCR; PaddleOCR(use_angle_cls=False, lang='en', show_log=False, use_gpu=False)"

COPY server.py /app/server.py
# Parallel inference workers: uvicorn forks $WEB_CONCURRENCY processes,
# each with its own model instance (the gateway analyzer issues up to 8
# concurrent chunk requests). Override at `docker run -e WEB_CONCURRENCY=N`.
ENV WEB_CONCURRENCY=4
EXPOSE 8868
CMD ["python", "-m", "uvicorn", "server:app", "--app-dir", "/app", "--host", "0.0.0.0", "--port", "8868"]

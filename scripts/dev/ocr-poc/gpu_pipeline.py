"""Lower-copy OCR execution for the raw and resident RGBA transports.

Pinned uploads feed CUDA detector preprocessing and ONNX Runtime I/O binding.
Resident sessions retain framebuffers, exact-chunk OCR results, and detected-line
recognition results. Line hashes and tight crop downloads avoid rerunning
recognition or copying a full framebuffer when surrounding pixels change.
"""

import collections
import math
import threading
import time

import cv2
import numpy as np
import onnxruntime

try:
    import cupy as cp
    import cupyx
except ImportError as exc:  # pragma: no cover - exercised by GPU image startup
    raise RuntimeError(
        "OCR_GPU_PREPROCESS=1 requires cupy-cuda12x in the GPU image"
    ) from exc

from bucket_rec import (
    DET_H_BUCKETS,
    DET_W_BUCKETS,
    MAX_BATCH,
    REC_H,
    filter_and_scale_boxes,
)
from rapidocr.utils.process_img import get_rotate_crop_image
from raw_image import FRAME_FULL_SNAPSHOT, RdpFrameRequest


class UnsupportedGpuShape(ValueError):
    """A frame lies outside the fixed detector bucket grid."""



class _CachedRecognition:
    __slots__ = ("_text", "confidence")

    def __init__(self, text, confidence):
        self._text = bytearray(text.encode("utf-8"))
        self.confidence = float(confidence)

    def materialize(self):
        return self._text.decode("utf-8"), self.confidence

    def wipe(self):
        self._text[:] = b"\0" * len(self._text)


class _CachedWords:
    __slots__ = ("_words",)

    def __init__(self, words):
        self._words = tuple(
            (
                bytearray(word["text"].encode("utf-8")),
                float(word["conf"]),
                int(word["x"]),
                int(word["y"]),
                int(word["w"]),
                int(word["h"]),
            )
            for word in words
        )

    def materialize(self):
        return tuple(
            {
                "text": text.decode("utf-8"),
                "conf": confidence,
                "x": x,
                "y": y,
                "w": width,
                "h": height,
            }
            for text, confidence, x, y, width, height in self._words
        )

    def wipe(self):
        for text, *_geometry in self._words:
            text[:] = b"\0" * len(text)


def _wipe_cache(cache):
    while cache:
        _key, value = cache.popitem(last=False)
        wipe = getattr(value, "wipe", None)
        if wipe is not None:
            wipe()

_DET_KERNEL = cp.RawKernel(
    r'''
extern "C" __device__ float area_sample(
    const unsigned char* rgba,
    int src_h,
    int src_w,
    int real_h,
    int real_w,
    int out_y,
    int out_x,
    int channel
) {
    if (src_h == real_h && src_w == real_w) {
        int src_channel = 2 - channel;
        return (float)rgba[(out_y * src_w + out_x) * 4 + src_channel];
    }

    float y0 = (float)out_y * (float)src_h / (float)real_h;
    float y1 = (float)(out_y + 1) * (float)src_h / (float)real_h;
    float x0 = (float)out_x * (float)src_w / (float)real_w;
    float x1 = (float)(out_x + 1) * (float)src_w / (float)real_w;
    int iy0 = (int)floorf(y0);
    int iy1 = (int)ceilf(y1);
    int ix0 = (int)floorf(x0);
    int ix1 = (int)ceilf(x1);
    float sum = 0.0f;
    float weight_sum = 0.0f;
    int src_channel = 2 - channel;
    for (int iy = iy0; iy < iy1; ++iy) {
        int sy = min(max(iy, 0), src_h - 1);
        float wy = fmaxf(0.0f, fminf(y1, (float)(iy + 1)) - fmaxf(y0, (float)iy));
        for (int ix = ix0; ix < ix1; ++ix) {
            int sx = min(max(ix, 0), src_w - 1);
            float wx = fmaxf(0.0f, fminf(x1, (float)(ix + 1)) - fmaxf(x0, (float)ix));
            float weight = wx * wy;
            sum += weight * (float)rgba[(sy * src_w + sx) * 4 + src_channel];
            weight_sum += weight;
        }
    }
    return weight_sum > 0.0f ? floorf(sum / weight_sum + 0.5f) : 0.0f;
}

extern "C" __global__ void det_preprocess(
    const unsigned char* rgba,
    int src_h,
    int src_w,
    float* output,
    int real_h,
    int real_w,
    int dst_h,
    int dst_w,
    double mean0,
    double mean1,
    double mean2,
    double std0,
    double std1,
    double std2
) {
    int pixel = blockDim.x * blockIdx.x + threadIdx.x;
    int pixels = dst_h * dst_w;
    if (pixel >= pixels) return;
    int y = pixel / dst_w;
    int x = pixel - y * dst_w;
    int sample_y = min(y, real_h - 1);
    int sample_x = min(x, real_w - 1);
    double means[3] = {mean0, mean1, mean2};
    double stds[3] = {std0, std1, std2};
    for (int c = 0; c < 3; ++c) {
        float value = area_sample(
            rgba, src_h, src_w, real_h, real_w, sample_y, sample_x, c
        );
        float scaled = value * (1.0f / 255.0f);
        output[c * pixels + pixel] = (float)(((double)scaled - means[c]) / stds[c]);
    }
}
''',
    "det_preprocess",
    options=("--std=c++11",),
)

_APPLY_PATCH_KERNEL = cp.RawKernel(
    r'''
extern "C" __global__ void apply_rgba_patch(
    unsigned char* frame,
    int frame_w,
    const unsigned char* patch,
    int patch_h,
    int patch_w,
    int dst_x,
    int dst_y,
    int* changed
) {
    int pixel = blockDim.x * blockIdx.x + threadIdx.x;
    int pixels = patch_h * patch_w;
    if (pixel >= pixels) return;
    int py = pixel / patch_w;
    int px = pixel - py * patch_w;
    int src = pixel * 4;
    int dst = ((dst_y + py) * frame_w + dst_x + px) * 4;
    bool differs = false;
    #pragma unroll
    for (int channel = 0; channel < 4; ++channel) {
        unsigned char value = patch[src + channel];
        differs |= frame[dst + channel] != value;
        frame[dst + channel] = value;
    }
    if (differs) atomicExch(changed, 1);
}
''',
    "apply_rgba_patch",
    options=("--std=c++11",),
)

_RDP_BITMAP_KERNEL_SOURCE = r'''
__device__ int read_u8(
    const unsigned char* source,
    int source_len,
    int* source_pos,
    unsigned int* value
) {
    if (*source_pos >= source_len) return 0;
    *value = source[*source_pos];
    *source_pos += 1;
    return 1;
}

__device__ int read_u16(
    const unsigned char* source,
    int source_len,
    int* source_pos,
    unsigned int* value
) {
    if (*source_pos > source_len - 2) return 0;
    *value = (unsigned int)source[*source_pos]
        | ((unsigned int)source[*source_pos + 1] << 8);
    *source_pos += 2;
    return 1;
}

__device__ int read_pixel(
    const unsigned char* source,
    int source_len,
    int* source_pos,
    int depth,
    unsigned int* value
) {
    if (*source_pos > source_len - depth) return 0;
    unsigned int pixel = (unsigned int)source[*source_pos];
    if (depth >= 2) {
        pixel |= (unsigned int)source[*source_pos + 1] << 8;
    }
    if (depth == 3) {
        pixel |= (unsigned int)source[*source_pos + 2] << 16;
    }
    *source_pos += depth;
    *value = pixel;
    return 1;
}

__device__ int write_pixel(
    unsigned int* output,
    int pixel_count,
    int* output_pos,
    unsigned int value
) {
    if (*output_pos >= pixel_count) return 0;
    output[*output_pos] = value;
    *output_pos += 1;
    return 1;
}

__device__ unsigned int pixel_above(
    const unsigned int* output,
    int output_pos,
    int width
) {
    return output_pos < width ? 0u : output[output_pos - width];
}

__device__ int write_fg_bg(
    unsigned int* output,
    int pixel_count,
    int* output_pos,
    int width,
    unsigned int mask,
    unsigned int foreground,
    int count
) {
    for (int bit = 0; bit < count; ++bit) {
        unsigned int above = pixel_above(output, *output_pos, width);
        unsigned int value = (mask & (1u << bit))
            ? (above ^ foreground)
            : above;
        if (!write_pixel(output, pixel_count, output_pos, value)) return 0;
    }
    return 1;
}

extern "C" __global__ void decode_rdp_rle(
    const unsigned char* source,
    int source_len,
    int width,
    int height,
    int bits_per_pixel,
    unsigned int* output,
    int* result
) {
    if (blockIdx.x != 0 || threadIdx.x != 0) return;
    int depth = bits_per_pixel / 8;
    if ((bits_per_pixel != 16 && bits_per_pixel != 24) || depth < 1) {
        result[0] = 1;
        return;
    }
    int pixel_count = width * height;
    int source_pos = 0;
    int output_pos = 0;
    unsigned int foreground = bits_per_pixel == 16 ? 0xffffu : 0xffffffu;
    int insert_foreground = 0;
    int first_line = 1;

    while (source_pos < source_len) {
        if (first_line && output_pos >= width) {
            first_line = 0;
            insert_foreground = 0;
        }

        unsigned int header_value = 0;
        if (!read_u8(source, source_len, &source_pos, &header_value)) {
            result[0] = 2;
            return;
        }
        unsigned char header = (unsigned char)header_value;
        int code;
        if ((header & 0xc0) != 0xc0) {
            code = header >> 5;
        } else if ((header & 0xf0) == 0xf0) {
            code = header;
        } else {
            code = header >> 4;
        }

        int run_length = 0;
        unsigned int length_value = 0;
        if (code == 0x02) {
            run_length = header & 0x1f;
            if (run_length == 0) {
                if (!read_u8(source, source_len, &source_pos, &length_value)) {
                    result[0] = 2;
                    return;
                }
                run_length = (int)length_value + 1;
            } else {
                run_length *= 8;
            }
        } else if (code == 0x0d) {
            run_length = header & 0x0f;
            if (run_length == 0) {
                if (!read_u8(source, source_len, &source_pos, &length_value)) {
                    result[0] = 2;
                    return;
                }
                run_length = (int)length_value + 1;
            } else {
                run_length *= 8;
            }
        } else if (
            code == 0x00 || code == 0x01 || code == 0x03 || code == 0x04
        ) {
            run_length = header & 0x1f;
            if (run_length == 0) {
                if (!read_u8(source, source_len, &source_pos, &length_value)) {
                    result[0] = 2;
                    return;
                }
                run_length = (int)length_value + 32;
            }
        } else if (code == 0x0c || code == 0x0e) {
            run_length = header & 0x0f;
            if (run_length == 0) {
                if (!read_u8(source, source_len, &source_pos, &length_value)) {
                    result[0] = 2;
                    return;
                }
                run_length = (int)length_value + 16;
            }
        } else if (
            code == 0xf0 || code == 0xf1 || code == 0xf2 || code == 0xf3
            || code == 0xf4 || code == 0xf6 || code == 0xf7 || code == 0xf8
        ) {
            if (!read_u16(source, source_len, &source_pos, &length_value)) {
                result[0] = 2;
                return;
            }
            run_length = (int)length_value;
            if (run_length == 0) {
                result[0] = 4;
                return;
            }
        }

        if (code == 0x00 || code == 0xf0) {
            if (output_pos > pixel_count - run_length) {
                result[0] = 3;
                return;
            }
            int remaining = run_length;
            if (insert_foreground) {
                unsigned int above = pixel_above(output, output_pos, width);
                if (!write_pixel(
                    output,
                    pixel_count,
                    &output_pos,
                    above ^ foreground
                )) {
                    result[0] = 3;
                    return;
                }
                remaining -= 1;
            }
            for (int index = 0; index < remaining; ++index) {
                unsigned int above = pixel_above(output, output_pos, width);
                if (!write_pixel(output, pixel_count, &output_pos, above)) {
                    result[0] = 3;
                    return;
                }
            }
            insert_foreground = 1;
            continue;
        }

        insert_foreground = 0;
        if (
            code == 0x01 || code == 0xf1 || code == 0x0c || code == 0xf6
        ) {
            if (source_pos > source_len - depth) {
                result[0] = 2;
                return;
            }
            if (code == 0x0c || code == 0xf6) {
                if (!read_pixel(
                    source,
                    source_len,
                    &source_pos,
                    depth,
                    &foreground
                )) {
                    result[0] = 2;
                    return;
                }
            }
            if (output_pos > pixel_count - run_length) {
                result[0] = 3;
                return;
            }
            for (int index = 0; index < run_length; ++index) {
                unsigned int above = pixel_above(output, output_pos, width);
                if (!write_pixel(
                    output,
                    pixel_count,
                    &output_pos,
                    above ^ foreground
                )) {
                    result[0] = 3;
                    return;
                }
            }
        } else if (code == 0x0e || code == 0xf8) {
            unsigned int pixel_a = 0;
            unsigned int pixel_b = 0;
            if (
                !read_pixel(source, source_len, &source_pos, depth, &pixel_a)
                || !read_pixel(source, source_len, &source_pos, depth, &pixel_b)
            ) {
                result[0] = 2;
                return;
            }
            if (run_length > (pixel_count - output_pos) / 2) {
                result[0] = 3;
                return;
            }
            for (int index = 0; index < run_length; ++index) {
                write_pixel(output, pixel_count, &output_pos, pixel_a);
                write_pixel(output, pixel_count, &output_pos, pixel_b);
            }
        } else if (code == 0x03 || code == 0xf3) {
            unsigned int pixel = 0;
            if (!read_pixel(
                source, source_len, &source_pos, depth, &pixel
            )) {
                result[0] = 2;
                return;
            }
            if (output_pos > pixel_count - run_length) {
                result[0] = 3;
                return;
            }
            for (int index = 0; index < run_length; ++index) {
                write_pixel(output, pixel_count, &output_pos, pixel);
            }
        } else if (
            code == 0x02 || code == 0xf2 || code == 0x0d || code == 0xf7
        ) {
            if (code == 0x0d || code == 0xf7) {
                if (!read_pixel(
                    source,
                    source_len,
                    &source_pos,
                    depth,
                    &foreground
                )) {
                    result[0] = 2;
                    return;
                }
            }
            int remaining = run_length;
            while (remaining > 0) {
                unsigned int mask = 0;
                if (!read_u8(source, source_len, &source_pos, &mask)) {
                    result[0] = 2;
                    return;
                }
                int count = remaining < 8 ? remaining : 8;
                if (output_pos > pixel_count - count) {
                    result[0] = 3;
                    return;
                }
                if (!write_fg_bg(
                    output,
                    pixel_count,
                    &output_pos,
                    width,
                    mask,
                    foreground,
                    count
                )) {
                    result[0] = 3;
                    return;
                }
                remaining -= count;
            }
        } else if (code == 0x04 || code == 0xf4) {
            if (output_pos > pixel_count - run_length) {
                result[0] = 3;
                return;
            }
            for (int index = 0; index < run_length; ++index) {
                unsigned int pixel = 0;
                if (!read_pixel(
                    source, source_len, &source_pos, depth, &pixel
                )) {
                    result[0] = 2;
                    return;
                }
                write_pixel(output, pixel_count, &output_pos, pixel);
            }
        } else if (code == 0xf9 || code == 0xfa) {
            unsigned int mask = code == 0xf9 ? 0x03u : 0x05u;
            if (output_pos > pixel_count - 8) {
                result[0] = 3;
                return;
            }
            if (!write_fg_bg(
                output,
                pixel_count,
                &output_pos,
                width,
                mask,
                foreground,
                8
            )) {
                result[0] = 3;
                return;
            }
        } else if (code == 0xfd || code == 0xfe) {
            unsigned int pixel = code == 0xfd
                ? (bits_per_pixel == 16 ? 0xffffu : 0xffffffu)
                : 0u;
            if (!write_pixel(output, pixel_count, &output_pos, pixel)) {
                result[0] = 3;
                return;
            }
        } else {
            result[0] = 5;
            return;
        }
    }
    if (output_pos != pixel_count) {
        result[0] = 6;
    }
}

extern "C" __global__ void unpack_rdp_bitmap(
    const unsigned char* source,
    int bits_per_pixel,
    int pixel_count,
    unsigned int* output
) {
    int pixel = blockDim.x * blockIdx.x + threadIdx.x;
    if (pixel >= pixel_count) return;
    int depth = bits_per_pixel / 8;
    int offset = pixel * depth;
    unsigned int value = (unsigned int)source[offset];
    if (depth >= 2) value |= (unsigned int)source[offset + 1] << 8;
    if (depth >= 3) value |= (unsigned int)source[offset + 2] << 16;
    if (depth == 4) value |= (unsigned int)source[offset + 3] << 24;
    output[pixel] = value;
}

extern "C" __global__ void composite_rdp_bitmap(
    const unsigned int* source,
    int bits_per_pixel,
    int patch_width,
    int patch_height,
    unsigned char* frame,
    int frame_width,
    int dst_x,
    int dst_y,
    int* result
) {
    if (result[0] != 0) return;
    int pixel = blockDim.x * blockIdx.x + threadIdx.x;
    int pixel_count = patch_width * patch_height;
    if (pixel >= pixel_count) return;
    unsigned int value = source[pixel];
    unsigned char red;
    unsigned char green;
    unsigned char blue;
    if (bits_per_pixel == 16) {
        unsigned int r = (value >> 11) & 0x1f;
        unsigned int g = (value >> 5) & 0x3f;
        unsigned int b = value & 0x1f;
        red = (unsigned char)((r << 3) | (r >> 2));
        green = (unsigned char)((g << 2) | (g >> 4));
        blue = (unsigned char)((b << 3) | (b >> 2));
    } else {
        blue = (unsigned char)(value & 0xff);
        green = (unsigned char)((value >> 8) & 0xff);
        red = (unsigned char)((value >> 16) & 0xff);
    }
    int source_y = pixel / patch_width;
    int source_x = pixel - source_y * patch_width;
    int target_y = dst_y + patch_height - 1 - source_y;
    int target = (target_y * frame_width + dst_x + source_x) * 4;
    int differs = frame[target] != red
        || frame[target + 1] != green
        || frame[target + 2] != blue
        || frame[target + 3] != 255;
    frame[target] = red;
    frame[target + 1] = green;
    frame[target + 2] = blue;
    frame[target + 3] = 255;
    if (differs) atomicExch(result + 1, 1);
}
'''

_RDP_RLE_KERNEL = cp.RawKernel(
    _RDP_BITMAP_KERNEL_SOURCE,
    "decode_rdp_rle",
    options=("--std=c++11",),
)
_RDP_UNPACK_KERNEL = cp.RawKernel(
    _RDP_BITMAP_KERNEL_SOURCE,
    "unpack_rdp_bitmap",
    options=("--std=c++11",),
)
_RDP_COMPOSITE_KERNEL = cp.RawKernel(
    _RDP_BITMAP_KERNEL_SOURCE,
    "composite_rdp_bitmap",
    options=("--std=c++11",),
)




_CTC_KERNEL = cp.RawKernel(
    r'''
extern "C" __global__ void ctc_reduce(
    const float* logits,
    int rows,
    int classes,
    int* token_ids,
    float* confidence
) {
    int row = blockDim.x * blockIdx.x + threadIdx.x;
    if (row >= rows) return;
    const float* values = logits + row * classes;
    int best_id = 0;
    float best = values[0];
    for (int c = 1; c < classes; ++c) {
        float value = values[c];
        if (value > best) {
            best = value;
            best_id = c;
        }
    }
    token_ids[row] = best_id;
    confidence[row] = best;
}
''',
    "ctc_reduce",
    options=("--std=c++11",),
)

_HASH_RECT_KERNEL = cp.RawKernel(
    r'''
__device__ __forceinline__ unsigned long long mix64(unsigned long long value) {
    value ^= value >> 30;
    value *= 0xbf58476d1ce4e5b9ULL;
    value ^= value >> 27;
    value *= 0x94d049bb133111ebULL;
    return value ^ (value >> 31);
}

extern "C" __global__ void hash_rgba_rect(
    const unsigned char* frame,
    int width,
    int x0,
    int y0,
    int x1,
    int y1,
    unsigned long long* output
) {
    __shared__ unsigned long long block_xor[256];
    __shared__ unsigned long long block_sum[256];
    int lane = threadIdx.x;
    int row_bytes = (x1 - x0) * 4;
    int byte_count = (y1 - y0) * row_bytes;
    int logical = blockIdx.x * blockDim.x + lane;
    int stride = blockDim.x * gridDim.x;
    unsigned long long local_xor = 0;
    unsigned long long local_sum = 0;
    for (; logical < byte_count; logical += stride) {
        int row = logical / row_bytes;
        int column_byte = logical - row * row_bytes;
        int source = (y0 + row) * width * 4 + x0 * 4 + column_byte;
        unsigned long long tagged =
            (((unsigned long long)logical) << 8) | frame[source];
        local_xor ^= mix64(tagged + 0x9e3779b97f4a7c15ULL);
        local_sum += mix64(tagged ^ 0xd6e8feb86659fd93ULL);
    }
    block_xor[lane] = local_xor;
    block_sum[lane] = local_sum;
    __syncthreads();
    for (int offset = blockDim.x / 2; offset > 0; offset >>= 1) {
        if (lane < offset) {
            block_xor[lane] ^= block_xor[lane + offset];
            block_sum[lane] += block_sum[lane + offset];
        }
        __syncthreads();
    }
    if (lane == 0) {
        atomicXor(output, block_xor[0]);
        atomicAdd(output + 1, block_sum[0]);
    }
}
''',
    "hash_rgba_rect",
    options=("--std=c++11",),
)


def _next_bucket(value, buckets):
    return next((bucket for bucket in buckets if value <= bucket), None)


def _elapsed(start, end):
    end.synchronize()
    return float(cp.cuda.get_elapsed_time(start, end))

def _hash_device_rect(frame, x0, y0, x1, y1, output):
    started = time.perf_counter()
    byte_count = (x1 - x0) * (y1 - y0) * 4
    blocks = min(256, (byte_count + 255) // 256)
    output.fill(0)
    _HASH_RECT_KERNEL(
        (blocks,),
        (256,),
        (
            frame,
            np.int32(frame.shape[1]),
            np.int32(x0),
            np.int32(y0),
            np.int32(x1),
            np.int32(y1),
            output,
        ),
    )
    values = cp.asnumpy(output)
    return (int(values[0]), int(values[1])), (
        time.perf_counter() - started
    ) * 1000.0


def _bind_device_input(session, value):
    binding = session.io_binding()
    binding.bind_input(
        name=session.get_inputs()[0].name,
        device_type="cuda",
        device_id=0,
        element_type=np.float32,
        shape=tuple(value.shape),
        buffer_ptr=value.data.ptr,
    )
    return binding


def _cupy_view(ort_value):
    shape = tuple(ort_value.shape())
    count = math.prod(shape)
    memory = cp.cuda.UnownedMemory(
        ort_value.data_ptr(), count * np.dtype(np.float32).itemsize, ort_value
    )
    pointer = cp.cuda.MemoryPointer(memory, 0)
    return cp.ndarray(shape, dtype=cp.float32, memptr=pointer)


class GpuOcrPipeline:
    """Per-process GPU path with reusable buffers scrubbed after each request."""

    def __init__(self, base_detector, bucket_det, bucket_rec, det_downscale, det_min_side):
        if not onnxruntime.get_device().upper().startswith("GPU"):
            raise RuntimeError("ONNX Runtime does not report a GPU device")
        if bucket_det is None or bucket_rec is None:
            raise RuntimeError(
                "GPU preprocessing requires fixed detector and fp16 recognition buckets"
            )
        self.base_detector = base_detector
        self.bucket_det = bucket_det
        self.bucket_rec = bucket_rec
        self.det_downscale = det_downscale
        self.det_min_side = det_min_side
        self._tls = threading.local()

    def _state(self):
        state = getattr(self._tls, "state", None)
        if state is None:
            state = {
                "capacity": 0,
                "pinned": None,
                "frame": None,
                "det_inputs": {},
                "det_bindings": {},
                "rec_inputs": {},
                "rec_staging": {},
                "ctc": {},
                "crop_host_capacity": 0,
                "crop_host": None,
                "rdp_pixels_capacity": 0,
                "rdp_pixels": None,
                "rdp_results_capacity": 0,
                "rdp_results": None,
                # All entries above are reusable allocation capacity only.
            }
            self._tls.state = state
        return state

    def scrub_sensitive_scratch(self):
        """Zeroes reusable host/device buffers before another session can run."""
        state = getattr(self._tls, "state", None)
        if state is None:
            return
        host_values = []
        device_values = []
        if state["pinned"] is not None:
            host_values.append(state["pinned"])
        if state["crop_host"] is not None:
            host_values.append(state["crop_host"])
        if state["rdp_pixels"] is not None:
            device_values.append(state["rdp_pixels"])
        if state["rdp_results"] is not None:
            device_values.append(state["rdp_results"])
        if state["frame"] is not None:
            device_values.append(state["frame"])
        host_values.extend(state["rec_staging"].values())
        device_values.extend(state["det_inputs"].values())
        device_values.extend(state["rec_inputs"].values())
        for _binding, output, staging in state["det_bindings"].values():
            device_values.append(output)
            host_values.append(staging)
        for token_ids, confidence in state["ctc"].values():
            device_values.extend((token_ids, confidence))
        for value in host_values:
            value.fill(0)
        for value in device_values:
            value.fill(0)
        cp.cuda.get_current_stream().synchronize()

    def _upload(self, rgba):
        state = self._state()
        count = int(rgba.size)
        if count > state["capacity"]:
            if state["pinned"] is not None:
                state["pinned"].fill(0)
            if state["frame"] is not None:
                state["frame"].fill(0)
                cp.cuda.get_current_stream().synchronize()
            capacity = 1 << (count - 1).bit_length()
            state["pinned"] = cupyx.empty_pinned(capacity, dtype=np.uint8)
            state["frame"] = cp.empty(capacity, dtype=cp.uint8)
            state["capacity"] = capacity
        np.copyto(state["pinned"][:count], rgba.reshape(-1))
        start = cp.cuda.Event()
        end = cp.cuda.Event()
        start.record()
        state["frame"].data.copy_from_host_async(
            state["pinned"].ctypes.data, count, cp.cuda.get_current_stream()
        )
        end.record()
        upload_ms = _elapsed(start, end)
        return state["frame"][:count].reshape(rgba.shape), upload_ms

    def _upload_bytes(self, body):
        state = self._state()
        count = len(body)
        if count > state["capacity"]:
            if state["pinned"] is not None:
                state["pinned"].fill(0)
            if state["frame"] is not None:
                state["frame"].fill(0)
                cp.cuda.get_current_stream().synchronize()
            capacity = 1 << (count - 1).bit_length()
            state["pinned"] = cupyx.empty_pinned(capacity, dtype=np.uint8)
            state["frame"] = cp.empty(capacity, dtype=cp.uint8)
            state["capacity"] = capacity
        np.copyto(state["pinned"][:count], np.frombuffer(body, dtype=np.uint8))
        start = cp.cuda.Event()
        end = cp.cuda.Event()
        start.record()
        state["frame"].data.copy_from_host_async(
            state["pinned"].ctypes.data, count, cp.cuda.get_current_stream()
        )
        end.record()
        return state["frame"][:count], _elapsed(start, end)

    def _rdp_buffers(self, pixel_count, patch_count):
        state = self._state()
        if pixel_count > state["rdp_pixels_capacity"]:
            if state["rdp_pixels"] is not None:
                state["rdp_pixels"].fill(0)
                cp.cuda.get_current_stream().synchronize()
            capacity = 1 << (pixel_count - 1).bit_length()
            state["rdp_pixels"] = cp.empty(capacity, dtype=cp.uint32)
            state["rdp_pixels_capacity"] = capacity
        result_count = patch_count * 2
        if result_count > state["rdp_results_capacity"]:
            if state["rdp_results"] is not None:
                state["rdp_results"].fill(0)
                cp.cuda.get_current_stream().synchronize()
            capacity = 1 << (result_count - 1).bit_length()
            state["rdp_results"] = cp.empty(capacity, dtype=cp.int32)
            state["rdp_results_capacity"] = capacity
        return (
            state["rdp_pixels"][:pixel_count],
            state["rdp_results"][:result_count].reshape((patch_count, 2)),
        )

    def _detector_geometry(self, src_h, src_w, allow_dynamic=False):
        scale = self.det_downscale
        if scale >= 1.0 or min(src_h, src_w) * scale < self.det_min_side:
            real_h, real_w, scale = src_h, src_w, 1.0
        else:
            real_h = max(1, int(src_h * scale))
            real_w = max(1, int(src_w * scale))
        bucket_h = _next_bucket(real_h, DET_H_BUCKETS)
        bucket_w = _next_bucket(real_w, DET_W_BUCKETS)
        if bucket_h is None or bucket_w is None:
            if not allow_dynamic:
                raise UnsupportedGpuShape(
                    f"detector input {real_w}x{real_h} exceeds fixed GPU buckets"
                )
            # The detector accepts dynamic shapes but its stride requires
            # dimensions divisible by 32. This path is correctness-first for
            # desktops outside the tuned bucket grid.
            bucket_h = ((real_h + 31) // 32) * 32
            bucket_w = ((real_w + 31) // 32) * 32
        return real_h, real_w, scale, bucket_h, bucket_w

    def _detector_input(self, frame, allow_dynamic=False):
        src_h, src_w = frame.shape[:2]
        real_h, real_w, scale, bucket_h, bucket_w = self._detector_geometry(
            src_h, src_w, allow_dynamic=allow_dynamic
        )

        state = self._state()
        key = (bucket_h, bucket_w)
        cacheable = bucket_h in DET_H_BUCKETS and bucket_w in DET_W_BUCKETS
        output = state["det_inputs"].get(key) if cacheable else None
        if output is None:
            output = cp.empty((1, 3, bucket_h, bucket_w), dtype=cp.float32)
            if cacheable:
                state["det_inputs"][key] = output
        try:
            mean = np.asarray(self.base_detector.mean, dtype=np.float64)
            std = np.asarray(self.base_detector.std, dtype=np.float64)
            pixels = bucket_h * bucket_w
            start = cp.cuda.Event()
            end = cp.cuda.Event()
            start.record()
            _DET_KERNEL(
                ((pixels + 255) // 256,),
                (256,),
                (
                    frame,
                    np.int32(src_h),
                    np.int32(src_w),
                    output,
                    np.int32(real_h),
                    np.int32(real_w),
                    np.int32(bucket_h),
                    np.int32(bucket_w),
                    mean[0],
                    mean[1],
                    mean[2],
                    std[0],
                    std[1],
                    std[2],
                ),
            )
            end.record()
            preprocess_ms = _elapsed(start, end)
            return output, (real_h, real_w), scale, preprocess_ms, cacheable
        except Exception:
            if cacheable and state["det_inputs"].get(key) is output:
                state["det_inputs"].pop(key)
            output.fill(0)
            cp.cuda.get_current_stream().synchronize()
            output = None
            cp.get_default_memory_pool().free_all_blocks()
            raise

    def _detector_binding(self, session, det_input, shape, cacheable):
        state = self._state()
        key = tuple(shape)
        cached = state["det_bindings"].get(key) if cacheable else None
        if cached is not None:
            return cached

        output = None
        staging = None
        binding = None
        try:
            output = cp.empty((1, 1, *shape), dtype=cp.float32)
            staging = cupyx.empty_pinned(output.shape, dtype=np.float32)
            binding = _bind_device_input(session, det_input)
            binding.bind_output(
                name=session.get_outputs()[0].name,
                device_type="cuda",
                device_id=0,
                element_type=np.float32,
                shape=output.shape,
                buffer_ptr=output.data.ptr,
            )
            cached = (binding, output, staging)
            if cacheable:
                state["det_bindings"][key] = cached
            return cached
        except Exception:
            if output is not None:
                output.fill(0)
            if staging is not None:
                staging.fill(0)
            cp.cuda.get_current_stream().synchronize()
            binding = output = staging = None
            cp.get_default_memory_pool().free_all_blocks()
            cp.get_default_pinned_memory_pool().free_all_blocks()
            raise


    def _detect(self, frame, timings, allow_dynamic=False):
        (
            det_input,
            (real_h, real_w),
            scale,
            preprocess_ms,
            cacheable,
        ) = self._detector_input(frame, allow_dynamic=allow_dynamic)
        timings["det_preprocess_ms"] = preprocess_ms
        shape = det_input.shape[2:]
        stream = cp.cuda.get_current_stream()
        binding = output = prediction = None
        try:
            detector = self.bucket_det.detector_for_shape(shape)
            session = detector.session.session
            binding, output, prediction = self._detector_binding(
                session, det_input, shape, cacheable
            )
            started = time.perf_counter()
            binding.synchronize_inputs()
            session.run_with_iobinding(binding)
            binding.synchronize_outputs()
            output.data.copy_to_host_async(
                prediction.ctypes.data, output.nbytes, stream
            )
            stream.synchronize()
            timings["det_infer_ms"] = (time.perf_counter() - started) * 1000.0

            started = time.perf_counter()
            boxes, _scores = detector.postprocess_op(prediction, shape)
            if boxes is None or len(boxes) < 1:
                timings["det_postprocess_ms"] = (
                    time.perf_counter() - started
                ) * 1000.0
                return None
            boxes = detector.sorted_boxes(boxes)
            boxes = filter_and_scale_boxes(boxes, real_h, real_w, 1.0 / scale)
            timings["det_postprocess_ms"] = (
                time.perf_counter() - started
            ) * 1000.0
            return boxes
        finally:
            if not cacheable:
                det_input.fill(0)
                if output is not None:
                    output.fill(0)
                if prediction is not None:
                    prediction.fill(0)
                stream.synchronize()
                binding = output = prediction = det_input = None
                cp.get_default_memory_pool().free_all_blocks()
                cp.get_default_pinned_memory_pool().free_all_blocks()

    def _rec_input(self, bucket_w):
        state = self._state()
        value = state["rec_inputs"].get(bucket_w)
        if value is None:
            value = cp.empty((MAX_BATCH, 3, REC_H, bucket_w), dtype=cp.float32)
            state["rec_inputs"][bucket_w] = value
        return value

    def _rec_staging(self, bucket_w):
        state = self._state()
        value = state["rec_staging"].get(bucket_w)
        if value is None:
            value = cupyx.empty_pinned(
                (MAX_BATCH, 3, REC_H, bucket_w), dtype=np.float32
            )
            state["rec_staging"][bucket_w] = value
        value.fill(0.0)
        return value

    def _ctc_buffers(self, bucket_w, steps):
        state = self._state()
        key = (bucket_w, steps)
        buffers = state["ctc"].get(key)
        if buffers is None:
            buffers = (
                cp.empty((MAX_BATCH, steps), dtype=cp.int32),
                cp.empty((MAX_BATCH, steps), dtype=cp.float32),
            )
            state["ctc"][key] = buffers
        return buffers

    def _recognize_crops(
        self,
        boxes,
        crops,
        timings,
        stage_started,
        crop_download_ms=0.0,
        line_hash_ms=0.0,
        texts=None,
        scores=None,
    ):
        texts = [""] * len(boxes) if texts is None else texts
        scores = [0.0] * len(boxes) if scores is None else scores
        groups = collections.defaultdict(list)
        for index, crop in crops:
            crop_h, crop_w = crop.shape[:2]
            if crop_h <= 0 or crop_w <= 0:
                continue
            scaled_w = round(crop_w * REC_H / float(crop_h))
            bucket_w = self.bucket_rec.bucket_for(scaled_w)
            groups[bucket_w].append((index, crop))

        upload_ms = 0.0
        infer_ms = 0.0
        ctc_ms = 0.0
        for bucket_w, entries in groups.items():
            session = self.bucket_rec.sessions[bucket_w]
            for offset in range(0, len(entries), MAX_BATCH):
                chunk = entries[offset : offset + MAX_BATCH]
                staging = self._rec_staging(bucket_w)
                for batch_index, (_index, crop) in enumerate(chunk):
                    staging[batch_index] = self.bucket_rec._preprocess(crop, bucket_w)

                batch = self._rec_input(bucket_w)
                upload_started = cp.cuda.Event()
                upload_finished = cp.cuda.Event()
                upload_started.record()
                batch.data.copy_from_host_async(
                    staging.ctypes.data,
                    staging.nbytes,
                    cp.cuda.get_current_stream(),
                )
                upload_finished.record()
                upload_ms += _elapsed(upload_started, upload_finished)

                binding = None
                output = None
                logits = None
                token_ids = None
                confidence = None
                ids_cpu = None
                confidence_cpu = None
                try:
                    binding = _bind_device_input(session, batch)
                    output_name = session.get_outputs()[0].name
                    binding.bind_output(output_name, "cuda", 0)
                    infer_started = time.perf_counter()
                    binding.synchronize_inputs()
                    session.run_with_iobinding(binding)
                    binding.synchronize_outputs()
                    output = binding.get_outputs()[0]
                    infer_ms += (time.perf_counter() - infer_started) * 1000.0

                    reduce_started = time.perf_counter()
                    logits = _cupy_view(output)
                    batch_size, steps, classes = logits.shape
                    token_ids, confidence = self._ctc_buffers(bucket_w, steps)
                    rows = batch_size * steps
                    _CTC_KERNEL(
                        ((rows + 255) // 256,),
                        (256,),
                        (
                            logits,
                            np.int32(rows),
                            np.int32(classes),
                            token_ids,
                            confidence,
                        ),
                    )
                    count = len(chunk)
                    ids_cpu = cp.asnumpy(token_ids[:count])
                    confidence_cpu = cp.asnumpy(confidence[:count])
                    decoded = self.bucket_rec.decode.decode(
                        ids_cpu,
                        confidence_cpu,
                        remove_duplicate=True,
                    )[0]
                    ctc_ms += (time.perf_counter() - reduce_started) * 1000.0
                    for row, (index, _crop) in enumerate(chunk):
                        texts[index] = decoded[row][0]
                        scores[index] = float(decoded[row][1])
                finally:
                    # ORT owns recognition outputs unless one is explicitly
                    # bound. Recover its view even after a failed inference so
                    # partially-written logits do not remain in the allocator.
                    if logits is None and binding is not None:
                        try:
                            outputs = binding.get_outputs()
                            if outputs:
                                output = outputs[0]
                                logits = _cupy_view(output)
                        except Exception:  # noqa: BLE001 - preserve inference error
                            output = None
                    if ids_cpu is not None:
                        ids_cpu.fill(0)
                    if confidence_cpu is not None:
                        confidence_cpu.fill(0)
                    dirty_device = False
                    if logits is not None:
                        logits.fill(0)
                        dirty_device = True
                    if token_ids is not None:
                        token_ids.fill(0)
                        dirty_device = True
                    if confidence is not None:
                        confidence.fill(0)
                        dirty_device = True
                    if dirty_device:
                        cp.cuda.get_current_stream().synchronize()
                    binding = output = logits = None
        timings["crop_rec_preprocess_ms"] = max(
            0.0,
            (time.perf_counter() - stage_started) * 1000.0
            - crop_download_ms
            - line_hash_ms
            - upload_ms
            - infer_ms
            - ctc_ms,
        )
        timings["crop_download_ms"] = crop_download_ms
        timings["line_hash_ms"] = line_hash_ms
        timings["rec_upload_ms"] = upload_ms
        timings["rec_infer_ms"] = infer_ms
        timings["ctc_reduce_decode_ms"] = ctc_ms
        return texts, scores

    def _recognize_host(self, rgba, boxes, timings):
        started = time.perf_counter()
        bgr = cv2.cvtColor(rgba, cv2.COLOR_RGBA2BGR)
        crops = []
        try:
            for index, box in enumerate(boxes):
                crop = get_rotate_crop_image(
                    bgr, np.asarray(box, dtype=np.float32)
                )
                crops.append((index, crop))
            return self._recognize_crops(boxes, crops, timings, started)
        finally:
            bgr.fill(0)
            for _index, crop in crops:
                crop.fill(0)

    def _crop_host_canvas(self, height, width):
        state = self._state()
        count = height * width * 3
        if count > state["crop_host_capacity"]:
            if state["crop_host"] is not None:
                state["crop_host"].fill(0)
            capacity = 1 << (count - 1).bit_length()
            state["crop_host"] = cupyx.empty_pinned(capacity, dtype=np.uint8)
            state["crop_host_capacity"] = capacity
        return state["crop_host"][:count].reshape((height, width, 3))

    def _recognize_device(
        self,
        frame,
        boxes,
        timings,
        cache=None,
        hash_output=None,
        max_cache_entries=0,
    ):
        """Hashes and downloads only tight detected-line rectangles."""
        if cache is not None and (hash_output is None or max_cache_entries < 1):
            raise ValueError("recognition cache requires hash output and a positive bound")
        started = time.perf_counter()
        frame_h, frame_w = frame.shape[:2]
        crops = []
        bgr_canvas = self._crop_host_canvas(frame_h, frame_w)
        sensitive_host = []
        device_crop_scrubbed = False
        texts = [""] * len(boxes)
        scores = [0.0] * len(boxes)
        miss_keys = {}
        cache_hits = 0
        cache_misses = 0
        download_ms = 0.0
        line_hash_ms = 0.0
        try:
            for index, box in enumerate(boxes):
                points = np.ascontiguousarray(box, dtype=np.float32)
                x0 = max(0, math.floor(float(points[:, 0].min())) - 8)
                y0 = max(0, math.floor(float(points[:, 1].min())) - 8)
                x1 = min(
                    frame_w,
                    math.ceil(float(points[:, 0].max())) + 9,
                )
                y1 = min(
                    frame_h,
                    math.ceil(float(points[:, 1].max())) + 9,
                )
                if x0 >= x1 or y0 >= y1:
                    continue
                if cache is not None:
                    pixel_hash, hash_ms = _hash_device_rect(
                        frame, x0, y0, x1, y1, hash_output
                    )
                    line_hash_ms += hash_ms
                    key = (
                        frame_w,
                        frame_h,
                        x0,
                        y0,
                        x1,
                        y1,
                        points.tobytes(),
                        pixel_hash,
                    )
                    cached = cache.pop(key, None)
                    if cached is not None:
                        cache[key] = cached
                        texts[index], scores[index] = cached.materialize()
                        cache_hits += 1
                        continue
                    miss_keys[index] = key
                    cache_misses += 1
                download_started = time.perf_counter()
                # Force an owned contiguous allocation: a full-width slice may
                # already be contiguous, and zeroing an ascontiguousarray view
                # would otherwise erase the resident framebuffer itself.
                device_crop = frame[y0:y1, x0:x1].copy(order="C")
                try:
                    rgba = cp.asnumpy(device_crop)
                finally:
                    device_crop.fill(0)
                    device_crop_scrubbed = True
                sensitive_host.append(rgba)
                download_ms += (time.perf_counter() - download_started) * 1000.0
                bgr = cv2.cvtColor(rgba, cv2.COLOR_RGBA2BGR)
                sensitive_host.append(bgr)
                bgr_canvas[y0:y1, x0:x1] = bgr
                crop = get_rotate_crop_image(bgr_canvas, points)
                sensitive_host.append(crop)
                crops.append((index, crop))
            texts, scores = self._recognize_crops(
                boxes,
                crops,
                timings,
                started,
                crop_download_ms=download_ms,
                line_hash_ms=line_hash_ms,
                texts=texts,
                scores=scores,
            )
            if cache is not None:
                for index, key in miss_keys.items():
                    cache[key] = _CachedRecognition(texts[index], scores[index])
                    while len(cache) > max_cache_entries:
                        _key, evicted = cache.popitem(last=False)
                        evicted.wipe()
            return texts, scores, cache_hits, cache_misses
        finally:
            if device_crop_scrubbed:
                cp.cuda.get_current_stream().synchronize()
            for value in sensitive_host:
                value.fill(0)


    @staticmethod
    def _words(boxes, texts, scores):
        words = []
        for index, box in enumerate(boxes):
            text = texts[index] if index < len(texts) else ""
            if not text:
                continue
            xs = [point[0] for point in box]
            ys = [point[1] for point in box]
            x, y = int(min(xs)), int(min(ys))
            words.append(
                {
                    "text": text,
                    "conf": scores[index] if index < len(scores) else 0.0,
                    "x": x,
                    "y": y,
                    "w": int(max(xs)) - x,
                    "h": int(max(ys)) - y,
                }
            )
        return words

    @staticmethod
    def _record_empty_recognition(timings):
        timings.update(
            {
                "crop_rec_preprocess_ms": 0.0,
                "crop_download_ms": 0.0,
                "rec_infer_ms": 0.0,
                "rec_upload_ms": 0.0,
                "ctc_reduce_decode_ms": 0.0,
            }
        )

    def __call__(self, rgba):
        # Reject unsupported shapes before `_upload` can ratchet persistent
        # pinned/device staging to the size of a CPU-fallback frame.
        self._detector_geometry(*rgba.shape[:2])
        total_started = time.perf_counter()
        timings = {}
        try:
            host_started = time.perf_counter()
            frame, upload_ms = self._upload(rgba)
            timings["host_stage_ms"] = (
                (time.perf_counter() - host_started) * 1000.0 - upload_ms
            )
            timings["upload_ms"] = upload_ms
            boxes = self._detect(frame, timings)
            if boxes:
                texts, scores = self._recognize_host(rgba, boxes, timings)
                words = self._words(boxes, texts, scores)
            else:
                self._record_empty_recognition(timings)
                words = []
            duration_ms = (time.perf_counter() - total_started) * 1000.0
            return {"duration_ms": duration_ms, "words": words, "stages": timings}
        finally:
            self.scrub_sensitive_scratch()

    def process_device(
        self,
        frame,
        recognition_cache=None,
        hash_output=None,
        max_recognition_cache_entries=0,
    ):
        """OCRs a top-down RGBA CuPy view without uploading its full pixels."""
        total_started = time.perf_counter()
        timings = {"host_stage_ms": 0.0, "upload_ms": 0.0}
        boxes = self._detect(frame, timings, allow_dynamic=True)
        if boxes:
            texts, scores, cache_hits, cache_misses = self._recognize_device(
                frame,
                boxes,
                timings,
                cache=recognition_cache,
                hash_output=hash_output,
                max_cache_entries=max_recognition_cache_entries,
            )
            words = self._words(boxes, texts, scores)
        else:
            self._record_empty_recognition(timings)
            timings["line_hash_ms"] = 0.0
            words = []
            cache_hits = 0
            cache_misses = 0
        duration_ms = (time.perf_counter() - total_started) * 1000.0
        return {
            "duration_ms": duration_ms,
            "words": words,
            "stages": timings,
            "rec_cache_hits": cache_hits,
            "rec_cache_misses": cache_misses,
        }


class ResidentSequenceError(RuntimeError):
    """A worker does not own the sequence expected by the agent."""

    def __init__(self, expected):
        super().__init__(f"expected resident frame sequence {expected}")
        self.expected = expected


class ResidentCapacityError(RuntimeError):
    """A framebuffer cannot fit inside the configured per-worker VRAM budget."""


class _ResidentFrame:
    def __init__(self, frame, width, height, now):
        self.frame = frame
        self.width = width
        self.height = height
        self.sequence = 0
        self.last_used = now
        self.ocr_cache = collections.OrderedDict()
        self.recognition_cache = collections.OrderedDict()
        self.hash_output = cp.empty(2, dtype=cp.uint64)

    @property
    def nbytes(self):
        return int(self.frame.nbytes)


class GpuResidentStore:
    """Bounded GPU framebuffers with per-session exact-pixel OCR result LRU."""

    def __init__(
        self,
        pipeline,
        max_sessions=8,
        max_bytes=512 << 20,
        ttl_seconds=300.0,
        max_cache_entries=128,
        max_recognition_cache_entries=512,
        clock=time.monotonic,
    ):
        if max_sessions < 1:
            raise ValueError("resident max_sessions must be positive")
        if max_bytes < 4:
            raise ValueError("resident max_bytes must be at least one pixel")
        if ttl_seconds <= 0:
            raise ValueError("resident ttl_seconds must be positive")
        if max_cache_entries < 1:
            raise ValueError("resident OCR cache entries must be positive")
        if max_recognition_cache_entries < 1:
            raise ValueError("resident recognition cache entries must be positive")
        self.pipeline = pipeline
        self.max_sessions = max_sessions
        self.max_bytes = max_bytes
        self.ttl_seconds = ttl_seconds
        self.max_cache_entries = max_cache_entries
        self.max_recognition_cache_entries = max_recognition_cache_entries
        self._clock = clock
        self._sessions = collections.OrderedDict()
        self._used_bytes = 0
        self._scratch_owner = None

    def _drop(self, session_key):
        state = self._sessions.pop(session_key, None)
        owns_scratch = self._scratch_owner == session_key
        if state is None:
            if owns_scratch:
                self.pipeline.scrub_sensitive_scratch()
                self._scratch_owner = None
            return
        self._used_bytes -= state.nbytes
        state.frame.fill(0)
        state.hash_output.fill(0)
        _wipe_cache(state.ocr_cache)
        _wipe_cache(state.recognition_cache)
        if owns_scratch:
            self.pipeline.scrub_sensitive_scratch()
            self._scratch_owner = None
        else:
            cp.cuda.get_current_stream().synchronize()
        del state
        cp.get_default_memory_pool().free_all_blocks()

    def _claim_scratch(self, session_key):
        if self._scratch_owner == session_key:
            return
        if self._scratch_owner is not None:
            self.pipeline.scrub_sensitive_scratch()
        self._scratch_owner = session_key

    def _evict_expired(self, now, keep=None):
        expired = [
            key
            for key, state in self._sessions.items()
            if key != keep and now - state.last_used >= self.ttl_seconds
        ]
        for key in expired:
            self._drop(key)

    def evict_expired(self):
        """Drops idle framebuffers; callers serialize this with process()."""
        self._evict_expired(self._clock())

    def _reserve(self, additional, keep=None, creating=False):
        if additional > self.max_bytes:
            raise ResidentCapacityError(
                f"resident framebuffer needs {additional} bytes, budget is {self.max_bytes}"
            )
        now = self._clock()
        self._evict_expired(now, keep=keep)
        while (
            self._used_bytes + additional > self.max_bytes
            or (creating and len(self._sessions) >= self.max_sessions)
        ):
            victim = next(
                (key for key in self._sessions if key != keep),
                None,
            )
            if victim is None:
                raise ResidentCapacityError(
                    "resident framebuffer budget is exhausted by the active session"
                )
            self._drop(victim)

    def _create(self, session_key, width, height):
        nbytes = width * height * 4
        self._reserve(nbytes, creating=True)
        frame = cp.zeros((height, width, 4), dtype=cp.uint8)
        state = _ResidentFrame(frame, width, height, self._clock())
        self._sessions[session_key] = state
        self._used_bytes += state.nbytes
        return state

    def _grow(self, session_key, state, width, height):
        if width < state.width or height < state.height:
            raise ValueError(
                "resident framebuffer dimensions cannot shrink without a full snapshot"
            )
        if width == state.width and height == state.height:
            return False
        new_bytes = width * height * 4
        self._reserve(new_bytes - state.nbytes, keep=session_key)
        frame = cp.zeros((height, width, 4), dtype=cp.uint8)
        old_frame = state.frame
        frame[: state.height, : state.width] = old_frame
        old_frame.fill(0)
        cp.cuda.get_current_stream().synchronize()
        self._used_bytes += new_bytes - state.nbytes
        state.frame = frame
        state.width = width
        state.height = height
        return True

    def _apply(self, state, patches):
        flags = cp.zeros(len(patches), dtype=cp.int32)
        upload_ms = 0.0
        started = time.perf_counter()
        for index, patch in enumerate(patches):
            host = np.frombuffer(patch.rgba, dtype=np.uint8).reshape(
                (patch.height, patch.width, 4)
            )
            device, patch_upload_ms = self.pipeline._upload(host)
            upload_ms += patch_upload_ms
            pixels = patch.width * patch.height
            _APPLY_PATCH_KERNEL(
                ((pixels + 255) // 256,),
                (256,),
                (
                    state.frame,
                    np.int32(state.width),
                    device,
                    np.int32(patch.height),
                    np.int32(patch.width),
                    np.int32(patch.x),
                    np.int32(patch.y),
                    flags[index : index + 1],
                ),
            )
        changed = bool(cp.asnumpy(flags).any())
        return changed, upload_ms, (time.perf_counter() - started) * 1000.0

    def _apply_rdp(self, state, request):
        patch_count = len(request.patches)
        max_pixels = max(
            patch.width * patch.height for patch in request.patches
        )
        decoded, results = self.pipeline._rdp_buffers(
            max_pixels, patch_count
        )
        results.fill(0)
        device_body, upload_ms = self.pipeline._upload_bytes(request.body)
        started = cp.cuda.Event()
        finished = cp.cuda.Event()
        started.record()
        for index, patch in enumerate(request.patches):
            pixel_count = patch.width * patch.height
            pixels = decoded[:pixel_count]
            source = device_body[
                patch.data_offset : patch.data_offset + len(patch.data)
            ]
            result = results[index]
            if patch.compressed:
                pixels.fill(0)
                _RDP_RLE_KERNEL(
                    (1,),
                    (1,),
                    (
                        source,
                        np.int32(len(patch.data)),
                        np.int32(patch.width),
                        np.int32(patch.height),
                        np.int32(patch.bits_per_pixel),
                        pixels,
                        result,
                    ),
                )
            else:
                _RDP_UNPACK_KERNEL(
                    ((pixel_count + 255) // 256,),
                    (256,),
                    (
                        source,
                        np.int32(patch.bits_per_pixel),
                        np.int32(pixel_count),
                        pixels,
                    ),
                )
            _RDP_COMPOSITE_KERNEL(
                ((pixel_count + 255) // 256,),
                (256,),
                (
                    pixels,
                    np.int32(patch.bits_per_pixel),
                    np.int32(patch.width),
                    np.int32(patch.height),
                    state.frame,
                    np.int32(state.width),
                    np.int32(patch.x),
                    np.int32(patch.y),
                    result,
                ),
            )
        finished.record()
        kernel_ms = _elapsed(started, finished)
        host_results = cp.asnumpy(results)
        failures = np.flatnonzero(host_results[:, 0])
        if failures.size:
            index = int(failures[0])
            code = int(host_results[index, 0])
            raise ValueError(
                f"invalid RDP bitmap patch {index}: CUDA decoder error {code}"
            )
        return bool(host_results[:, 1].any()), upload_ms, kernel_ms

    @staticmethod
    def _hash_chunk(state, chunk):
        return _hash_device_rect(
            state.frame,
            0,
            chunk.win_y0,
            state.width,
            chunk.win_y1,
            state.hash_output,
        )

    def _ocr_chunk(self, state, chunk):
        pixel_hash, hash_ms = self._hash_chunk(state, chunk)
        key = (
            state.width,
            chunk.win_y0,
            chunk.win_y1,
            chunk.own_y0,
            chunk.own_y1,
            pixel_hash,
        )
        cached = state.ocr_cache.pop(key, None)
        if cached is not None:
            state.ocr_cache[key] = cached
            return cached.materialize(), {}, hash_ms, True, 0, 0

        result = self.pipeline.process_device(
            state.frame[chunk.win_y0 : chunk.win_y1],
            recognition_cache=state.recognition_cache,
            hash_output=state.hash_output,
            max_recognition_cache_entries=self.max_recognition_cache_entries,
        )
        words = tuple(result["words"])
        state.ocr_cache[key] = _CachedWords(words)
        while len(state.ocr_cache) > self.max_cache_entries:
            _key, evicted = state.ocr_cache.popitem(last=False)
            evicted.wipe()
        return (
            words,
            result.get("stages", {}),
            hash_ms,
            False,
            result.get("rec_cache_hits", 0),
            result.get("rec_cache_misses", 0),
        )

    @staticmethod
    def _merge_stages(total, stages):
        for name, value in stages.items():
            total[name] = total.get(name, 0.0) + float(value)

    def process(self, session_key, sequence, width, height, request):
        # Expiry applies even to the requested session. An incremental request
        # after TTL then takes the normal 409/resync path instead of reviving
        # unredacted pixels or OCR text past their configured lifetime.
        self.evict_expired()
        full_snapshot = bool(request.flags & FRAME_FULL_SNAPSHOT)
        state = self._sessions.get(session_key)
        if full_snapshot:
            # Preserve the agent's globally monotonic sequence when state
            # moves between Uvicorn workers. Resetting to zero can make stale,
            # divergent worker framebuffers alias the same later sequence.
            self._drop(session_key)
            state = self._create(session_key, width, height)
            created = True
        elif state is None:
            if sequence != 0:
                raise ResidentSequenceError(0)
            state = self._create(session_key, width, height)
            created = True
        else:
            if sequence != state.sequence:
                raise ResidentSequenceError(state.sequence)
            created = False

        grew = self._grow(session_key, state, width, height)
        self._claim_scratch(session_key)
        total_started = time.perf_counter()
        try:
            if isinstance(request, RdpFrameRequest):
                pixels_changed, upload_ms, composite_ms = self._apply_rdp(
                    state, request
                )
                composite_stage = "frame_decode_composite_ms"
            else:
                pixels_changed, upload_ms, composite_ms = self._apply(
                    state, request.patches
                )
                composite_stage = "frame_composite_ms"
            frame_changed = created or grew or pixels_changed
            stages = {
                "frame_upload_ms": upload_ms,
                composite_stage: composite_ms,
            }
            words = []
            cache_hits = 0
            cache_misses = 0
            rec_cache_hits = 0
            rec_cache_misses = 0
            if frame_changed or request.chunks:
                for chunk in request.chunks:
                    (
                        chunk_words,
                        chunk_stages,
                        hash_ms,
                        cache_hit,
                        chunk_rec_hits,
                        chunk_rec_misses,
                    ) = self._ocr_chunk(state, chunk)
                    stages["frame_hash_ms"] = (
                        stages.get("frame_hash_ms", 0.0) + hash_ms
                    )
                    self._merge_stages(stages, chunk_stages)
                    if cache_hit:
                        cache_hits += 1
                    else:
                        cache_misses += 1
                    rec_cache_hits += chunk_rec_hits
                    rec_cache_misses += chunk_rec_misses
                    for word in chunk_words:
                        word = dict(word)
                        word["y"] += chunk.win_y0
                        center = word["y"] + word["h"] // 2
                        if chunk.own_y0 <= center < chunk.own_y1:
                            words.append(word)

            if sequence == (1 << 64) - 1:
                raise OverflowError("resident frame sequence overflow")
            state.sequence = sequence + 1
            state.last_used = self._clock()
            self._sessions.move_to_end(session_key)
            return {
                "duration_ms": (time.perf_counter() - total_started) * 1000.0,
                "words": words,
                "stages": stages,
                "frame_sequence": state.sequence,
                "frame_changed": frame_changed,
                "ocr_cache_hits": cache_hits,
                "ocr_cache_misses": cache_misses,
                "ocr_cache_entries": len(state.ocr_cache),
                "rec_cache_hits": rec_cache_hits,
                "rec_cache_misses": rec_cache_misses,
                "rec_cache_entries": len(state.recognition_cache),
            }
        except Exception:
            # The frame may already contain a prefix of this batch. Discard it
            # so no later retry can observe a sequence-consistent partial state.
            self._drop(session_key)
            raise

    def release(self, session_key, sequence):
        state = self._sessions.get(session_key)
        if state is None:
            return
        if sequence != state.sequence:
            raise ResidentSequenceError(state.sequence)
        self._drop(session_key)

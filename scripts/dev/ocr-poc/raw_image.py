"""Strict decoders for the agent's OCR transports.

Raw RGBA carries top-down RGBA8 pixels. Resident RDP carries original bitmap
patches and OCR chunk geometry so the sidecar can decode and composite them
directly into its GPU-resident framebuffer.
"""

from dataclasses import dataclass
import struct

import numpy as np

CONTENT_TYPE = "application/vnd.hoop.rgba8"
WIDTH_HEADER = "x-hoop-image-width"
HEIGHT_HEADER = "x-hoop-image-height"
MAX_DIM = 4096
CHANNELS = 4
FRAME_CONTENT_TYPE = "application/vnd.hoop.rgba-frame.v1"
FRAME_SESSION_HEADER = "x-hoop-frame-session"
FRAME_SEQUENCE_HEADER = "x-hoop-frame-sequence"
FRAME_MAGIC = b"HFR1"
FRAME_VERSION = 1
FRAME_FULL_SNAPSHOT = 1
MAX_FRAME_REQUEST_BYTES = 128 << 20
MAX_FRAME_PATCHES = 256
RDP_FRAME_CONTENT_TYPE = "application/vnd.hoop.rdp-frame.v2"
RDP_FRAME_MAGIC = b"HFR2"
RDP_FRAME_VERSION = 2
RDP_PATCH_COMPRESSED = 1
MAX_RDP_FRAME_PATCHES = 4096


@dataclass(frozen=True)
class FramePatch:
    x: int
    y: int
    width: int
    height: int
    rgba: memoryview


@dataclass(frozen=True)
class FrameChunk:
    win_y0: int
    win_y1: int
    own_y0: int
    own_y1: int


@dataclass(frozen=True)
class FrameRequest:
    flags: int
    patches: tuple[FramePatch, ...]
    chunks: tuple[FrameChunk, ...]


@dataclass(frozen=True)
class RdpFramePatch:
    x: int
    y: int
    width: int
    height: int
    bits_per_pixel: int
    compressed: bool
    data_offset: int
    data: memoryview


@dataclass(frozen=True)
class RdpFrameRequest:
    body: memoryview
    flags: int
    patches: tuple[RdpFramePatch, ...]
    chunks: tuple[FrameChunk, ...]



class RawImageError(ValueError):
    """The raw image request violates the wire contract."""


def validate_content_type(value: str | None) -> None:
    """Rejects requests that do not identify the raw RGBA wire format."""
    media_type = value.partition(";")[0].strip().lower() if value is not None else ""
    if media_type != CONTENT_TYPE:
        raise RawImageError(f"content-type must be {CONTENT_TYPE}")


def validate_frame_content_type(value: str | None) -> None:
    """Rejects requests that do not identify the resident-frame wire format."""
    media_type = value.partition(";")[0].strip().lower() if value is not None else ""
    if media_type != FRAME_CONTENT_TYPE:
        raise RawImageError(f"content-type must be {FRAME_CONTENT_TYPE}")


def validate_rdp_frame_content_type(value: str | None) -> None:
    """Rejects requests that do not identify the RDP bitmap wire format."""
    media_type = value.partition(";")[0].strip().lower() if value is not None else ""
    if media_type != RDP_FRAME_CONTENT_TYPE:
        raise RawImageError(f"content-type must be {RDP_FRAME_CONTENT_TYPE}")


def _dimension(value: str | None, name: str) -> int:
    if value is None:
        raise RawImageError(f"missing {name} header")
    try:
        parsed = int(value, 10)
    except ValueError as exc:
        raise RawImageError(f"invalid {name} header") from exc
    if not 1 <= parsed <= MAX_DIM:
        raise RawImageError(f"{name} must be in [1, {MAX_DIM}]")
    return parsed


def parse_geometry(
    width_header: str | None, height_header: str | None
) -> tuple[int, int]:
    return (
        _dimension(width_header, WIDTH_HEADER),
        _dimension(height_header, HEIGHT_HEADER),
    )


def rgba_body_length(width: int, height: int) -> int:
    return width * height * CHANNELS


async def read_exact_request_body(request, expected: int) -> bytearray:
    """Streams one exact-size request body with a hard allocation bound."""
    declared_header = request.headers.get("content-length")
    transfer_encoding = request.headers.get("transfer-encoding")
    if declared_header is not None and transfer_encoding is not None:
        raise RawImageError(
            "content-length and transfer-encoding cannot both be set"
        )

    if declared_header is not None:
        try:
            declared = int(declared_header, 10)
        except ValueError as exc:
            raise RawImageError("invalid content-length header") from exc
        if declared != expected:
            raise RawImageError(
                f"invalid RGBA body length: got {declared}, expected {expected}"
            )
        body = bytearray(expected)
    else:
        body = bytearray()

    offset = 0
    async for chunk in request.stream():
        end = offset + len(chunk)
        if end > expected:
            raise RawImageError(
                f"invalid RGBA body length: got at least {end}, expected {expected}"
            )
        if declared_header is None:
            body.extend(chunk)
        else:
            body[offset:end] = chunk
        offset = end

    if offset != expected:
        raise RawImageError(
            f"invalid RGBA body length: got {offset}, expected {expected}"
        )
    return body



async def read_bounded_request_body(
    request, limit: int = MAX_FRAME_REQUEST_BYTES
) -> bytearray:
    """Streams a variable-size request body without ever allocating over `limit`."""
    declared_header = request.headers.get("content-length")
    transfer_encoding = request.headers.get("transfer-encoding")
    if declared_header is not None and transfer_encoding is not None:
        raise RawImageError(
            "content-length and transfer-encoding cannot both be set"
        )

    if declared_header is not None:
        try:
            declared = int(declared_header, 10)
        except ValueError as exc:
            raise RawImageError("invalid content-length header") from exc
        if declared < 0:
            raise RawImageError("invalid content-length header")
        if declared > limit:
            raise RawImageError(f"frame body exceeds {limit} bytes")
        body = bytearray(declared)
    else:
        declared = None
        body = bytearray()

    offset = 0
    async for chunk in request.stream():
        end = offset + len(chunk)
        if end > limit:
            raise RawImageError(f"frame body exceeds {limit} bytes")
        if declared is not None:
            if end > declared:
                raise RawImageError(
                    f"frame body exceeds declared content-length {declared}"
                )
            body[offset:end] = chunk
        else:
            body.extend(chunk)
        offset = end

    if declared is not None and offset != declared:
        raise RawImageError(
            f"frame body length is {offset}, declared content-length is {declared}"
        )
    return body


def decode_frame_request(
    body: bytes | bytearray | memoryview, width: int, height: int
) -> FrameRequest:
    """Validates one resident-frame request and returns zero-copy patch views."""
    view = memoryview(body)
    header_size = struct.calcsize("<4sHHHH")
    if len(view) < header_size:
        raise RawImageError("resident frame body is shorter than its header")
    magic, version, flags, patch_count, chunk_count = struct.unpack_from(
        "<4sHHHH", view, 0
    )
    if magic != FRAME_MAGIC:
        raise RawImageError("invalid resident frame magic")
    if version != FRAME_VERSION:
        raise RawImageError(f"unsupported resident frame version {version}")
    if flags & ~FRAME_FULL_SNAPSHOT:
        raise RawImageError(f"invalid resident frame flags {flags:#x}")
    if patch_count == 0:
        raise RawImageError("resident frame must contain at least one patch")
    if patch_count > MAX_FRAME_PATCHES:
        raise RawImageError(
            f"resident frame patch count exceeds {MAX_FRAME_PATCHES}"
        )

    offset = header_size
    patches = []
    patch_header_size = struct.calcsize("<HHHHI")
    for _ in range(patch_count):
        if len(view) - offset < patch_header_size:
            raise RawImageError("truncated resident patch header")
        x, y, patch_width, patch_height, byte_count = struct.unpack_from(
            "<HHHHI", view, offset
        )
        offset += patch_header_size
        if patch_width == 0 or patch_height == 0:
            raise RawImageError("resident patch dimensions must be nonzero")
        if x + patch_width > width or y + patch_height > height:
            raise RawImageError("resident patch lies outside the framebuffer")
        expected = rgba_body_length(patch_width, patch_height)
        if byte_count != expected:
            raise RawImageError(
                f"invalid resident patch length: got {byte_count}, expected {expected}"
            )
        end = offset + byte_count
        if end > len(view):
            raise RawImageError("truncated resident patch pixels")
        patches.append(
            FramePatch(
                x=x,
                y=y,
                width=patch_width,
                height=patch_height,
                rgba=view[offset:end],
            )
        )
        offset = end

    chunks = []
    chunk_size = struct.calcsize("<HHHH")
    for _ in range(chunk_count):
        if len(view) - offset < chunk_size:
            raise RawImageError("truncated resident OCR chunk")
        win_y0, win_y1, own_y0, own_y1 = struct.unpack_from(
            "<HHHH", view, offset
        )
        offset += chunk_size
        if (
            win_y0 >= win_y1
            or win_y1 > height
            or own_y0 >= own_y1
            or own_y0 < win_y0
            or own_y1 > win_y1
        ):
            raise RawImageError("invalid resident OCR chunk geometry")
        chunks.append(
            FrameChunk(
                win_y0=win_y0,
                win_y1=win_y1,
                own_y0=own_y0,
                own_y1=own_y1,
            )
        )

    if offset != len(view):
        raise RawImageError("resident frame body has trailing bytes")
    if flags & FRAME_FULL_SNAPSHOT:
        patch = patches[0] if len(patches) == 1 else None
        if (
            patch is None
            or patch.x != 0
            or patch.y != 0
            or patch.width != width
            or patch.height != height
        ):
            raise RawImageError(
                "resident full snapshot must contain exactly the whole framebuffer"
            )
    return FrameRequest(
        flags=flags,
        patches=tuple(patches),
        chunks=tuple(chunks),
    )

def decode_rdp_frame_request(
    body: bytes | bytearray | memoryview, width: int, height: int
) -> RdpFrameRequest:
    """Validates an RDP bitmap update and returns zero-copy wire-pixel views."""
    view = memoryview(body)
    header_size = struct.calcsize("<4sHHHH")
    if len(view) < header_size:
        raise RawImageError("RDP frame body is shorter than its header")
    magic, version, flags, patch_count, chunk_count = struct.unpack_from(
        "<4sHHHH", view, 0
    )
    if magic != RDP_FRAME_MAGIC:
        raise RawImageError("invalid RDP frame magic")
    if version != RDP_FRAME_VERSION:
        raise RawImageError(f"unsupported RDP frame version {version}")
    if flags != 0:
        raise RawImageError(f"invalid RDP frame flags {flags:#x}")
    if patch_count == 0:
        raise RawImageError("RDP frame must contain at least one patch")
    if patch_count > MAX_RDP_FRAME_PATCHES:
        raise RawImageError(
            f"RDP frame patch count exceeds {MAX_RDP_FRAME_PATCHES}"
        )

    offset = header_size
    patches = []
    patch_header_size = struct.calcsize("<HHHHHHI")
    for _ in range(patch_count):
        if len(view) - offset < patch_header_size:
            raise RawImageError("truncated RDP patch header")
        (
            x,
            y,
            patch_width,
            patch_height,
            bits_per_pixel,
            patch_flags,
            byte_count,
        ) = struct.unpack_from("<HHHHHHI", view, offset)
        offset += patch_header_size
        if patch_width == 0 or patch_height == 0:
            raise RawImageError("RDP patch dimensions must be nonzero")
        if x + patch_width > width or y + patch_height > height:
            raise RawImageError("RDP patch lies outside the framebuffer")
        if bits_per_pixel not in (16, 24, 32):
            raise RawImageError(
                f"unsupported RDP patch depth {bits_per_pixel}"
            )
        if patch_flags & ~RDP_PATCH_COMPRESSED:
            raise RawImageError(f"invalid RDP patch flags {patch_flags:#x}")
        compressed = bool(patch_flags & RDP_PATCH_COMPRESSED)
        if compressed and bits_per_pixel == 32:
            raise RawImageError("compressed 32-bpp RDP patches are unsupported")
        if byte_count == 0:
            raise RawImageError("RDP patch pixels must be nonempty")
        if not compressed:
            expected = patch_width * patch_height * (bits_per_pixel // 8)
            if byte_count != expected:
                raise RawImageError(
                    f"invalid uncompressed RDP patch length: got {byte_count}, "
                    f"expected {expected}"
                )
        end = offset + byte_count
        if end > len(view):
            raise RawImageError("truncated RDP patch pixels")
        patches.append(
            RdpFramePatch(
                x=x,
                y=y,
                width=patch_width,
                height=patch_height,
                bits_per_pixel=bits_per_pixel,
                data_offset=offset,
                compressed=compressed,
                data=view[offset:end],
            )
        )
        offset = end

    chunks = []
    chunk_size = struct.calcsize("<HHHH")
    for _ in range(chunk_count):
        if len(view) - offset < chunk_size:
            raise RawImageError("truncated RDP OCR chunk")
        win_y0, win_y1, own_y0, own_y1 = struct.unpack_from(
            "<HHHH", view, offset
        )
        offset += chunk_size
        if (
            win_y0 >= win_y1
            or win_y1 > height
            or own_y0 >= own_y1
            or own_y0 < win_y0
            or own_y1 > win_y1
        ):
            raise RawImageError("invalid RDP OCR chunk geometry")
        chunks.append(
            FrameChunk(
                win_y0=win_y0,
                win_y1=win_y1,
                own_y0=own_y0,
                own_y1=own_y1,
            )
        )

    if offset != len(view):
        raise RawImageError("RDP frame body has trailing bytes")
    return RdpFrameRequest(
        body=view,
        flags=flags,
        patches=tuple(patches),
        chunks=tuple(chunks),
    )


def decode_rgba(
    body: bytes | bytearray | memoryview,
    width_header: str | None,
    height_header: str | None,
):
    """Returns a zero-copy ``H x W x 4`` uint8 view over an exact RGBA body."""
    width, height = parse_geometry(width_header, height_header)
    expected = rgba_body_length(width, height)
    if len(body) != expected:
        raise RawImageError(
            f"invalid RGBA body length: got {len(body)}, expected {expected}"
        )
    return np.frombuffer(body, dtype=np.uint8).reshape((height, width, CHANNELS))

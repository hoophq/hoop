import asyncio
import struct

import numpy as np
import pytest

from raw_image import (
    CONTENT_TYPE,
    FRAME_CONTENT_TYPE,
    FRAME_FULL_SNAPSHOT,
    FRAME_MAGIC,
    FRAME_VERSION,
    HEIGHT_HEADER,
    MAX_DIM,
    MAX_FRAME_PATCHES,
    MAX_RDP_FRAME_PATCHES,
    RDP_FRAME_CONTENT_TYPE,
    RDP_FRAME_MAGIC,
    RDP_FRAME_VERSION,
    RDP_PATCH_COMPRESSED,
    WIDTH_HEADER,
    RawImageError,
    decode_frame_request,
    decode_rdp_frame_request,
    decode_rgba,
    read_bounded_request_body,
    read_exact_request_body,
    validate_content_type,
    validate_frame_content_type,
    validate_rdp_frame_content_type,
)


class FakeRequest:
    def __init__(self, headers, chunks):
        self.headers = headers
        self._chunks = chunks
        self.streamed = False

    async def stream(self):
        self.streamed = True
        for chunk in self._chunks:
            yield chunk


def read_body(headers, chunks, expected=8):
    request = FakeRequest(headers, chunks)
    return request, asyncio.run(read_exact_request_body(request, expected))


def encode_frame(
    *,
    flags=0,
    patch=(0, 0, 2, 1, bytes(range(8))),
    chunk=(0, 1, 0, 1),
):
    x, y, width, height, pixels = patch
    return b"".join(
        (
            struct.pack("<4sHHHH", FRAME_MAGIC, FRAME_VERSION, flags, 1, 1),
            struct.pack("<HHHHI", x, y, width, height, len(pixels)),
            pixels,
            struct.pack("<HHHH", *chunk),
        )
    )


def encode_rdp_frame(
    *,
    flags=0,
    patch=(0, 0, 2, 1, 24, False, bytes(range(6))),
    chunk=(0, 1, 0, 1),
):
    x, y, width, height, bpp, compressed, pixels = patch
    patch_flags = RDP_PATCH_COMPRESSED if compressed else 0
    return b"".join(
        (
            struct.pack(
                "<4sHHHH",
                RDP_FRAME_MAGIC,
                RDP_FRAME_VERSION,
                flags,
                1,
                1,
            ),
            struct.pack(
                "<HHHHHHI",
                x,
                y,
                width,
                height,
                bpp,
                patch_flags,
                len(pixels),
            ),
            pixels,
            struct.pack("<HHHH", *chunk),
        )
    )


def test_decode_rgba_is_zero_copy_top_down_view():
    body = bytes(range(24))
    image = decode_rgba(body, "3", "2")

    assert image.shape == (2, 3, 4)
    assert image.dtype == np.uint8
    assert image[0, 0].tolist() == [0, 1, 2, 3]
    assert image[1, 0].tolist() == [12, 13, 14, 15]
    assert image.base is not None


def test_decode_rgba_accepts_streamed_bytearray_without_copy():
    body = bytearray(range(8))
    image = decode_rgba(body, "2", "1")

    image[0, 0, 0] = 42
    assert body[0] == 42


@pytest.mark.parametrize(
    ("width", "height", "message"),
    [
        (None, "1", f"missing {WIDTH_HEADER} header"),
        ("1", None, f"missing {HEIGHT_HEADER} header"),
        ("abc", "1", f"invalid {WIDTH_HEADER} header"),
        ("0", "1", f"{WIDTH_HEADER} must be in [1, {MAX_DIM}]"),
        (str(MAX_DIM + 1), "1", f"{WIDTH_HEADER} must be in [1, {MAX_DIM}]"),
    ],
)
def test_decode_rgba_rejects_invalid_geometry(width, height, message):
    with pytest.raises(RawImageError, match=rf"^{message.replace('[', r'\[').replace(']', r'\]')}$"):
        decode_rgba(b"", width, height)


def test_decode_rgba_requires_exact_body_length():
    with pytest.raises(RawImageError, match="got 7, expected 8"):
        decode_rgba(b"1234567", "2", "1")
    with pytest.raises(RawImageError, match="got 9, expected 8"):
        decode_rgba(b"123456789", "2", "1")


@pytest.mark.parametrize(
    "content_type",
    [
        CONTENT_TYPE,
        CONTENT_TYPE.upper(),
        f"{CONTENT_TYPE}; charset=binary",
    ],
)
def test_raw_content_type_accepts_declared_media_type(content_type):
    validate_content_type(content_type)


@pytest.mark.parametrize("content_type", [None, "", "text/plain", "image/bmp"])
def test_raw_content_type_rejects_missing_or_wrong_media_type(content_type):
    with pytest.raises(
        RawImageError, match=rf"^content-type must be {CONTENT_TYPE}$"
    ):
        validate_content_type(content_type)


def test_raw_body_streams_exact_declared_length():
    request, body = read_body({"content-length": "8"}, [b"123", b"45678"])

    assert request.streamed
    assert body == b"12345678"


def test_raw_body_rejects_excess_despite_matching_content_length():
    request = FakeRequest({"content-length": "8"}, [b"123456789"])

    with pytest.raises(RawImageError, match="got at least 9, expected 8"):
        asyncio.run(read_exact_request_body(request, 8))


def test_raw_body_rejects_mixed_length_and_transfer_encoding_before_read():
    request = FakeRequest(
        {"content-length": "8", "transfer-encoding": "chunked"},
        [b"12345678"],
    )

    with pytest.raises(
        RawImageError,
        match="content-length and transfer-encoding cannot both be set",
    ):
        asyncio.run(read_exact_request_body(request, 8))
    assert not request.streamed


def test_raw_body_streams_exact_unknown_length():
    request, body = read_body({}, [b"12", b"345678"])

    assert request.streamed
    assert body == b"12345678"


@pytest.mark.parametrize(
    ("chunks", "message"),
    [
        ([b"1234567"], "got 7, expected 8"),
        ([b"1234", b"56789"], "got at least 9, expected 8"),
    ],
)
def test_raw_body_rejects_invalid_unknown_length(chunks, message):
    with pytest.raises(RawImageError, match=message):
        asyncio.run(read_exact_request_body(FakeRequest({}, chunks), 8))



def test_decode_frame_request_returns_zero_copy_patch_and_chunk_geometry():
    body = bytearray(encode_frame())
    frame = decode_frame_request(body, 2, 1)

    assert frame.flags == 0
    assert len(frame.patches) == 1
    assert frame.patches[0].rgba.tolist() == list(range(8))
    assert frame.chunks[0].win_y0 == 0
    assert frame.chunks[0].own_y1 == 1
    frame.patches[0].rgba[0] = 42
    assert 42 in body




def test_decode_frame_request_accepts_update_without_ocr_chunks():
    pixels = bytes(range(8))
    body = b"".join(
        (
            struct.pack("<4sHHHH", FRAME_MAGIC, FRAME_VERSION, 0, 1, 0),
            struct.pack("<HHHHI", 0, 0, 2, 1, len(pixels)),
            pixels,
        )
    )

    frame = decode_frame_request(body, 2, 1)

    assert len(frame.patches) == 1
    assert frame.chunks == ()

@pytest.mark.parametrize(
    ("body", "message"),
    [
        (b"", "shorter than its header"),
        (
            struct.pack("<4sHHHH", b"BAD!", FRAME_VERSION, 0, 1, 1),
            "invalid resident frame magic",
        ),
        (
            struct.pack("<4sHHHH", FRAME_MAGIC, FRAME_VERSION + 1, 0, 1, 1),
            "unsupported resident frame version",
        ),
        (
            struct.pack("<4sHHHH", FRAME_MAGIC, FRAME_VERSION, 2, 1, 1),
            "invalid resident frame flags",
        ),
        (encode_frame() + b"x", "trailing bytes"),
        (
            encode_frame(patch=(1, 0, 2, 1, bytes(range(8)))),
            "outside the framebuffer",
        ),
        (
            encode_frame(chunk=(0, 1, 1, 1)),
            "invalid resident OCR chunk geometry",
        ),
    ],
)
def test_decode_frame_request_rejects_invalid_wire_data(body, message):
    with pytest.raises(RawImageError, match=message):
        decode_frame_request(body, 2, 1)

def test_decode_frame_request_rejects_excess_patch_work_before_pixels():
    body = struct.pack(
        "<4sHHHH",
        FRAME_MAGIC,
        FRAME_VERSION,
        0,
        MAX_FRAME_PATCHES + 1,
        0,
    )

    with pytest.raises(
        RawImageError,
        match=rf"^resident frame patch count exceeds {MAX_FRAME_PATCHES}$",
    ):
        decode_frame_request(body, 1, 1)


def test_decode_frame_request_enforces_full_snapshot_shape():
    valid = encode_frame(flags=FRAME_FULL_SNAPSHOT)
    assert decode_frame_request(valid, 2, 1).flags == FRAME_FULL_SNAPSHOT

    partial = encode_frame(
        flags=FRAME_FULL_SNAPSHOT,
        patch=(0, 0, 1, 1, bytes(range(4))),
    )
    with pytest.raises(RawImageError, match="exactly the whole framebuffer"):
        decode_frame_request(partial, 2, 1)


@pytest.mark.parametrize(
    "content_type",
    [FRAME_CONTENT_TYPE, FRAME_CONTENT_TYPE.upper(), f"{FRAME_CONTENT_TYPE}; v=1"],
)
def test_frame_content_type_accepts_declared_media_type(content_type):
    validate_frame_content_type(content_type)


def test_decode_rdp_frame_returns_zero_copy_compressed_patch():
    body = bytearray(
        encode_rdp_frame(
            patch=(0, 0, 2, 1, 24, True, b"\xf4\x02\x00abcdef")
        )
    )

    frame = decode_rdp_frame_request(body, 2, 1)

    assert frame.flags == 0
    assert len(frame.patches) == 1
    assert frame.patches[0].compressed
    assert frame.patches[0].bits_per_pixel == 24
    assert frame.patches[0].data_offset == struct.calcsize(
        "<4sHHHH"
    ) + struct.calcsize("<HHHHHHI")
    assert frame.body[frame.patches[0].data_offset] == 0xF4
    assert frame.patches[0].data.tolist() == list(b"\xf4\x02\x00abcdef")
    assert frame.chunks[0].own_y1 == 1
    frame.patches[0].data[0] = 42
    assert 42 in body


@pytest.mark.parametrize(
    ("bpp", "pixels"),
    [
        (16, bytes(range(4))),
        (24, bytes(range(6))),
        (32, bytes(range(8))),
    ],
)
def test_decode_rdp_frame_accepts_exact_uncompressed_depths(bpp, pixels):
    body = encode_rdp_frame(
        patch=(0, 0, 2, 1, bpp, False, pixels)
    )

    frame = decode_rdp_frame_request(body, 2, 1)

    assert frame.patches[0].bits_per_pixel == bpp
    assert not frame.patches[0].compressed


@pytest.mark.parametrize(
    ("body", "message"),
    [
        (b"", "shorter than its header"),
        (
            encode_rdp_frame(flags=1),
            "invalid RDP frame flags",
        ),
        (
            encode_rdp_frame(
                patch=(0, 0, 2, 1, 32, True, b"compressed")
            ),
            "compressed 32-bpp RDP patches are unsupported",
        ),
        (
            encode_rdp_frame(
                patch=(0, 0, 2, 1, 8, False, bytes((0, 1)))
            ),
            "unsupported RDP patch depth",
        ),
        (
            encode_rdp_frame(
                patch=(0, 0, 2, 1, 24, False, bytes(range(5)))
            ),
            "invalid uncompressed RDP patch length",
        ),
        (
            encode_rdp_frame(
                patch=(1, 0, 2, 1, 24, False, bytes(range(6)))
            ),
            "outside the framebuffer",
        ),
        (
            encode_rdp_frame(chunk=(0, 1, 1, 1)),
            "invalid RDP OCR chunk geometry",
        ),
        (encode_rdp_frame() + b"x", "trailing bytes"),
    ],
)
def test_decode_rdp_frame_rejects_invalid_wire_data(body, message):
    with pytest.raises(RawImageError, match=message):
        decode_rdp_frame_request(body, 2, 1)


def test_decode_rdp_frame_rejects_excess_patch_work():
    body = struct.pack(
        "<4sHHHH",
        RDP_FRAME_MAGIC,
        RDP_FRAME_VERSION,
        0,
        MAX_RDP_FRAME_PATCHES + 1,
        0,
    )

    with pytest.raises(
        RawImageError,
        match=rf"^RDP frame patch count exceeds {MAX_RDP_FRAME_PATCHES}$",
    ):
        decode_rdp_frame_request(body, 1, 1)


@pytest.mark.parametrize(
    "content_type",
    [
        RDP_FRAME_CONTENT_TYPE,
        RDP_FRAME_CONTENT_TYPE.upper(),
        f"{RDP_FRAME_CONTENT_TYPE}; v=2",
    ],
)
def test_rdp_frame_content_type_accepts_declared_media_type(content_type):
    validate_rdp_frame_content_type(content_type)


def test_bounded_body_rejects_declared_and_streamed_overflow():
    declared = FakeRequest({"content-length": "9"}, [])
    with pytest.raises(RawImageError, match="exceeds 8 bytes"):
        asyncio.run(read_bounded_request_body(declared, limit=8))
    assert not declared.streamed

    streamed = FakeRequest({}, [b"1234", b"56789"])
    with pytest.raises(RawImageError, match="exceeds 8 bytes"):
        asyncio.run(read_bounded_request_body(streamed, limit=8))
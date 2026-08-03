#!/usr/bin/env python3
"""Verify one downloaded artifact against the reviewed agent-tools manifest."""

from __future__ import annotations

import argparse
import hashlib
import hmac
import re
import sys
from pathlib import Path

SHA256_RE = re.compile(r"[0-9a-f]{64}")


class ManifestError(RuntimeError):
    """Raised when the checksum manifest is malformed or incomplete."""


def load_manifest(path: Path) -> dict[str, str]:
    entries: dict[str, str] = {}
    for line_number, raw_line in enumerate(path.read_text().splitlines(), 1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        fields = line.split(maxsplit=1)
        if len(fields) != 2 or not SHA256_RE.fullmatch(fields[0]):
            raise ManifestError(f"{path}:{line_number}: invalid SHA-256 entry")
        digest, name = fields
        name = name.lstrip("*").strip()
        if not name or "/" in name or name in entries:
            raise ManifestError(f"{path}:{line_number}: invalid or duplicate artifact name")
        entries[name] = digest
    if not entries:
        raise ManifestError(f"{path}: checksum manifest is empty")
    return entries


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as artifact:
        for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("artifact", type=Path)
    parser.add_argument(
        "--name",
        help="manifest key when it differs from the downloaded file's basename",
    )
    args = parser.parse_args()

    try:
        entries = load_manifest(args.manifest)
        name = args.name or args.artifact.name
        expected = entries.get(name)
        if expected is None:
            raise ManifestError(f"{name!r} has no reviewed SHA-256 entry in {args.manifest}")
        actual = sha256(args.artifact)
        if not hmac.compare_digest(actual, expected):
            raise ManifestError(
                f"SHA-256 mismatch for {name}: expected {expected}, got {actual}"
            )
    except (ManifestError, OSError) as exc:
        print(f"download verification failed: {exc}", file=sys.stderr)
        return 1

    print(f"{name}: SHA-256 verified")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

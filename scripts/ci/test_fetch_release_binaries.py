#!/usr/bin/env python3
"""Tests for the verified release-input fetch used by the weekly slim-agent rebuild.\n\nfetch-release-binaries.py is a fail-closed supply-chain control: it decides\nwhether bytes downloaded from the release endpoint are allowed to become a\npublished image. The cases that matter are the negative ones - a wrong digest,\na release with no trust anchor - each of which must refuse to produce an\narchive at all."""

from __future__ import annotations

import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest import mock

SCRIPTS = Path(__file__).resolve().parent


def load(filename: str, module_name: str):
    spec = importlib.util.spec_from_file_location(module_name, SCRIPTS / filename)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"could not load {filename}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


fetch = load("fetch-release-binaries.py", "fetch_release_binaries")


class ReleaseFetchTests(unittest.TestCase):
    def setUp(self):
        self.directory = Path(tempfile.mkdtemp())
        self.payload = b"release-archive-bytes"
        self.digest = hashlib.sha256(self.payload).hexdigest()

    def fake_download(self, url, destination):
        staging = destination.with_suffix(destination.suffix + ".part")
        staging.parent.mkdir(parents=True, exist_ok=True)
        staging.write_bytes(self.payload)
        return hashlib.sha256(self.payload).hexdigest()

    def test_github_asset_digest_is_preferred_over_a_pin(self):
        assets = {"hoop_9.9.9_Linux_x86_64.tar.gz": (self.digest, "https://gh/asset")}
        pins = {"hoop_9.9.9_Linux_x86_64.tar.gz": "f" * 64}
        with mock.patch.object(fetch, "download", self.fake_download):
            result = fetch.fetch_archive(
                "9.9.9", "amd64", "x86_64", assets, pins, self.directory
            )
        self.assertEqual(result["source"], "github-release-asset")
        # Landed under the GOARCH name Dockerfile.agent globs.
        self.assertTrue((self.directory / "hoop_9.9.9_Linux_amd64.tar.gz").exists())

    def test_reviewed_pin_is_used_when_no_asset_exists(self):
        pins = {"hoop_9.9.9_Linux_aarch64.tar.gz": self.digest}
        with mock.patch.object(fetch, "download", self.fake_download):
            result = fetch.fetch_archive(
                "9.9.9", "arm64", "aarch64", {}, pins, self.directory
            )
        self.assertEqual(result["source"], "reviewed-pin")
        self.assertEqual(result["sha256"], self.digest)

    def test_digest_mismatch_raises_and_leaves_no_archive(self):
        pins = {"hoop_9.9.9_Linux_x86_64.tar.gz": "a" * 64}
        with mock.patch.object(fetch, "download", self.fake_download):
            with self.assertRaises(fetch.VerificationError) as caught:
                fetch.fetch_archive("9.9.9", "amd64", "x86_64", {}, pins, self.directory)
        self.assertIn("SHA-256 mismatch", str(caught.exception))
        self.assertEqual(list(self.directory.glob("*.tar.gz")), [])
        self.assertEqual(list(self.directory.glob("*.part")), [])

    def test_release_with_no_anchor_is_refused(self):
        with mock.patch.object(fetch, "download", self.fake_download):
            with self.assertRaises(fetch.VerificationError) as caught:
                fetch.fetch_archive("9.9.9", "amd64", "x86_64", {}, {}, self.directory)
        self.assertIn("neither a GitHub release asset digest nor a reviewed pin", str(caught.exception))
        self.assertEqual(list(self.directory.glob("*")), [])

    def test_malformed_asset_digest_is_an_error_not_a_downgrade(self):
        release = {"assets": [{"name": "a.tar.gz", "digest": "sha256:zz", "browser_download_url": "u"}]}
        with mock.patch.object(fetch, "http_get", mock.mock_open(read_data=b"")):
            with mock.patch.object(fetch.json, "load", return_value=release):
                with self.assertRaises(fetch.VerificationError):
                    fetch.github_asset_digests("9.9.9")

    def test_manifest_rejects_a_malformed_digest_line(self):
        path = self.directory / "pins.sha256"
        path.write_text("not-a-digest  hoop_1.0.0_Linux_x86_64.tar.gz\n")
        with self.assertRaises(fetch.VerificationError):
            fetch.load_pins(path)

    def test_manifest_rejects_duplicate_entries(self):
        path = self.directory / "dupe.sha256"
        path.write_text(f"{self.digest}  a.tar.gz\n{'b' * 64}  a.tar.gz\n")
        with self.assertRaises(fetch.VerificationError):
            fetch.load_pins(path)

    def test_partial_download_leaves_nothing_behind(self):
        """A stream that dies mid-transfer must not leave a usable archive."""

        class DyingResponse:
            def __enter__(self):
                return self

            def __exit__(self, *exc):
                return False

            def read(self, _size):
                raise OSError("connection reset")

        destination = self.directory / "hoop_9.9.9_Linux_amd64.tar.gz"
        with mock.patch.object(fetch, "http_get", lambda *a, **k: DyingResponse()):
            with self.assertRaises(OSError):
                fetch.download("https://example.invalid/a.tar.gz", destination)
        self.assertFalse(destination.exists())
        self.assertEqual(list(self.directory.glob("*.part")), [])

    def test_download_writes_to_staging_until_verified(self):
        """download() must not create the final path itself."""
        payload = self.payload

        class Response:
            def __init__(self):
                self.chunks = [payload, b""]

            def __enter__(self):
                return self

            def __exit__(self, *exc):
                return False

            def read(self, _size):
                return self.chunks.pop(0)

        destination = self.directory / "hoop_9.9.9_Linux_amd64.tar.gz"
        with mock.patch.object(fetch, "http_get", lambda *a, **k: Response()):
            digest = fetch.download("https://example.invalid/a.tar.gz", destination)
        self.assertEqual(digest, self.digest)
        self.assertFalse(destination.exists())
        staging = destination.with_suffix(destination.suffix + ".part")
        self.assertTrue(staging.exists())

    def test_latest_version_spans_pages(self):
        """The highest version can sit on a later page; creation order differs."""
        pages = [
            [{"tag_name": "1.135.0", "draft": False, "prerelease": False}],
            [
                {"tag_name": "1.200.0", "draft": False, "prerelease": False},
                {"tag_name": "9.9.9", "draft": True, "prerelease": False},
                {"tag_name": "8.8.8", "draft": False, "prerelease": True},
                {"tag_name": "nightly", "draft": False, "prerelease": False},
            ],
        ]
        with mock.patch.object(fetch, "paginate", lambda *a, **k: iter(pages)):
            self.assertEqual(fetch.resolve_latest_version(), "1.200.0")

    def test_no_published_release_is_an_error(self):
        with mock.patch.object(fetch, "paginate", lambda *a, **k: iter([[]])):
            with self.assertRaises(fetch.VerificationError):
                fetch.resolve_latest_version()

    def test_version_ordering_is_numeric_not_lexicographic(self):
        self.assertGreater(fetch.version_key("1.136.0"), fetch.version_key("1.9.0"))
        self.assertGreater(fetch.version_key("1.136.10"), fetch.version_key("1.136.9"))

    def test_shipped_pins_file_parses(self):
        pins = fetch.load_pins(SCRIPTS / "legacy-release-checksums.sha256")
        self.assertIn("hoop_1.136.0_Linux_x86_64.tar.gz", pins)
        self.assertIn("hoop_1.136.0_Linux_aarch64.tar.gz", pins)


if __name__ == "__main__":
    unittest.main()

"""Offline GPU asset contract tests; binaries here are deliberately synthetic."""
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("gpu_assets", Path(__file__).with_name("gpu-assets.py"))
assets = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assets)


class AssetsTests(unittest.TestCase):
    def setUp(self):
        self.base = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.root = self.base / "input"
        self.root.mkdir(parents=True)
        self.meta = {key: "test" for key in assets.METADATA}
        self.meta.update(format=1, arch="amd64", libhoop_ica="v0.7.0")
        (self.root / "manifest.json").write_text(json.dumps(self.meta))
        for name in assets.REQUIRED:
            p = self.root / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(b"synthetic asset")
        (self.root / "bin/hoop-gpuframe-smoke").chmod(0o755)
        self.archive = self.base / "assets.tar.gz"

    def test_round_trip_and_engine_version(self):
        assets.pack(self.root, self.archive)
        out = self.base / "unpacked"
        got = assets.verify(self.archive, engine="v0.7.0", output=out)
        self.assertEqual(set(got["files"]), assets.REQUIRED)
        self.assertEqual(assets.verify_tree(out), got)
        self.assertTrue(os.access(out / "bin/hoop-gpuframe-smoke", os.X_OK))
        with self.assertRaisesRegex(ValueError, "mismatch"):
            assets.verify(self.archive, engine="v0.8.0")
        # Corrupted files and unexpected files are both refused.
        (out / "engines/det.engine").write_bytes(b"corrupted")
        with self.assertRaisesRegex(ValueError, "SHA256 mismatch"):
            assets.verify_tree(out)
        with self.assertRaises(FileExistsError):
            assets.pack(self.root, self.archive)

    def test_missing_file_and_symlink(self):
        # Keep the removed fixture as another artifact instead of deleting it.
        p = self.root / "engines/det.engine"
        p.rename(self.base / "det.saved")
        with self.assertRaisesRegex(ValueError, "missing assets"):
            assets.pack(self.root, self.archive)
        p.symlink_to(self.base / "det.saved")
        with self.assertRaisesRegex(ValueError, "symlink"):
            assets.pack(self.root, self.archive)

    def test_rejects_unsafe_tar_before_extraction(self):
        for index, name in enumerate(("../escape", "/absolute", "engines/../escape", "./manifest.json")):
            archive = self.base / f"bad-{index}.tar.gz"
            with tarfile.open(archive, "w:gz") as tar:
                entry = tarfile.TarInfo(name)
                entry.size = 1
                tar.addfile(entry, io.BytesIO(b"x"))
            with self.assertRaisesRegex(ValueError, "unsafe asset path"):
                assets.verify(archive, output=self.base / f"bad-out-{index}")
            self.assertFalse((self.base / f"bad-out-{index}").exists())

    def test_duplicate_and_link_entries(self):
        for index, kind in enumerate(("duplicate", "link")):
            archive = self.base / f"invalid-{index}.tar.gz"
            with tarfile.open(archive, "w:gz") as tar:
                entry = tarfile.TarInfo("manifest.json")
                entry.size = 2
                tar.addfile(entry, io.BytesIO(b"{}"))
                if kind == "link":
                    entry = tarfile.TarInfo("lib/alias.so")
                    entry.type, entry.linkname = tarfile.SYMTYPE, "../../outside"
                    tar.addfile(entry)
                else:
                    tar.addfile(entry, io.BytesIO(b"{}"))
            with self.assertRaisesRegex(ValueError, "non-regular or duplicate"):
                assets.verify(archive)

    def test_inventory_and_hash_tampering(self):
        assets.pack(self.root, self.archive)
        with tarfile.open(self.archive, "r:gz") as src:
            entries = [(m, src.extractfile(m).read()) for m in src]
        for mode in ("extra", "corrupt"):
            archive = self.base / f"{mode}.tar.gz"
            with tarfile.open(archive, "w:gz") as tar:
                for old, data in entries:
                    if mode == "corrupt" and old.name == "engines/det.engine":
                        data = b"bad engine"
                    entry = tarfile.TarInfo(old.name)
                    entry.size = len(data)
                    tar.addfile(entry, io.BytesIO(data))
                if mode == "extra":
                    entry = tarfile.TarInfo("unexpected")
                    tar.addfile(entry, io.BytesIO(b""))
            with self.assertRaises(ValueError):
                assets.verify(archive)


if __name__ == "__main__":
    unittest.main()

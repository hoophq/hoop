"""Offline producer tests. Native files/commands are synthetic, never GPU evidence."""
import argparse
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("gpu_builder", Path(__file__).with_name("build-gpu-runtime.py"))
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class BuilderTests(unittest.TestCase):
    def setUp(self):
        self.base = Path(self.enterContext(tempfile.TemporaryDirectory()))

    def file(self, relative, data=b"synthetic"):
        path = self.base / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("xb") as stream:
            stream.write(data)
        return path

    def test_dependency_parser_and_host_exclusions(self):
        entries = builder.dependencies("libocr.so.1 => /sdk/libocr.so.1 (0xff)\n/lib64/ld-linux-x86-64.so.2 (0xff)\nlinux-vdso.so.1 (0xff)")
        self.assertEqual(entries[0], ("libocr.so.1", Path("/sdk/libocr.so.1")))
        with self.assertRaisesRegex(ValueError, "unresolved"):
            builder.dependencies("libcudnn.so.9 => not found")
        for name in ("libcuda.so.1", "libnvidia-ml.so.1", "libc.so.6", "ld-linux-x86-64.so.2"):
            self.assertTrue(builder.HOST_LIBRARY.match(name), name)
        for name in ("libcudart.so.13", "libcudnn.so.9", "libstdc++.so.6"):
            self.assertFalse(builder.HOST_LIBRARY.match(name), name)

    def test_collector_materializes_aliases_and_ort_provider_dependencies(self):
        entry = self.file("sdk/libentry.so")
        actual = self.file("sdk/libonnxruntime.so.1.28.0")
        alias = actual.with_name("libonnxruntime.so.1")
        alias.symlink_to(actual.name)
        lazy = self.file("sdk/libcudnn_engines_precompiled.so.9")
        self.file("sdk/libcuda.so.1")
        root = self.base / "runtime"
        def ldd(argv, **kwargs):
            if Path(argv[1]) == entry:
                return f"libonnxruntime.so.1 => {alias} (0xff)\nlibcuda.so.1 => /host/libcuda.so.1 (0xff)"
            return ""
        inventory = builder.collect(root, [(entry.name, entry)], [lazy.parent], {}, run=ldd)
        self.assertTrue((root / "ort/lib/libonnxruntime.so.1").is_file())
        self.assertFalse((root / "ort/lib/libonnxruntime.so.1").is_symlink())
        self.assertTrue((root / "lib/libcudnn_engines_precompiled.so.9").is_file())
        self.assertFalse((root / "lib/libcuda.so.1").exists())
        self.assertIn("ort/lib/libonnxruntime.so.1", inventory)

    def test_collision_preserves_first_library(self):
        a = self.file("a/libduplicate.so", b"first")
        b = self.file("b/libduplicate.so", b"second")
        root = self.base / "runtime"
        with self.assertRaisesRegex(ValueError, "conflicting"):
            builder.collect(root, [(a.name, a), (b.name, b)], [], {}, run=lambda *a, **k: "")
        self.assertEqual((root / "lib/libduplicate.so").read_bytes(), b"first")

    def test_relocation_and_dependency_isolation(self):
        root = self.base / "runtime"
        self.file("runtime/lib/libentry.so")
        self.file("runtime/ort/lib/libonnxruntime.so")
        self.file("runtime/bin/hoop-gpuframe-smoke")
        calls = []
        builder.relocate(root, run=lambda args: calls.append(args))
        self.assertTrue(all("$ORIGIN" in args[2] for args in calls))
        builder.check_closure(root, {}, run=lambda *a, **k: "libcuda.so.1 => /driver/libcuda.so.1 (0xff)")
        with self.assertRaisesRegex(ValueError, "builder file"):
            builder.check_closure(root, {}, run=lambda *a, **k: "libentry.so => /builder/libentry.so (0xff)")

    def test_source_pins_and_dirty_checkout(self):
        sha = "a" * 40
        def clean(argv):
            if "rev-parse" in argv: return sha
            if "submodule" in argv: return " 012345 dependency\n"
            return ""
        with mock.patch.object(builder, "command", side_effect=clean):
            self.assertEqual(builder.source_revision(self.base, sha), sha)
        def dirty(argv):
            if "diff" in argv: raise subprocess.CalledProcessError(1, argv)
            return clean(argv)
        with mock.patch.object(builder, "command", side_effect=dirty):
            with self.assertRaises(subprocess.CalledProcessError):
                builder.source_revision(self.base, sha)
        with mock.patch.object(builder, "command", side_effect=["a" * 40, "b" * 40]):
            with self.assertRaisesRegex(ValueError, "does not match"):
                builder.source_revision(self.base, sha)

    def setup_build(self):
        self.file("cuda/include/cuda.h", b"#define CUDA_VERSION 13020\n")
        self.file("cudnn/include/cudnn_version.h", b"#define CUDNN_MAJOR 9\n#define CUDNN_MINOR 9\n#define CUDNN_PATCHLEVEL 0\n")
        self.file("trt/include/NvInferVersion.h", b"#define NV_TENSORRT_MAJOR 10\n#define NV_TENSORRT_MINOR 16\n#define NV_TENSORRT_PATCH 0\n#define NV_TENSORRT_BUILD 72\n")
        for name in ("libonnxruntime.so", "libonnxruntime_providers_cuda.so", "libonnxruntime_providers_shared.so"):
            self.file("ort/lib/" + name)
        (self.base / "pdfium").mkdir()
        for name in ("det.engine", "rec.engine", "dict.txt"):
            self.file("engines/" + name)
        self.file("fixture.ppm", b"P6\n1 1\n255\nabc")
        self.file("expected.txt", b"SYNTHETIC TEXT")
        self.file("licenses/NOTICE", b"Synthetic fixtures only")
        self.file("turbo-source/LICENSE")
        self.file("ica-source/LICENSE")
        self.file("ica-source/NOTICE")
        cfg = {k: str(self.base / v) for k, v in {
            "cuda_root": "cuda", "cudnn_root": "cudnn", "tensorrt_root": "trt", "ort_root": "ort",
            "pdfium_root": "pdfium", "engines_dir": "engines", "smoke_fixture": "fixture.ppm",
            "smoke_expected": "expected.txt", "licenses_dir": "licenses"}.items()}
        cfg.update(turboocr_revision="a" * 40, metadata={"cuda": "13.2", "cudnn": "9.9.0", "tensorrt": "10.16.0.72",
            "ort": "1.28.0", "min_driver": "580.0", "compute_capability": "12.0", "gpu_model": "RTX 5090"})
        config = self.file("config.json", json.dumps(cfg).encode())
        return argparse.Namespace(config=config, turboocr_source=self.base / "turbo-source",
            ica_source=self.base / "ica-source", work_dir=self.base / "build", version="test-1", jobs="2")

    def emulate_build(self, fail_smoke=False):
        def run(argv, **kwargs):
            argv = [str(a) for a in argv]
            if argv[0] == "nvidia-smi": return "NVIDIA GeForce RTX 5090, 12.0, 590.0, GPU-synthetic\n"
            if argv[0] == "cmake" and "--build" in argv:
                out = Path(argv[argv.index("--build") + 1])
                out.mkdir(exist_ok=True)
                names = ("libturboocr-device.so", "turbo_ocr_tests") if "turboocr-device" in argv else ("libhoop-gpuframe.so", "hoop-gpuframe-smoke")
                for name in names:
                    with (out / name).open("xb") as stream: stream.write(b"synthetic binary")
            if argv[0].endswith("/runtime/bin/hoop-gpuframe-smoke"):
                if fail_smoke and argv[-1] == "matches-only":
                    raise subprocess.CalledProcessError(1, argv, output="FAIL synthetic smoke")
                return "PASS: synthetic test double (not GPU inference)\n"
            return ""
        return run

    def test_mocked_build_success_packs_verified_archive(self):
        args = self.setup_build()
        with mock.patch.object(builder.platform, "system", return_value="Linux"), \
             mock.patch.object(builder.platform, "machine", return_value="x86_64"), \
             mock.patch.object(builder, "source_revision", return_value="a" * 40), \
             mock.patch.object(builder, "ort_version", return_value="1.28.0"), \
             mock.patch.object(builder, "command", side_effect=self.emulate_build()), \
             mock.patch.object(builder, "relocate"), mock.patch.object(builder, "check_closure"):
            # Pass the dynamic command explicitly because collect's default
            # argument intentionally captures the production runner.
            original = builder.collect
            with mock.patch.object(builder, "collect", side_effect=lambda r, s, d, e: original(r, s, d, e, run=builder.command)):
                archive = builder.build(args)
        manifest = builder.assets.verify(archive, engine=builder.pin())
        self.assertTrue(manifest["validation"]["matches_only_smoke"])
        self.assertEqual(manifest["compute_capability"], "12.0")
        self.assertTrue(archive.with_name(archive.name + ".sha256").is_file())

    def test_smoke_failure_prevents_asset_creation(self):
        args = self.setup_build()
        original = builder.collect
        with mock.patch.object(builder.platform, "system", return_value="Linux"), \
             mock.patch.object(builder.platform, "machine", return_value="x86_64"), \
             mock.patch.object(builder, "source_revision", return_value="a" * 40), \
             mock.patch.object(builder, "ort_version", return_value="1.28.0"), \
             mock.patch.object(builder, "command", side_effect=self.emulate_build(fail_smoke=True)), \
             mock.patch.object(builder, "relocate"), mock.patch.object(builder, "check_closure"), \
             mock.patch.object(builder, "collect", side_effect=lambda r, s, d, e: original(r, s, d, e, run=builder.command)):
            with self.assertRaises(subprocess.CalledProcessError): builder.build(args)
        self.assertEqual(list(args.work_dir.glob("*.tar.gz")), [])
        self.assertIn("FAIL", (args.work_dir / "logs/smoke-matches-only.log").read_text())


if __name__ == "__main__":
    unittest.main()

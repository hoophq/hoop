"""Keep image path versions and CI on the same pinned ORT download."""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class RuntimeManifestTest(unittest.TestCase):
    def test_docker_and_default_library_versions_match_manifest(self):
        manifest = (ROOT / "deploy/onnxruntime/manifest.env").read_text()
        version = re.search(r"^ORT_PACKAGE_VERSION=(\d+\.\d+\.\d+)$", manifest, re.M).group(1)
        self.assertRegex(manifest, r"(?m)^ORT_PACKAGE_SHA256=[0-9a-f]{64}$")
        for name in ("Dockerfile.agent", "Dockerfile.sidecar"):
            with self.subTest(name=name):
                source = (ROOT / name).read_text()
                self.assertEqual(re.search(r"^ARG ORT_VERSION=(.+)$", source, re.M).group(1), version)
                self.assertIn('sh /opt/hoop-ort-download/fetch.sh /opt/ort "${TARGETARCH}"', source)
                self.assertNotIn("github.com/microsoft/onnxruntime/releases/download", source)
        source = (ROOT / "sidecar/pii/alcatraz/ner.go").read_text()
        self.assertIn(f"/opt/alcatraz/ort/releases/{version}/lib/libonnxruntime.so", source)

    def test_ner_ci_uses_the_shared_downloader(self):
        source = (ROOT / ".github/workflows/pullrequest.yml").read_text()
        job = source.split("  sidecar-pii-ner:", 1)[1].split("  sidecar-plugins:", 1)[0]
        self.assertIn('sh deploy/onnxruntime/fetch.sh "$RUNNER_TEMP/ort" amd64', job)
        self.assertNotIn("ORT_VERSION:", job)
        self.assertNotIn("github.com/microsoft/onnxruntime/releases/download", job)

    def test_bundle_rebuild_includes_shared_downloader(self):
        source = (ROOT / ".github/workflows/pullrequest-release.yml").read_text()
        gate = source.split("- name: Decide whether the bundle changed", 1)[1].split("- name: Set up QEMU", 1)[0]
        self.assertIn("deploy/onnxruntime", gate)


if __name__ == "__main__":
    unittest.main()

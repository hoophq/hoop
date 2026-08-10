#!/usr/bin/env python3
"""Regression tests for the release SBOM generator."""

from __future__ import annotations

import importlib.util
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).with_name("generate-sbom-manifest.py")
SPEC = importlib.util.spec_from_file_location("generate_sbom_manifest", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"could not load {MODULE_PATH}")
generate_sbom_manifest = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(generate_sbom_manifest)


class SubprocessRunnerTests(unittest.TestCase):
    @mock.patch.object(generate_sbom_manifest.subprocess, "run")
    def test_trivy_enforces_checked_subprocess(self, run: mock.Mock) -> None:
        run.return_value = subprocess.CompletedProcess(args=[], returncode=0)

        generate_sbom_manifest.trivy("/cache", "/out", "image", "--help")

        run.assert_called_once_with(
            [
                "docker",
                "run",
                "--rm",
                "-v",
                "/cache:/root/.cache/trivy",
                "-v",
                "/out:/out",
                generate_sbom_manifest.TRIVY_IMAGE,
                "image",
                "--help",
            ],
            check=True,
        )

    @mock.patch.object(generate_sbom_manifest.subprocess, "run")
    def test_run_defaults_to_nonchecking_subprocess(self, run: mock.Mock) -> None:
        run.return_value = subprocess.CompletedProcess(args=[], returncode=1)

        result = generate_sbom_manifest._run(["false"])

        self.assertEqual(1, result.returncode)
        run.assert_called_once_with(["false"], check=False)


class RequiredImageContractTests(unittest.TestCase):
    """The release contract: every documented image, on both platforms.

    These cover the decisions that make the published evidence correct, which
    the subprocess tests above cannot see.
    """

    def test_every_documented_image_is_required(self) -> None:
        documented = {repo for repo, _ in generate_sbom_manifest.IMAGE_REPOS}
        self.assertEqual(generate_sbom_manifest.REQUIRED_IMAGE_REPOS, documented)

    def test_both_release_platforms_are_required(self) -> None:
        self.assertEqual(
            generate_sbom_manifest.REQUIRED_PLATFORMS,
            {"linux/amd64", "linux/arm64"},
        )

    def test_slim_agent_flavours_are_documented(self) -> None:
        """Both slim tags must be covered; neither is optional any more."""
        entries = set(generate_sbom_manifest.IMAGE_REPOS)
        self.assertIn(("hoophq/hoopagent", "-minimal"), entries)
        self.assertIn(("hoophq/hoopagent", "-distroless"), entries)

    def run_main(self, resolved) -> int:
        with tempfile.TemporaryDirectory() as outdir:
            argv = ["generate-sbom-manifest.py", "1.0.0", outdir]
            with mock.patch.object(generate_sbom_manifest.sys, "argv", argv):
                with mock.patch.object(generate_sbom_manifest, "resolve_image", return_value=resolved):
                    with mock.patch.object(generate_sbom_manifest, "trivy"):
                        with mock.patch.object(generate_sbom_manifest, "scan_platform", return_value={}):
                            return generate_sbom_manifest.main()

    def test_an_unresolvable_image_fails_the_run(self) -> None:
        self.assertEqual(self.run_main(None), 1)

    def test_a_missing_platform_fails_the_run(self) -> None:
        """An amd64-only manifest must not be accepted as complete."""
        resolved = ("sha256:" + "a" * 64, {"linux/amd64": "sha256:" + "b" * 64})
        self.assertEqual(self.run_main(resolved), 1)


if __name__ == "__main__":
    unittest.main()

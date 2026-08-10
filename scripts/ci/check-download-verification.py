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
from urllib.parse import urlparse

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

# Downloads verified by something other than the SHA-256 manifest, keyed by
# (host, path prefix) so a URL cannot claim an exemption by carrying the
# trusted string somewhere in its path — https://evil.example/nodejs.org/dist/…
# is not nodejs.org. Each entry names the ALTERNATIVE check that must also
# appear in the instruction, so an exemption lapses the moment its real
# verification is dropped.
EXEMPT: dict[tuple[str, str], tuple[str, str]] = {
    ("packages.cloud.google.com", "/apt/doc/apt-key.gpg"): (
        "GOOGLE_CLOUD_KEY_FPR",
        "apt repository signing key, pinned by full OpenPGP fingerprint and "
        "installed into a per-repository signed-by keyring.",
    ),
    ("www.mongodb.org", "/static/pgp"): (
        "MONGODB_KEY_FPR",
        "apt repository signing key, pinned by full OpenPGP fingerprint and "
        "installed into a per-repository signed-by keyring.",
    ),
    ("nodejs.org", "/dist"): (
        "SHASUMS256.txt.asc",
        "Node.js tarball, verified against upstream's GPG-signed SHASUMS256.txt "
        "using the release keyring listed above it — a stronger check than a "
        "static digest.",
    ),
}


def instructions(dockerfile: str) -> list[tuple[int, str]]:
    """Yield (starting line number, flattened text) per instruction.

    Backslash continuations are joined into ONE logical line so a command
    whose URL sits on a different physical line than its `curl` is still seen
    as a single command. Comments are dropped rather than treated as the end
    of an instruction: inside a continued RUN a comment line carries no
    trailing backslash, and ending the instruction there would hide every
    command after it from the checks below.
    """
    out: list[tuple[int, str]] = []
    current: list[str] = []
    start = 0
    continued = False
    for number, raw in enumerate(dockerfile.splitlines(), 1):
        line = raw.rstrip()
        stripped = line.strip()
        if not continued:
            if not stripped or stripped.startswith("#"):
                continue
            start = number
        elif stripped.startswith("#"):
            # A comment inside a continuation: skip it without closing the
            # instruction. It cannot itself continue the line.
            continue
        continues = line.endswith("\\")
        current.append(line[:-1].strip() if continues else stripped)
        continued = continues
        if not continued:
            out.append((start, " ".join(part for part in current if part)))
            current = []
    if current:
        out.append((start, " ".join(part for part in current if part)))
    return out


URL = re.compile(r"https?://[^\s\"']+")

# `deb [...] https://... suite component` lines declare an apt repository; they
# are configuration, not a fetch. Everything apt then pulls from them is
# authenticated by the repository's signing key, which is fingerprint-pinned
# above. Dropping these keeps a source-list entry from looking like a download.
APT_SOURCE = re.compile(r"\bdeb(?:-src)?\s+(?:\[[^\]]*\]\s*)?(https?://\S+)")


def exempt_proof(url: str) -> str | None:
    """The alternative check a URL's exemption requires, if it has one.

    Matched on parsed host and path prefix, never on a substring of the whole
    URL: an attacker-controlled host is not made trustworthy by mentioning a
    trusted one in its path.
    """
    parsed = urlparse(url)
    for (host, prefix), (proof, _) in EXEMPT.items():
        if parsed.hostname == host and parsed.path.startswith(prefix):
            return proof
    return None


def unexempted_urls(text: str) -> list[str]:
    """URLs the instruction fetches that still need checksum verification.

    A URL is dropped when it declares an apt repository (configuration, not a
    fetch) or when it carries an exemption whose alternative check is present.
    """
    sources = set(APT_SOURCE.findall(text))
    remaining = []
    for url in URL.findall(text):
        if url in sources:
            continue
        proof = exempt_proof(url)
        if proof is not None and proof in text:
            continue
        remaining.append(url)
    if not remaining and not URL.search(text) and DOWNLOAD.search(text):
        # Fetches a URL held in a variable. Nothing here can review those
        # bytes, so it cannot be waved through as "no unexempted URLs".
        return ["<variable>"]
    return remaining


# Commands that consume a downloaded artifact: once one of these runs, an
# unverified file has already been installed or executed and a later check is
# too late to matter.
CONSUMER = re.compile(
    r"\b(?:dpkg\s+-i|apt-key\s+add|install\b|tar\s|unzip\b|gpg\b|bash\b|sh\b|python3?\b|\./)"
)

# `verify-manual-download.py <manifest> <artifact>` — capture what it verifies
# so the artifact can be matched against what was downloaded.
VERIFY_CALL = re.compile(
    re.escape(VERIFIER) + r"\s+(?P<manifest>\S+)\s+(?P<artifact>\S+)"
)

# The file a fetch writes: `-o x`, `--output x`, or `-O`/`--remote-name` (which
# derives the name from the URL).
FETCH_TARGET = re.compile(r"(?:-o|--output)\s+(?P<target>\S+)")


def shell_words(command: str) -> str:
    """Normalise a token for comparison: strip quotes and any $-expansion."""
    return command.strip().strip("\"'")


def split_commands(text: str) -> list[str]:
    """Split a flattened instruction into commands, in execution order."""
    return [part for part in re.split(r"&&|\|\||;", text) if part.strip()]


def verification_order_problem(text: str) -> str | None:
    """Report a verifier that runs too late, or verifies the wrong file.

    Substring presence is not enough. `curl x && dpkg -i x && verify x` and
    `curl x && verify y && dpkg -i x` both mention the verifier while leaving
    unauthenticated bytes installed, which is exactly what this gate exists to
    prevent.
    """
    downloaded: set[str] = set()
    verified: set[str] = set()
    for command in split_commands(text):
        if DOWNLOAD.search(command):
            target = FETCH_TARGET.search(command)
            if target:
                downloaded.add(shell_words(target.group("target")))
            elif re.search(r"\s(?:-O|--remote-name|-OL|-LO)\b", command):
                url = URL.search(command)
                if url:
                    downloaded.add(url.group(0).rsplit("/", 1)[-1])

        verify = VERIFY_CALL.search(command)
        if verify:
            verified.add(shell_words(verify.group("artifact")))
            continue

        if CONSUMER.search(command):
            pending = {
                name
                for name in downloaded - verified
                # Only complain when this command actually touches the file.
                if name and name in command
            }
            if pending:
                return (
                    f"installs or executes {sorted(pending)[0]} before "
                    f"{VERIFIER} has verified it"
                )

    unverified = downloaded - verified
    if unverified:
        return (
            f"downloads {sorted(unverified)[0]} but never passes it to "
            f"{VERIFIER}"
        )
    return None


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
        if not unexempted_urls(code):
            continue
        if VERIFIER not in code:
            problems.append(
                f"{path}:{number}: RUN downloads an artifact but never calls "
                f"{VERIFIER}. Pin its SHA-256 in "
                f"licenses/agent-tools-checksums.sha256 and verify it in this "
                f"same RUN, before the artifact is installed or executed."
            )
        else:
            ordering = verification_order_problem(code)
            if ordering:
                problems.append(
                    f"{path}:{number}: RUN {ordering}. The verifier has to run "
                    f"on the downloaded file, before anything installs or "
                    f"executes it — otherwise the check proves nothing."
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

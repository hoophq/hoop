#!/usr/bin/env python3
"""Gate hand-installed (non-dpkg) binaries against a declared license manifest.

Trivy's license scanner is package-manager-based. Per Trivy's own docs,
"custom-built binaries or those installed via curl may not be detected" — so
anything dropped into an image via curl/tarball/zip (Node, kubectl, aws-cli,
sqlcmd, Oracle Instant Client, and the SSPL-licensed legacy MongoDB 5.0 shell)
is invisible to `trivy image --scanners license`. That blind spot is exactly how
the SSPL shell shipped while the AGPL/SSPL gate reported "clean".

This script closes it. For a built image and a named train it:

  1. enumerates every path under the manifest's `scan_dirs` that dpkg does NOT
     own (i.e. the hand-installed artifacts Trivy can't see);
  2. requires each to match an entry in licenses/manual-binaries.toml — an
     undeclared binary fails, so no curl/tarball install can be added without
     declaring its license;
  3. fails if a forbidden-licensed (AGPL/SSPL) entry is present on a train not
     listed in its `trains` — so the clean train must carry no AGPL/SSPL binary.

Usage:
    check-manual-binaries.py --image IMAGE --train {legacy|clean} \
        [--manifest licenses/manual-binaries.toml]

Requires Python 3.11+ (tomllib) and a local Docker daemon holding IMAGE.
"""

import argparse
import re
import shlex
import subprocess
import sys
import tomllib

FORBIDDEN = re.compile(r"\b(AGPL|SSPL)\b", re.IGNORECASE)


class ManifestError(RuntimeError):
    """Raised when the manifest is malformed."""


def load_manifest(path: str) -> tuple[list[str], list[dict]]:
    with open(path, "rb") as fp:
        data = tomllib.load(fp)
    scan_dirs = data.get("scan_dirs")
    if not isinstance(scan_dirs, list) or not scan_dirs:
        raise ManifestError(f"{path}: 'scan_dirs' must be a non-empty array")
    # These are embedded into a shell script (defended by shlex.quote); require
    # absolute paths with no newlines/NUL so the manifest can never smuggle in
    # shell control characters even if the trust model changes later.
    for d in scan_dirs:
        if not isinstance(d, str) or not d.startswith("/") or any(
                c in d for c in "\n\r\0"):
            raise ManifestError(
                f"{path}: scan_dir {d!r} must be an absolute path "
                f"without newlines")
    entries = data.get("binaries", [])
    for entry in entries:
        for field in ("path", "license", "trains"):
            if field not in entry:
                raise ManifestError(
                    f"{path}: entry {entry!r} is missing required '{field}'")
        if not isinstance(entry["trains"], list) or not entry["trains"]:
            raise ManifestError(
                f"{path}: entry {entry['path']!r} needs a non-empty 'trains'")
    return scan_dirs, entries


def match_entry(path: str, entries: list[dict]) -> dict | None:
    """Match by exact path, then by longest directory-prefix (trailing '/')."""
    for entry in entries:
        if entry["path"] == path:
            return entry
    best: dict | None = None
    for entry in entries:
        p = entry["path"]
        if p.endswith("/") and path.startswith(p):
            if best is None or len(p) > len(best["path"]):
                best = entry
    return best


def enumerate_image(image: str, scan_dirs: list[str]) -> list[tuple[str, bool]]:
    """Return (path, dpkg_managed) for every entry under scan_dirs in IMAGE."""
    dirs = " ".join(shlex.quote(d) for d in scan_dirs)
    # Recurse (no -maxdepth) so nested artifacts are covered, and restrict to
    # regular files and symlinks so directories themselves are not flagged.
    script = f"""
set -eu
for d in {dirs}; do
  [ -d "$d" ] || continue
  find "$d" -mindepth 1 -name __pycache__ -prune -o \\( -type f -o -type l \\) -print | while IFS= read -r p; do
    if dpkg -S "$p" >/dev/null 2>&1; then
      printf 'MANAGED\\t%s\\n' "$p"
    else
      printf 'UNMANAGED\\t%s\\n' "$p"
    fi
  done
done
"""
    proc = subprocess.run(
        ["docker", "run", "--rm", "--entrypoint", "sh", image, "-c", script],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"failed to inspect image {image!r}:\n{proc.stderr.strip()}")
    results: list[tuple[str, bool]] = []
    for line in proc.stdout.splitlines():
        if not line.strip():
            continue
        state, _, path = line.partition("\t")
        results.append((path, state == "MANAGED"))
    return results


def evaluate(paths: list[tuple[str, bool]], entries: list[dict],
             train: str) -> list[str]:
    """Pure policy check. Returns a list of human-readable violations."""
    violations: list[str] = []
    for path, managed in sorted(paths):
        if managed:
            continue  # dpkg-owned: covered by Trivy's package license scan
        entry = match_entry(path, entries)
        if entry is None:
            violations.append(
                f"undeclared hand-installed artifact: {path} — add it to the "
                f"manifest with its license (or a covering path prefix)")
            continue
        if FORBIDDEN.search(entry["license"]) and train not in entry["trains"]:
            violations.append(
                f"{path}: {entry['license']} is forbidden on train "
                f"'{train}' (allowed only on: {', '.join(entry['trains'])})")
    return violations


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True, help="image name/tag to inspect")
    parser.add_argument("--train", required=True, choices=["legacy", "clean"],
                        help="which image train this build represents")
    parser.add_argument("--manifest", default="licenses/manual-binaries.toml",
                        help="path to the manual-binaries manifest")
    args = parser.parse_args()

    scan_dirs, entries = load_manifest(args.manifest)
    paths = enumerate_image(args.image, scan_dirs)
    violations = evaluate(paths, entries, args.train)

    unmanaged = sum(1 for _, managed in paths if not managed)
    print(f"scanned {len(paths)} paths under {scan_dirs} "
          f"({unmanaged} hand-installed) on train '{args.train}'")
    if violations:
        for v in violations:
            print(f"FORBIDDEN {args.image}: {v}", file=sys.stderr)
        print(f"manual-binary check: {len(violations)} violation(s)")
        return 1
    print("manual-binary check: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())

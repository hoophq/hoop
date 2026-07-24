#!/usr/bin/env python3
"""Gate hand-installed (non-dpkg) binaries against a declared license manifest.

Trivy's license scanner is package-manager-based. Per Trivy's own docs,
"custom-built binaries or those installed via curl may not be detected" — so
anything dropped into an image via curl/tarball/zip (Node, kubectl, aws-cli,
sqlcmd, Oracle Instant Client, and the SSPL-licensed legacy MongoDB 5.0 shell)
is invisible to `trivy image --scanners license`. That blind spot is exactly how
the SSPL shell shipped while the AGPL/SSPL gate reported "clean".

This script closes it. For a built image and a named train it:

  1. finds every file/symlink under the manifest's `scan_dirs` that is absent
     from the dpkg package file lists (a fast bulk pre-filter);
  2. requires each to match an entry in licenses/manual-binaries.toml — an
     undeclared artifact fails, so no curl/tarball install can be added without
     declaring its license;
  3. fails if a forbidden-licensed (AGPL/SSPL) entry is present on a train not
     listed in its `trains` — so the clean train must carry no AGPL/SSPL binary;
  4. before failing, confirms each would-be violation with `dpkg -S` (the
     authoritative ownership check), so a file-list pre-filter miss — e.g. a
     dpkg diversion — can never spuriously block CI.

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
    # scan_dirs are embedded into a shell `set --` list; require absolute paths
    # with no whitespace or shell metacharacters so word-splitting/globbing can
    # never reinterpret them, even if the trust model changes later.
    for d in scan_dirs:
        if not isinstance(d, str) or not d.startswith("/") or any(
                c in d for c in "\n\r\0\t *?[]{}"):
            raise ManifestError(
                f"{path}: scan_dir {d!r} must be an absolute path without "
                f"whitespace or shell metacharacters")
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


def enumerate_candidates(image: str, scan_dirs: list[str]) -> list[str]:
    """Files/symlinks under scan_dirs absent from the dpkg package file lists.

    This is a fast bulk pre-filter (diff against the union of
    /var/lib/dpkg/info/*.list), NOT full dpkg ownership resolution — callers
    confirm any resulting policy violation with `dpkg -S` (see `dpkg_owned`) so a
    file-list miss (e.g. a dpkg diversion) cannot cause a false CI failure.
    Directories and __pycache__ are excluded.
    """
    dirs = " ".join(shlex.quote(d) for d in scan_dirs)
    script = f"""
set -eu
set -- {dirs}
managed="$(mktemp)"; found="$(mktemp)"
find /var/lib/dpkg/info -name '*.list' -exec cat {{}} + 2>/dev/null \\
  | LC_ALL=C sort -u > "$managed"
for d in "$@"; do
  [ -d "$d" ] || continue
  find "$d" -mindepth 1 -name __pycache__ -prune -o \\( -type f -o -type l \\) -print
done | LC_ALL=C sort -u > "$found"
LC_ALL=C comm -23 "$found" "$managed"
rm -f "$managed" "$found"
"""
    proc = subprocess.run(
        ["docker", "run", "--rm", "--entrypoint", "sh", image, "-c", script],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"failed to inspect image {image!r}:\n{proc.stderr.strip()}")
    return [line for line in proc.stdout.splitlines() if line.strip()]


def dpkg_owned(image: str, paths: list[str]) -> set[str]:
    """Return the subset of `paths` that dpkg authoritatively reports as owned.

    Runs inside the image. Only called on would-be violations (normally none),
    so per-path `dpkg -S` is cheap here. Paths are passed on argv, never
    interpolated into the shell program.
    """
    if not paths:
        return set()
    script = ('for p in "$@"; do '
              'if dpkg -S "$p" >/dev/null 2>&1; then printf "%s\\n" "$p"; fi; '
              'done')
    proc = subprocess.run(
        ["docker", "run", "--rm", "--entrypoint", "sh", image, "-c", script,
         "sh", *paths],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"failed to confirm dpkg ownership in {image!r}:\n"
            f"{proc.stderr.strip()}")
    return {line for line in proc.stdout.splitlines() if line.strip()}


def evaluate(candidate_paths: list[str], entries: list[dict],
             train: str) -> list[tuple[str, str]]:
    """Pure policy check. Returns (path, message) for each would-be violation.

    Every candidate must be declared in the manifest, and a forbidden-licensed
    entry may only appear on a train that allows it.
    """
    violations: list[tuple[str, str]] = []
    for path in sorted(candidate_paths):
        entry = match_entry(path, entries)
        if entry is None:
            violations.append((path, (
                f"undeclared hand-installed artifact: {path} — add it to the "
                f"manifest with its license (or a covering path prefix)")))
            continue
        if FORBIDDEN.search(entry["license"]) and train not in entry["trains"]:
            violations.append((path, (
                f"{path}: {entry['license']} is forbidden on train "
                f"'{train}' (allowed only on: {', '.join(entry['trains'])})")))
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
    candidates = enumerate_candidates(args.image, scan_dirs)
    violations = evaluate(candidates, entries, args.train)

    # Confirm would-be violations with authoritative dpkg ownership: drop any
    # path dpkg actually owns (a file-list pre-filter miss), so CI never fails
    # on a dpkg-managed file.
    owned = dpkg_owned(args.image, [p for p, _ in violations])
    confirmed = [(p, msg) for p, msg in violations if p not in owned]

    print(f"scanned {len(candidates)} candidate artifact(s) under {scan_dirs} "
          f"on train '{args.train}'")
    for p in sorted(owned):
        print(f"note: {p} is dpkg-owned (file-list pre-filter miss); "
              f"not a violation")
    if confirmed:
        for _, msg in confirmed:
            print(f"FORBIDDEN {args.image}: {msg}", file=sys.stderr)
        print(f"manual-binary check: {len(confirmed)} violation(s)")
        return 1
    print("manual-binary check: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())

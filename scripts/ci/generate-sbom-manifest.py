#!/usr/bin/env python3
"""Generate SBOMs and a per-tag release manifest for the published hoop images.

DEP-91: downstream users build custom agents FROM these images and run their own
security scans. Attaching an SBOM (CycloneDX + SPDX) and a compact JSON manifest
(index digest, base OS, component count, Trivy CRITICAL/HIGH summary) to each
GitHub release lets them verify what shipped and its vulnerability posture
without re-pulling and re-scanning every image themselves.

The published tags are multi-arch (linux/amd64 + linux/arm64). SBOMs and the
vulnerability summary are generated PER PLATFORM: a single default scan on the
amd64 runner would only describe the amd64 variant and misrepresent arm64. For
each image that resolves in the registry this writes, into <outdir>:
  <name>-<arch>.cdx.json    CycloneDX SBOM  (per platform)
  <name>-<arch>.spdx.json   SPDX SBOM       (per platform)
and one combined release-manifest.json recording, per image, the tag's index
digest and a per-platform entry (child digest, base OS, component count, Trivy
CRITICAL/HIGH counts, SBOM filenames).

Trivy pulls the images from the registry directly (respecting --platform), so it
authenticates via TRIVY_USERNAME/TRIVY_PASSWORD when set (forwarded from the
job's Docker Hub credentials) to avoid anonymous rate limits. Images published
by continue-on-error jobs (e.g. hoophq/hoopdev-minimal) may lag or be absent;
visibility is retried, then the image is skipped with a warning rather than
failing.

Usage: generate-sbom-manifest.py <tag> <outdir>
"""
from __future__ import annotations

import hashlib
import json
import os
import subprocess
import sys
import time
from datetime import datetime, timezone

TRIVY_IMAGE = "aquasec/trivy:0.72.0"

# Core published line documented per release. The -ng clean-line images are a
# separate train / mirror and are intentionally out of scope here.
IMAGE_REPOS = ["hoophq/hoop", "hoophq/hoopdev", "hoophq/hoopdev-minimal"]

# Docker Hub can lag before a just-pushed multi-arch manifest becomes visible;
# mirror the retry the release workflow already uses for freshly-pushed tags.
VISIBILITY_RETRIES = 5
VISIBILITY_DELAY_S = 10


def _run(cmd: list[str], **kw) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, **kw)


def resolve_image(ref: str):
    """Resolve a published tag, retrying while the registry lags.

    Returns (index_digest, {platform: child_digest}) or None if the tag never
    becomes visible. A single-manifest (non-index) image yields an empty
    platform map and is scanned without --platform.
    """
    raw = None
    for attempt in range(VISIBILITY_RETRIES):
        cp = _run(
            ["docker", "buildx", "imagetools", "inspect", ref, "--raw"],
            capture_output=True,
        )
        if cp.returncode == 0:
            raw = cp.stdout  # bytes
            break
        if attempt < VISIBILITY_RETRIES - 1:
            print(f"{ref} not visible yet (attempt {attempt + 1}/{VISIBILITY_RETRIES}); retrying")
            time.sleep(VISIBILITY_DELAY_S)
    if not raw:
        return None

    try:
        index = json.loads(raw)
    except json.JSONDecodeError:
        return None

    # A manifest's digest is sha256 over its exact raw bytes, so hash the --raw
    # output directly. This yields the tag's index digest from the same call and
    # avoids a second inspect that could fail (leaving an empty digest) after
    # --raw already succeeded.
    index_digest = "sha256:" + hashlib.sha256(raw).hexdigest()

    children: dict[str, str] = {}
    for m in index.get("manifests", []) or []:
        plat = m.get("platform", {}) or {}
        # Skip attestation / non-runnable manifests (they have no real arch).
        if plat.get("os") == "linux" and plat.get("architecture") and plat.get("architecture") != "unknown":
            children[f"linux/{plat['architecture']}"] = m.get("digest", "")
    return index_digest, children


def trivy(cache: str, outdir: str, *args: str) -> None:
    _run(
        [
            "docker", "run", "--rm",
            "-e", "TRIVY_USERNAME", "-e", "TRIVY_PASSWORD",
            "-v", f"{cache}:/root/.cache/trivy",
            "-v", f"{outdir}:/out",
            TRIVY_IMAGE, *args,
        ],
        check=True,
    )


def summarize(vuln_json_path: str) -> tuple[str, dict]:
    """Return (os_string, severity/fixable counts) from a Trivy JSON report."""
    with open(vuln_json_path) as f:
        report = json.load(f)
    meta_os = report.get("Metadata", {}).get("OS", {}) or {}
    os_str = " ".join(x for x in (meta_os.get("Family"), meta_os.get("Name")) if x)
    counts = {"critical": 0, "high": 0, "fixable_critical": 0, "fixable_high": 0}
    for result in report.get("Results", []) or []:
        for v in result.get("Vulnerabilities", []) or []:
            sev = (v.get("Severity") or "").upper()
            fixed = bool(v.get("FixedVersion"))
            if sev == "CRITICAL":
                counts["critical"] += 1
                counts["fixable_critical"] += int(fixed)
            elif sev == "HIGH":
                counts["high"] += 1
                counts["fixable_high"] += int(fixed)
    return os_str, counts


def component_count(cdx_path: str) -> int:
    with open(cdx_path) as f:
        return len(json.load(f).get("components", []) or [])


def scan_platform(cache: str, outdir: str, ref: str, short: str, platform: str) -> dict:
    """Generate per-platform SBOMs + vuln summary; return the manifest entry."""
    arch = platform.split("/")[-1]
    base = f"{short}-{arch}"
    plat_args = ["--platform", platform]
    trivy(cache, outdir, "image", "--quiet", *plat_args,
          "--format", "cyclonedx", "--output", f"/out/{base}.cdx.json", ref)
    trivy(cache, outdir, "image", "--quiet", *plat_args,
          "--format", "spdx-json", "--output", f"/out/{base}.spdx.json", ref)
    trivy(cache, outdir, "image", "--quiet", *plat_args,
          "--scanners", "vuln", "--severity", "CRITICAL,HIGH",
          "--format", "json", "--output", f"/out/{base}.vuln.json", ref)

    os_str, counts = summarize(os.path.join(outdir, f"{base}.vuln.json"))
    cc = component_count(os.path.join(outdir, f"{base}.cdx.json"))
    # The per-platform vuln JSON is an intermediate; counts live in the manifest,
    # the full inventory lives in the SBOMs.
    os.remove(os.path.join(outdir, f"{base}.vuln.json"))
    return {
        "base_os": os_str,
        "component_count": cc,
        "vulnerabilities": counts,
        "sbom": {"cyclonedx": f"{base}.cdx.json", "spdx": f"{base}.spdx.json"},
    }


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: generate-sbom-manifest.py <tag> <outdir>", file=sys.stderr)
        return 2
    tag, outdir = sys.argv[1], sys.argv[2]
    os.makedirs(outdir, exist_ok=True)
    cache = os.path.abspath(".trivy-cache")
    os.makedirs(cache, exist_ok=True)

    # Warm the vulnerability DB once so the per-image/per-platform scans reuse it
    # instead of re-downloading it for every invocation.
    trivy(cache, outdir, "image", "--download-db-only")

    manifest: dict = {
        "release": tag,
        "git_sha": os.environ.get("GITHUB_SHA", ""),
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "trivy": TRIVY_IMAGE,
        "images": {},
    }

    produced = False
    for repo in IMAGE_REPOS:
        ref = f"{repo}:{tag}"
        short = repo.split("/")[-1]
        resolved = resolve_image(ref)
        if resolved is None:
            print(f"::warning::{ref} not visible after retries; skipping SBOM/manifest entry")
            continue
        index_digest, children = resolved
        # Fall back to a single unqualified scan for a non-index (single-arch) tag.
        platforms = children or {"linux/amd64": ""}

        entry = {"index_digest": index_digest, "platforms": {}}
        for platform, child_digest in sorted(platforms.items()):
            print(f"Generating SBOMs for {ref} ({platform})")
            plat_entry = scan_platform(cache, outdir, ref, short, platform)
            plat_entry["digest"] = child_digest
            entry["platforms"][platform] = plat_entry
        manifest["images"][ref] = entry
        produced = True

    if not produced:
        print("::warning::no images resolved; nothing to attach", file=sys.stderr)
        return 0

    with open(os.path.join(outdir, "release-manifest.json"), "w") as f:
        json.dump(manifest, f, indent=2, sort_keys=True)
        f.write("\n")
    print(json.dumps(manifest, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())

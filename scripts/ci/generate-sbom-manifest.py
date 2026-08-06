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
import re
import subprocess
import sys
import time
from datetime import datetime, timezone

TRIVY_IMAGE = "aquasec/trivy:0.72.0"

# Core published line documented per release. The -ng clean-line images are a
# separate train / mirror and are intentionally out of scope here.
IMAGE_REPOS = ["hoophq/hoop", "hoophq/hoopdev", "hoophq/hoopdev-minimal"]

# Bundled CLI versions recorded in the manifest for the fat agent image, per
# issue #1643 ("bundled tool versions"). Sourced from Dockerfile.tools' ARG
# defaults — the legacy-train pins hoophq/hoopdev is built from. The
# CycloneDX/SPDX SBOMs remain the complete component inventory; this is a
# convenience summary of the CLIs the ticket calls out.
TOOLS_DOCKERFILE = os.path.join(os.path.dirname(__file__), "..", "..", "Dockerfile.tools")
BUNDLED_TOOL_ARGS = {
    "KUBECTL_VERSION": "kubectl",
    "SQLCMD_VERSION": "sqlcmd",
    "MONGOSH_VERSION": "mongosh",
    "MONGODB_TOOLS_VERSION": "mongodb-tools",
    "NODE_VERSION": "node",
    "AWS_CLI_VERSION": "aws-cli",
    "GCLOUD_VERSION": "gcloud",
    "GCLOUD_GKE_AUTHN_PLUGIN_VERSION": "gke-gcloud-auth-plugin",
}
# Only the fat agent bundles those CLIs; the gateway and minimal agent do not.
BUNDLED_TOOL_IMAGES = {"hoophq/hoopdev"}

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
    platform map; the caller then resolves its real platform and scans by digest.
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


def single_platform(ref: str) -> str | None:
    """Return 'os/arch' for a single-manifest (non-index) image, or None.

    Used only for the fallback when a tag is not a multi-arch index: read the
    real platform from the image config instead of assuming one.
    """
    cp = _run(
        ["docker", "buildx", "imagetools", "inspect", ref,
         "--format", "{{.Image.OS}}/{{.Image.Architecture}}"],
        capture_output=True, text=True,
    )
    val = cp.stdout.strip() if cp.returncode == 0 else ""
    if val and "/" in val and "<no value>" not in val:
        return val
    return None


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


def bundled_tools() -> dict:
    """Read bundled-CLI versions from Dockerfile.tools' ARG defaults.

    Best-effort: an ARG that is renamed or removed is simply omitted (the SBOMs
    still carry the full component inventory), so this never fails the release.
    """
    try:
        with open(TOOLS_DOCKERFILE) as f:
            text = f.read()
    except OSError:
        return {}
    out: dict[str, str] = {}
    for arg, name in BUNDLED_TOOL_ARGS.items():
        m = re.search(rf"^ARG {arg}=(.+)$", text, re.M)
        if m:
            out[name] = m.group(1).strip()
    return out


def scan_platform(cache: str, outdir: str, scan_ref: str, short: str,
                  platform: str, use_platform: bool) -> dict:
    """Generate per-platform SBOMs + vuln summary; return the manifest entry.

    scan_ref is an immutable digest reference (repo@sha256:...) for a resolved
    multi-arch child, so the SBOM describes exactly the bytes recorded in the
    manifest even if the tag moves between resolve and scan. Only the non-index
    fallback scans by tag, pinning the arch with --platform.
    """
    arch = platform.split("/")[-1]
    base = f"{short}-{arch}"
    plat_args = ["--platform", platform] if use_platform else []
    trivy(cache, outdir, "image", "--quiet", *plat_args,
          "--format", "cyclonedx", "--output", f"/out/{base}.cdx.json", scan_ref)
    trivy(cache, outdir, "image", "--quiet", *plat_args,
          "--format", "spdx-json", "--output", f"/out/{base}.spdx.json", scan_ref)
    trivy(cache, outdir, "image", "--quiet", *plat_args,
          "--scanners", "vuln", "--severity", "CRITICAL,HIGH",
          "--format", "json", "--output", f"/out/{base}.vuln.json", scan_ref)

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
    # Absolute: outdir is bind-mounted into the Trivy container, and Docker
    # rejects a relative path as an invalid volume name.
    tag, outdir = sys.argv[1], os.path.abspath(sys.argv[2])
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
    tools = bundled_tools()
    for repo in IMAGE_REPOS:
        ref = f"{repo}:{tag}"
        short = repo.split("/")[-1]
        resolved = resolve_image(ref)
        if resolved is None:
            print(f"::warning::{ref} not visible after retries; skipping SBOM/manifest entry")
            continue
        index_digest, children = resolved
        # A published tag is normally a multi-arch index. If it is instead a
        # single manifest, resolve its real platform (never assume one) and scan
        # it by its own digest (== index_digest); skip if the platform is unknown.
        if children:
            platforms = children
        else:
            plat = single_platform(ref)
            if not plat:
                print(f"::warning::{ref} is a single manifest of unknown platform; skipping SBOM/manifest entry")
                continue
            platforms = {plat: index_digest}

        entry = {"index_digest": index_digest, "platforms": {}}
        if repo in BUNDLED_TOOL_IMAGES and tools:
            entry["bundled_tools"] = tools
        for platform, child_digest in sorted(platforms.items()):
            print(f"Generating SBOMs for {ref} ({platform})")
            # Scan the immutable child digest so the SBOM matches the manifest.
            if child_digest:
                scan_ref, use_platform = f"{repo}@{child_digest}", False
            else:
                scan_ref, use_platform = ref, True
            plat_entry = scan_platform(cache, outdir, scan_ref, short, platform, use_platform)
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

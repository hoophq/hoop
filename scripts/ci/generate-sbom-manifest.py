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

Trivy scans exact child digests with an immutable, digest-pinned scanner image.
The SBOM job intentionally has no Docker Hub credentials: all release images
are public, so a scanner compromise cannot expose a production push token.
The established gateway and fat-agent images are required; visibility or
platform gaps fail the job. The additive hoophq/hoopdev-minimal image may lag
or be absent and is skipped with a warning after bounded visibility retries.

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

TRIVY_IMAGE = "aquasec/trivy@sha256:cffe3f5161a47a6823fbd23d985795b3ed72a4c806da4c4df16266c02accdd6f"

# Core published line documented per release. The -ng clean-line images are a
# separate train / mirror and are intentionally out of scope here.
IMAGE_REPOS = ["hoophq/hoop", "hoophq/hoopdev", "hoophq/hoopdev-minimal"]

# The established gateway and fat-agent images are release requirements. The
# additive minimal image remains optional until its independent publish line
# succeeds.
REQUIRED_IMAGE_REPOS = {"hoophq/hoop", "hoophq/hoopdev"}
REQUIRED_PLATFORMS = {"linux/amd64", "linux/arm64"}

# Bundled CLI versions are OCI labels on each exact hoophq/hoopdev child image.
# New agent-tools bases provide inherited labels; the release workflow probes
# older pinned bases by immutable child digest and attaches the same schema.
# Never read versions from the checked-out Dockerfile.tools recipe: it can
# legitimately be newer than the agent-tools bytes Dockerfile.dev consumes.
BUNDLED_TOOL_LABELS = {
    "dev.hoop.agent-tools.kubectl.version": "kubectl",
    "dev.hoop.agent-tools.sqlcmd.version": "sqlcmd",
    "dev.hoop.agent-tools.mongosh.version": "mongosh",
    "dev.hoop.agent-tools.mongodb-tools.version": "mongodb-tools",
    "dev.hoop.agent-tools.node.version": "node",
    "dev.hoop.agent-tools.aws-cli.version": "aws-cli",
    "dev.hoop.agent-tools.gcloud.version": "gcloud",
    "dev.hoop.agent-tools.gke-gcloud-auth-plugin.version": "gke-gcloud-auth-plugin",
}
BUNDLED_TOOL_IMAGES = {"hoophq/hoopdev"}

# Docker Hub can lag before a just-pushed multi-arch manifest becomes visible;
# mirror the retry the release workflow already uses for freshly-pushed tags.
VISIBILITY_RETRIES = 5
VISIBILITY_DELAY_S = 10


def _run(cmd: list[str], **kw) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, check=False, **kw)


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




def bundled_tools_from_image(ref: str) -> dict[str, str]:
    """Read bundled-tool provenance labels from an exact image digest."""
    cp = _run(
        ["docker", "buildx", "imagetools", "inspect", ref,
         "--format", "{{json .Image.Config.Labels}}"],
        capture_output=True, text=True,
    )
    if cp.returncode != 0 or not cp.stdout.strip():
        return {}
    try:
        labels = json.loads(cp.stdout)
    except json.JSONDecodeError:
        return {}
    if not isinstance(labels, dict):
        return {}
    return {
        name: str(labels[label])
        for label, name in BUNDLED_TOOL_LABELS.items()
        if labels.get(label)
    }


def scan_platform(cache: str, outdir: str, scan_ref: str, short: str,
                  platform: str, use_platform: bool) -> dict:
    """Generate per-platform SBOMs + vuln summary; return the manifest entry.

    scan_ref is normally an immutable digest reference (repo@sha256:...), so
    the SBOM describes exactly the bytes recorded in the manifest even if the
    release tag moves. use_platform is only a defensive fallback for malformed
    index metadata that lacks a child digest.
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
    missing_required: list[str] = []
    for repo in IMAGE_REPOS:
        ref = f"{repo}:{tag}"
        short = repo.split("/")[-1]
        resolved = resolve_image(ref)
        if resolved is None:
            if repo in REQUIRED_IMAGE_REPOS:
                print(f"::error::{ref} not visible after retries; required release SBOM is incomplete")
                missing_required.append(ref)
            else:
                print(f"::warning::{ref} not visible after retries; skipping optional SBOM/manifest entry")
            continue
        index_digest, children = resolved
        if repo in REQUIRED_IMAGE_REPOS:
            available = {platform for platform, digest in children.items() if digest}
            missing_platforms = sorted(REQUIRED_PLATFORMS - available)
            if missing_platforms:
                print(f"::error::{ref} is missing required platforms: {', '.join(missing_platforms)}")
                missing_required.append(ref)
                continue
        # A published tag is normally a multi-arch index. If it is instead a
        # single manifest, resolve its real platform (never assume one) and scan
        # it by its own digest (== index_digest); skip if the platform is unknown.
        if children:
            platforms = children
        else:
            plat = single_platform(f"{repo}@{index_digest}")
            if not plat:
                print(f"::warning::{ref} is a single manifest of unknown platform; skipping optional SBOM/manifest entry")
                continue
            platforms = {plat: index_digest}

        entry = {"index_digest": index_digest, "platforms": {}}
        platform_tools: list[dict[str, str]] = []
        for platform, child_digest in sorted(platforms.items()):
            print(f"Generating SBOMs for {ref} ({platform})")
            # Scan the immutable child digest so the SBOM matches the manifest.
            if child_digest:
                scan_ref, use_platform = f"{repo}@{child_digest}", False
            else:
                scan_ref, use_platform = ref, True
            plat_entry = scan_platform(cache, outdir, scan_ref, short, platform, use_platform)
            if repo in BUNDLED_TOOL_IMAGES:
                tools = bundled_tools_from_image(scan_ref)
                if tools:
                    plat_entry["bundled_tools"] = tools
                    platform_tools.append(tools)
                else:
                    print(f"::error::{scan_ref} lacks bundled-tool provenance labels")
                    missing_required.append(f"{ref} ({platform}) bundled-tool labels")
            plat_entry["digest"] = child_digest
            entry["platforms"][platform] = plat_entry
        if platform_tools and len(platform_tools) == len(platforms):
            if all(tools == platform_tools[0] for tools in platform_tools[1:]):
                entry["bundled_tools"] = platform_tools[0]
            else:
                print(f"::warning::{ref} bundled-tool versions differ by platform; see platform entries")
        manifest["images"][ref] = entry
        produced = True

    if missing_required:
        print(
            "::error::required release artifacts incomplete: "
            + ", ".join(sorted(set(missing_required))),
            file=sys.stderr,
        )
        return 1

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

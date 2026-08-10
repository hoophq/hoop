#!/usr/bin/env python3
"""Download published Hoop release tarballs and verify them before use.

The weekly slim-agent rebuild (.github/workflows/hoopagent-weekly-rebuild.yml)
builds a customer-facing image out of an already-published release, so the
bytes it feeds into the image are a release input like any other and must be
authenticated before they are unpacked.

Two things have to be authentic: WHICH release is promoted, and the BYTES of
that release.

Which release: the newest published, non-draft, non-prerelease GitHub Release.
The distribution endpoint's latest.txt is deliberately not used for this. It is
mutable, so an attacker controlling it could name an older release whose bytes
still verify perfectly and have the weekly job roll `latest-minimal` back onto
a version with known-fixed vulnerabilities. Callers may also pass --version
explicitly, and --not-older-than refuses any target below a floor (the workflow
passes the version currently published, making a rollback impossible).

The bytes, in order of preference:

1. The GitHub Release asset for the tag. GitHub records an immutable SHA-256
   for every uploaded asset and serves it over the API, independently of the
   distribution endpoint the archive is normally served from. The asset is
   downloaded from GitHub and checked against that digest, so the archive and
   the digest come from the same authenticated source.
2. A reviewed pin in scripts/ci/legacy-release-checksums.sha256, for releases
   published before the workflow started uploading those assets. The archive
   then comes from the distribution endpoint and is checked against a digest
   that was reviewed into the repository, so a change at the endpoint alone
   cannot alter what is built.

There is deliberately no third case: a release with neither an asset digest nor
a reviewed pin fails, rather than being taken on trust from the endpoint.
"""

from __future__ import annotations

import argparse
import hashlib
import hmac
import json
import os
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path

RELEASES_BASE = "https://releases.hoop.dev/release"
GITHUB_API = "https://api.github.com"
REPOSITORY = "hoophq/hoop"

# Release archives are published under both the Go GOARCH spelling and the
# `uname -m` one. GitHub carries the uname-style names; Dockerfile.agent globs
# the GOARCH-style ones.
ARCHITECTURES = (("amd64", "x86_64"), ("arm64", "aarch64"))

VERSION_RE = re.compile(r"\A[0-9]+(?:\.[0-9]+)*\Z")
SHA256_RE = re.compile(r"\A[0-9a-f]{64}\Z")
DIGEST_PREFIX = "sha256:"
CHUNK = 1024 * 1024


class VerificationError(RuntimeError):
    """Raised when a release input cannot be authenticated."""


def http_get(url: str, *, accept: str | None = None, timeout: int = 300):
    request = urllib.request.Request(url)
    if accept:
        request.add_header("Accept", accept)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    if token and url.startswith(GITHUB_API):
        request.add_header("Authorization", f"Bearer {token}")
    return urllib.request.urlopen(request, timeout=timeout)


def version_key(version: str) -> tuple[int, ...]:
    return tuple(int(part) for part in version.split("."))


def resolve_latest_version() -> str:
    """Newest published release according to GitHub, not the endpoint.

    GitHub orders /releases by creation date and includes drafts and
    prereleases, so filter those out and pick the highest version among what
    remains rather than trusting position alone.
    """
    url = f"{GITHUB_API}/repos/{REPOSITORY}/releases?per_page=50"
    with http_get(url, accept="application/vnd.github+json") as response:
        releases = json.load(response)

    versions = [
        release["tag_name"]
        for release in releases
        if not release.get("draft")
        and not release.get("prerelease")
        and VERSION_RE.match(release.get("tag_name", ""))
    ]
    if not versions:
        raise VerificationError(
            f"no published release found for {REPOSITORY}; refusing to guess a version"
        )
    return max(versions, key=version_key)


def github_asset_digests(version: str) -> dict[str, tuple[str, str]]:
    """Map asset name -> (sha256, download URL) for one release tag.

    A missing release is not an error: releases published before the asset
    upload existed simply have no assets, and the caller falls back to the
    reviewed pins. A malformed digest IS an error — it means the anchor is
    there but unusable, and silently downgrading to a weaker check is exactly
    the failure this function exists to prevent.
    """
    url = f"{GITHUB_API}/repos/{REPOSITORY}/releases/tags/{version}"
    try:
        with http_get(url, accept="application/vnd.github+json") as response:
            release = json.load(response)
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            return {}
        raise

    assets: dict[str, tuple[str, str]] = {}
    for asset in release.get("assets", []):
        name = asset.get("name")
        digest = asset.get("digest") or ""
        if not name or not digest:
            continue
        if not digest.startswith(DIGEST_PREFIX):
            raise VerificationError(
                f"release asset {name!r} has an unsupported digest {digest!r}"
            )
        value = digest[len(DIGEST_PREFIX):]
        if not SHA256_RE.match(value):
            raise VerificationError(
                f"release asset {name!r} has a malformed SHA-256 {value!r}"
            )
        assets[name] = (value, asset["browser_download_url"])
    return assets


def load_pins(path: Path) -> dict[str, str]:
    """Read the reviewed compatibility pins.

    Same format as licenses/agent-tools-checksums.sha256 (`<sha256>  <name>`),
    kept separate because it pins release archives rather than third-party
    tool downloads.
    """
    if not path.exists():
        return {}
    pins: dict[str, str] = {}
    for number, raw in enumerate(path.read_text().splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        fields = line.split(maxsplit=1)
        if len(fields) != 2 or not SHA256_RE.match(fields[0]):
            raise VerificationError(f"{path}:{number}: invalid SHA-256 entry")
        digest, name = fields[0], fields[1].lstrip("*").strip()
        if not name or "/" in name or name in pins:
            raise VerificationError(f"{path}:{number}: invalid or duplicate artifact name")
        pins[name] = digest
    return pins


def download(url: str, destination: Path) -> str:
    digest = hashlib.sha256()
    destination.parent.mkdir(parents=True, exist_ok=True)
    # Write to a sibling temp file and rename only after the digest matches, so
    # a failed verification cannot leave a usable-looking archive behind for a
    # later step to pick up.
    staging = destination.with_suffix(destination.suffix + ".part")
    try:
        with http_get(url) as response, staging.open("wb") as handle:
            while True:
                chunk = response.read(CHUNK)
                if not chunk:
                    break
                digest.update(chunk)
                handle.write(chunk)
    except BaseException:
        staging.unlink(missing_ok=True)
        raise
    return digest.hexdigest()


def fetch_archive(
    version: str,
    goarch: str,
    uname_arch: str,
    assets: dict[str, tuple[str, str]],
    pins: dict[str, str],
    destination_dir: Path,
) -> dict[str, str]:
    name = f"hoop_{version}_Linux_{uname_arch}.tar.gz"
    if name in assets:
        expected, url, source = *assets[name], "github-release-asset"
    elif name in pins:
        expected, url, source = pins[name], f"{RELEASES_BASE}/{version}/{name}", "reviewed-pin"
    else:
        raise VerificationError(
            f"{name} has neither a GitHub release asset digest nor a reviewed pin; "
            f"refusing to build from an unauthenticated archive. Add the digest to "
            f"scripts/ci/legacy-release-checksums.sha256 after reviewing it, or "
            f"re-run the release so it uploads its assets."
        )

    # Dockerfile.agent globs the GOARCH spelling, so land it under that name.
    destination = destination_dir / f"hoop_{version}_Linux_{goarch}.tar.gz"
    staging = destination.with_suffix(destination.suffix + ".part")
    actual = download(url, destination)
    if not hmac.compare_digest(actual, expected):
        staging.unlink(missing_ok=True)
        raise VerificationError(
            f"SHA-256 mismatch for {name} from {source}: expected {expected}, got {actual}"
        )
    staging.replace(destination)
    print(f"{name}: SHA-256 verified ({source})")
    return {"name": name, "sha256": actual, "source": source, "path": str(destination)}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--version",
        help="release to fetch; defaults to the published latest",
    )
    parser.add_argument(
        "--dest",
        type=Path,
        default=Path("dist/binaries"),
        help="directory to write the verified archives into",
    )
    parser.add_argument(
        "--pins",
        type=Path,
        default=Path(__file__).with_name("legacy-release-checksums.sha256"),
        help="reviewed digests for releases predating GitHub asset uploads",
    )
    parser.add_argument(
        "--provenance-out",
        type=Path,
        help="write the verified version and digests here as JSON",
    )
    parser.add_argument(
        "--not-older-than",
        help=(
            "refuse to build a release older than this version. Pass the "
            "version currently published so a rebuild can never roll it back."
        ),
    )
    args = parser.parse_args()

    try:
        version = args.version or resolve_latest_version()
        if not VERSION_RE.match(version):
            raise VerificationError(f"unexpected release version {version!r}")
        floor = args.not_older_than
        if floor:
            if not VERSION_RE.match(floor):
                raise VerificationError(f"unexpected --not-older-than value {floor!r}")
            if version_key(version) < version_key(floor):
                raise VerificationError(
                    f"refusing to rebuild {version}: older than the currently "
                    f"published {floor}. A rebuild must never move a moving tag "
                    f"backwards."
                )
        assets = github_asset_digests(version)
        pins = load_pins(args.pins)
        archives = [
            fetch_archive(version, goarch, uname_arch, assets, pins, args.dest)
            for goarch, uname_arch in ARCHITECTURES
        ]
    except (VerificationError, OSError, urllib.error.URLError) as exc:
        print(f"release input verification failed: {exc}", file=sys.stderr)
        return 1

    provenance = {"version": version, "archives": archives}
    if args.provenance_out:
        args.provenance_out.parent.mkdir(parents=True, exist_ok=True)
        args.provenance_out.write_text(json.dumps(provenance, indent=2, sort_keys=True) + "\n")
    print(json.dumps(provenance, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

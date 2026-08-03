#!/usr/bin/env python3
"""Probe agent-tools versions or prepare exact metadata for a Hoop image build.

With no arguments, run this inside an agent-tools image to report the manually
bundled tool versions as JSON. ``--prepare-build`` runs on a CI host: it
resolves the pinned agent-tools tag to one platform child, executes this same
probe inside that exact digest with a locked-down container, and emits the
digest-bound build argument plus OCI labels through ``GITHUB_OUTPUT``.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

TOOL_NAMES = (
    "aws-cli",
    "gcloud",
    "gke-gcloud-auth-plugin",
    "kubectl",
    "mongodb-tools",
    "mongosh",
    "node",
    "sqlcmd",
)
VERSION_PATTERN = re.compile(r"[0-9A-Za-z][0-9A-Za-z.+:~_-]*")
AGENT_TOOLS_REPO = "hoophq/agent-tools"


def run(*args: str, timeout: int = 30) -> str:
    result = subprocess.run(
        args,
        check=False,
        capture_output=True,
        text=True,
        timeout=timeout,
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()
        raise RuntimeError(f"{' '.join(args)} exited {result.returncode}: {detail}")
    return (result.stdout or result.stderr).strip()


def package_version(name: str) -> str:
    return run("dpkg-query", "-W", "-f=${Version}", name)


def matched_version(pattern: str, output: str, tool: str) -> str:
    match = re.search(pattern, output)
    if not match:
        raise RuntimeError(f"could not parse {tool} version from: {output!r}")
    return match.group(1)


def report() -> dict[str, str]:
    kubectl = json.loads(run("kubectl", "version", "--client=true", "--output=json"))
    kubectl_version = kubectl.get("clientVersion", {}).get("gitVersion", "")
    if not kubectl_version:
        raise RuntimeError("kubectl did not report clientVersion.gitVersion")

    return {
        "aws-cli": matched_version(r"\baws-cli/([^\s]+)", run("aws", "--version"), "aws"),
        "gcloud": package_version("google-cloud-cli"),
        "gke-gcloud-auth-plugin": package_version(
            "google-cloud-sdk-gke-gcloud-auth-plugin"
        ),
        "kubectl": kubectl_version,
        "mongodb-tools": package_version("mongodb-org-tools"),
        "mongosh": package_version("mongodb-mongosh"),
        "node": run("node", "--version").removeprefix("v"),
        "sqlcmd": matched_version(
            r"\bVersion:\s*(v?[^\s]+)", run("sqlcmd", "--version"), "sqlcmd"
        ),
    }


def validate_versions(versions: object) -> dict[str, str]:
    if not isinstance(versions, dict) or set(versions) != set(TOOL_NAMES):
        actual = set(versions) if isinstance(versions, dict) else set()
        missing = sorted(set(TOOL_NAMES) - actual)
        unexpected = sorted(actual - set(TOOL_NAMES))
        raise RuntimeError(
            f"invalid tool set (missing={missing}, unexpected={unexpected})"
        )

    validated: dict[str, str] = {}
    for name in TOOL_NAMES:
        value = versions[name]
        if not isinstance(value, str) or not VERSION_PATTERN.fullmatch(value):
            raise RuntimeError(f"invalid {name} version: {value!r}")
        validated[name] = value
    return validated


def prepare_build(arch: str, github_output: Path) -> None:
    script = Path(__file__).resolve()
    dockerfile = script.parents[2] / "Dockerfile.dev"
    matches = re.findall(
        r"^ARG[ \t]+AGENT_TOOLS_TAG=([^ \t#]+)[ \t]*$",
        dockerfile.read_text(),
        flags=re.MULTILINE,
    )
    if len(matches) != 1 or not re.fullmatch(r"[0-9A-Za-z][0-9A-Za-z_.-]*", matches[0]):
        raise RuntimeError("Dockerfile.dev must declare exactly one valid AGENT_TOOLS_TAG")
    tag = matches[0]
    image = f"{AGENT_TOOLS_REPO}:{tag}"

    manifest = json.loads(
        run("docker", "buildx", "imagetools", "inspect", "--raw", image, timeout=120)
    )
    digests = [
        item.get("digest")
        for item in manifest.get("manifests", [])
        if item.get("platform", {}).get("os") == "linux"
        and item.get("platform", {}).get("architecture") == arch
    ]
    if len(digests) != 1 or not re.fullmatch(r"sha256:[0-9a-f]{64}", digests[0] or ""):
        raise RuntimeError(f"{image} does not resolve exactly one linux/{arch} child")
    digest = digests[0]
    exact_ref = f"{image}@{digest}"

    probe_output = run(
        "docker",
        "run",
        "--rm",
        "--platform",
        f"linux/{arch}",
        "--network",
        "none",
        "--read-only",
        "--user",
        "65534:65534",
        "--cap-drop",
        "ALL",
        "--security-opt",
        "no-new-privileges",
        "--pids-limit",
        "64",
        "--tmpfs",
        "/tmp:rw,noexec,nosuid,nodev,size=16m,uid=65534,gid=65534",
        "--env",
        "HOME=/tmp",
        "--volume",
        f"{script}:/probe.py:ro",
        "--entrypoint",
        "python3",
        exact_ref,
        "/probe.py",
        timeout=300,
    )
    versions = validate_versions(json.loads(probe_output))

    with github_output.open("a") as output:
        output.write(f"base={tag}@{digest}\n")
        output.write("labels<<AGENT_TOOL_LABELS\n")
        for name in TOOL_NAMES:
            output.write(f"dev.hoop.agent-tools.{name}.version={versions[name]}\n")
        output.write("AGENT_TOOL_LABELS\n")
    print(f"Prepared {exact_ref} metadata for linux/{arch}", file=sys.stderr)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--prepare-build", action="store_true")
    parser.add_argument("--arch", choices=("amd64", "arm64"))
    parser.add_argument("--github-output", type=Path)
    args = parser.parse_args()

    try:
        if args.prepare_build:
            if args.arch is None or args.github_output is None:
                parser.error("--prepare-build requires --arch and --github-output")
            prepare_build(args.arch, args.github_output)
        else:
            if args.arch is not None or args.github_output is not None:
                parser.error("--arch and --github-output require --prepare-build")
            versions = validate_versions(report())
            print(json.dumps(versions, sort_keys=True, separators=(",", ":")))
    except (json.JSONDecodeError, OSError, RuntimeError, subprocess.TimeoutExpired) as exc:
        print(f"agent-tools version probe failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

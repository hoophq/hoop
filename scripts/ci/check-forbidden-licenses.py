#!/usr/bin/env python3
"""Check Trivy license-scan JSON reports for forbidden licenses.

Reads one or more Trivy JSON reports (produced with `trivy image
--scanners license --format json`) and looks for AGPL-family and SSPL
licenses: the ones customer compliance scanners reject outright. Plain
GPL is expected on an Ubuntu base (bash, coreutils, ...) and is
deliberately not flagged.

Outputs:
- appends `found=true|false` to $GITHUB_OUTPUT (if set)
- writes a GitHub comment payload (JSON, `{"body": ...}`) to the path
  given by --comment-out: an alert with a findings table when forbidden
  licenses are present, or a short "resolved" note when they are not.

Usage:
    check-forbidden-licenses.py --comment-out comment.json \
        <image-name>=<trivy-report.json> [...]
"""

import argparse
import json
import os
import re
import sys

FORBIDDEN = re.compile(r"\b(AGPL|SSPL)\b", re.IGNORECASE)
MARKER = "<!-- trivy-license-scan -->"


def md_cell(value: str) -> str:
    """Neutralize markdown table metacharacters in untrusted metadata."""
    return (value.replace("\\", "\\\\").replace("`", "\\`")
            .replace("|", "\\|").replace("\n", " "))


def scan_report(path: str) -> list[tuple[str, str]]:
    with open(path) as fp:
        report = json.load(fp)
    return sorted({
        (lic["PkgName"], lic["Name"])
        for result in report.get("Results", [])
        for lic in result.get("Licenses", [])
        if FORBIDDEN.search(lic.get("Name", ""))
    })


def build_comment(findings: dict[str, list[tuple[str, str]]]) -> str:
    if findings:
        lines = [
            MARKER,
            "### :rotating_light: Forbidden licenses detected in preview images",
            "",
            "Trivy found AGPL/SSPL-licensed packages. These are rejected by",
            "customer compliance scanners and must not ship in our images.",
            "",
            "| Image | Package | License |",
            "|-------|---------|---------|",
        ]
        for image, hits in findings.items():
            for pkg, lic in hits:
                lines.append(
                    f"| `{md_cell(image)}` | `{md_cell(pkg)}` | `{md_cell(lic)}` |")
        lines += [
            "",
            "Most likely cause: a new apt package whose `Recommends` pulls",
            "them in — install it with `--no-install-recommends` (see the",
            "groff/ghostscript case in the Dockerfile.tools comments).",
        ]
    else:
        lines = [
            MARKER,
            "### :white_check_mark: License scan resolved",
            "",
            "A previous push introduced AGPL/SSPL-licensed packages; the",
            "latest preview images are clean.",
        ]
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--comment-out", required=True,
                        help="path to write the GitHub comment JSON payload")
    parser.add_argument("reports", nargs="+", metavar="IMAGE=REPORT.json",
                        help="image name and its Trivy JSON report path")
    args = parser.parse_args()

    findings: dict[str, list[tuple[str, str]]] = {}
    for spec in args.reports:
        image, sep, path = spec.partition("=")
        if not sep or not image or not path:
            parser.error(f"invalid report spec {spec!r}, expected IMAGE=PATH")
        hits = scan_report(path)
        if hits:
            findings[image] = hits

    github_output = os.environ.get("GITHUB_OUTPUT")
    if github_output:
        with open(github_output, "a") as out:
            out.write(f"found={'true' if findings else 'false'}\n")

    with open(args.comment_out, "w") as fp:
        json.dump({"body": build_comment(findings)}, fp)

    for image, hits in findings.items():
        for pkg, lic in hits:
            print(f"FORBIDDEN {image}: {pkg} ({lic})", file=sys.stderr)
    print(f"forbidden licenses found: {'yes' if findings else 'no'}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Pack/verify a versioned, target-specific ICA GPU runtime. No downloads or inference.

Input manifest.json supplies the compatibility metadata described in GPU.md.
The pack command adds a complete SHA256 inventory without changing the input.
Archives contain regular files only (materialize SONAME symlinks beforehand).
"""
import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import tarfile

REQUIRED = {
    "lib/libhoop-gpuframe.so", "lib/libturboocr-device.so",
    "engines/det.engine", "engines/rec.engine", "engines/dict.txt",
    "ort/lib/libonnxruntime.so", "ort/lib/libonnxruntime_providers_cuda.so",
    "ort/lib/libonnxruntime_providers_shared.so", "bin/hoop-gpuframe-smoke",
    "smoke/fixture.ppm", "smoke/expected.txt", "licenses/NOTICE",
}
METADATA = ("version", "libhoop_ica", "turboocr_revision", "cuda", "cudnn",
            "tensorrt", "ort", "compute_capability", "gpu_model", "min_driver")


def plain(name):
    p = PurePosixPath(name)
    if (not name or p.is_absolute() or ".." in p.parts or
            str(p) != name or any(c.isspace() for c in name) or "\\" in name):
        raise ValueError(f"unsafe asset path: {name!r}")
    return name


def metadata(m):
    if not isinstance(m, dict) or m.get("format") != 1 or m.get("arch") != "amd64":
        raise ValueError("GPU assets require format 1 and arch amd64")
    for key in METADATA:
        if not isinstance(m.get(key), str) or not m[key].strip():
            raise ValueError(f"manifest needs {key}")
    return m


def digest(stream):
    h = hashlib.sha256()
    for block in iter(lambda: stream.read(1024 * 1024), b""):
        h.update(block)
    return h.hexdigest()


def pack(root, output):
    if output.resolve().is_relative_to(root.resolve()):
        raise ValueError("archive output must be outside the input tree")
    m = metadata(json.loads((root / "manifest.json").read_text()))
    files = {}
    for p in sorted(root.rglob("*")):
        if p.is_symlink():
            raise ValueError(f"materialize symlink before packing: {p}")
        if p.is_dir():
            continue
        if not p.is_file():
            raise ValueError(f"not a regular file: {p}")
        name = plain(p.relative_to(root).as_posix())
        if name != "manifest.json":
            with p.open("rb") as stream:
                files[name] = digest(stream)
    if REQUIRED - files.keys():
        raise ValueError(f"missing assets: {sorted(REQUIRED - files.keys())}")
    m["files"] = files
    data = (json.dumps(m, indent=2, sort_keys=True) + "\n").encode()
    # Exclusive creation: never replace a prior published/tested artifact.
    with output.open("xb") as stream, tarfile.open(fileobj=stream, mode="w:gz") as tar:
        info = tarfile.TarInfo("manifest.json")
        info.size, info.mode = len(data), 0o644
        tar.addfile(info, io.BytesIO(data))
        for name in files:
            tar.add(root / name, arcname=name, recursive=False)


def verify(archive, engine=None, output=None):
    with tarfile.open(archive, "r:gz") as tar:
        members = {}
        for entry in tar:
            name = plain(entry.name)
            if not entry.isfile() or name in members:
                raise ValueError(f"non-regular or duplicate archive entry: {name}")
            members[name] = entry
        if "manifest.json" not in members or members["manifest.json"].size > 4 * 1024 * 1024:
            raise ValueError("missing or oversized manifest")
        m = metadata(json.load(tar.extractfile(members["manifest.json"])))
        if engine and m["libhoop_ica"] != engine:
            raise ValueError(f"libhoop-ica mismatch: {m['libhoop_ica']} != {engine}")
        files = m.get("files")
        if not isinstance(files, dict) or set(files) != set(members) - {"manifest.json"}:
            raise ValueError("manifest inventory does not match archive")
        if REQUIRED - files.keys():
            raise ValueError(f"missing assets: {sorted(REQUIRED - files.keys())}")
        for name, sha in files.items():
            if digest(tar.extractfile(members[name])) != sha:
                raise ValueError(f"SHA256 mismatch: {name}")
        if output is not None:
            output.mkdir(parents=True, exist_ok=False)
            for name, entry in members.items():
                p = output / name
                p.parent.mkdir(parents=True, exist_ok=True)
                with p.open("xb") as dst, tar.extractfile(entry) as src:
                    for block in iter(lambda: src.read(1024 * 1024), b""):
                        dst.write(block)
                p.chmod(0o755 if name.startswith("bin/") or entry.mode & 0o111 else 0o644)
        return m


def verify_tree(root):
    m = metadata(json.loads((root / "manifest.json").read_text()))
    actual = set()
    for p in root.rglob("*"):
        if p.is_symlink():
            raise ValueError(f"unexpected symlink: {p}")
        if not p.is_dir():
            actual.add(plain(p.relative_to(root).as_posix()))
    files = m.get("files", {})
    if not isinstance(files, dict) or actual != set(files) | {"manifest.json"} or REQUIRED - files.keys():
        raise ValueError("asset tree does not match manifest inventory")
    for name, sha in files.items():
        with (root / name).open("rb") as stream:
            if digest(stream) != sha:
                raise ValueError(f"SHA256 mismatch: {name}")
    return m


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("pack")
    p.add_argument("--root", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p = sub.add_parser("verify-tree")
    p.add_argument("root", type=Path)
    for command in ("verify", "unpack"):
        p = sub.add_parser(command)
        p.add_argument("archive", type=Path)
        p.add_argument("--engine", help="required libhoop-ica version")
        if command == "unpack":
            p.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "pack":
            pack(args.root, args.output)
        elif args.command == "verify-tree":
            verify_tree(args.root)
        else:
            m = verify(args.archive, args.engine, getattr(args, "output", None))
            print(f"verified GPU assets {m['version']}, {m['gpu_model']}, CUDA {m['cuda']}, TensorRT {m['tensorrt']}")
    except (ValueError, OSError, tarfile.TarError) as e:
        parser.exit(1, f"gpu-assets: {e}\n")


if __name__ == "__main__":
    main()

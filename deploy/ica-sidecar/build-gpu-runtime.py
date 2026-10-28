#!/usr/bin/env python3
"""Build and smoke-test the versioned GPU archive consumed by ICA sidecar assets.

Runs on a prepared Linux amd64 CUDA builder. No package installation, model
fetching, rental creation, publication, source edits, or CPU fallback. Work/output
paths must be new; failed build records are retained for diagnosis.
"""
import argparse
import ctypes
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("gpu_assets", HERE / "gpu-assets.py")
assets = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assets)

# Supplied by the host OS/driver. Never redistribute a builder's NVIDIA driver
# or glibc/loader into the runtime; the target driver injects its own libraries.
HOST_LIBRARY = re.compile(
    r"^(?:libcuda\.so(?:\.|$)|libnvidia-|ld-linux|linux-vdso|"
    r"lib(?:c|m|dl|pthread|rt|resolv|util|anl|BrokenLocale)\.so(?:\.|$)|libnss_)")
GPU_LIBRARY = re.compile(
    r"^lib(?:cudart|cublas|cudnn|cufft|curand|cusolver|cusparse|cupti|"
    r"nvrtc|nvJitLink|nvfatbin|nvjpeg|npp|nvinfer|nvonnxparser).*\.so(?:\.|$)")


def command(argv, env=None, cwd=None):
    return subprocess.run([str(a) for a in argv], check=True, text=True,
                          stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                          env=env, cwd=cwd).stdout


def pin():
    module = (HERE.parents[1] / "sidecar/ica/go.mod").read_text()
    match = re.search(r"github\.com/hoophq/libhoop-ica\s+(v\S+)", module)
    if not match:
        raise ValueError("cannot find the sidecar's libhoop-ica module pin")
    return match[1]


def source_revision(source, revision):
    actual = command(["git", "-C", source, "rev-parse", "HEAD"]).strip()
    expected = command(["git", "-C", source, "rev-parse", f"{revision}^{{commit}}"]).strip()
    if actual != expected:
        raise ValueError(f"source {source} HEAD {actual} does not match {revision}")
    # Ignore unrelated untracked files, but reject modifications to built code.
    command(["git", "-C", source, "diff", "--exit-code", "HEAD", "--"])
    submodules = command(["git", "-C", source, "submodule", "status", "--recursive"])
    if any(line.startswith(("-", "+", "U")) for line in submodules.splitlines()):
        raise ValueError(f"source {source} has uninitialized or mismatched submodules")
    return actual


def version(value):
    if not re.fullmatch(r"\d+(?:\.\d+)+", str(value)):
        raise ValueError(f"expected a numeric version, got {value!r}")
    return tuple(int(part) for part in value.split("."))


def defines(header, names):
    text = header.read_text()
    values = []
    for name in names:
        match = re.search(r"^\s*#\s*define\s+" + re.escape(name) + r"\s+(\d+)\b", text, re.M)
        if not match:
            raise ValueError(f"missing {name} in {header}")
        values.append(match[1])
    return ".".join(values)


def header(root, name):
    for path in (root / "include" / name, root / "include/x86_64-linux-gnu" / name):
        if path.is_file():
            return path
    raise ValueError(f"missing SDK header {name} under {root}")


def ort_version(library):
    # OrtGetApiBase exposes the library's version without initializing a model
    # or CUDA session. Do not trust a user-entered ORT version on its own.
    get_version = ctypes.CFUNCTYPE(ctypes.c_char_p)
    class ApiBase(ctypes.Structure):
        _fields_ = [("get_api", ctypes.c_void_p), ("get_version", get_version)]
    loaded = ctypes.CDLL(str(library))
    loaded.OrtGetApiBase.restype = ctypes.POINTER(ApiBase)
    return loaded.OrtGetApiBase().contents.get_version().decode("ascii")


def dependencies(output):
    """Return (SONAME, resolved file) pairs from ldd; fail on unresolved deps."""
    found = []
    for line in output.splitlines():
        if "=> not found" in line:
            raise ValueError(f"unresolved runtime dependency: {line.strip()}")
        match = re.match(r"\s*(\S+)\s+=>\s+(/\S+)\s+\(", line)
        if match:
            found.append((match[1], Path(match[2])))
            continue
        match = re.match(r"\s*(/\S+)\s+\(", line)
        if match:
            path = Path(match[1])
            found.append((path.name, path))
    return found


def copy_file(source, destination):
    source = source.resolve(strict=True)
    if not source.is_file():
        raise ValueError(f"not a regular input file: {source}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    if destination.exists():
        with source.open("rb") as src, destination.open("rb") as dst:
            if assets.digest(src) != assets.digest(dst):
                raise ValueError(f"conflicting runtime libraries named {destination.name}")
        return
    # Dereference SONAME symlinks so the archive can enforce regular files only.
    with source.open("rb") as src, destination.open("xb") as dst:
        shutil.copyfileobj(src, dst)
    destination.chmod(0o755 if os.access(source, os.X_OK) else 0o644)


def collect(root, seeds, search_dirs, env, run=command):
    queue = list(seeds)
    # cuDNN/NVRTC/TRT can load split libraries by name rather than DT_NEEDED.
    # Collect their runtime families explicitly as well as the ELF dependency
    # closure. Never sweep unrelated system libraries from /usr/lib.
    for directory in search_dirs:
        for path in sorted(directory.glob("*.so*")):
            if GPU_LIBRARY.match(path.name) and not HOST_LIBRARY.match(path.name):
                queue.append((path.name, path))
    visited = set()
    inventory = {}
    while queue:
        name, path = queue.pop(0)
        if HOST_LIBRARY.match(name):
            continue
        directory = "ort/lib" if name.startswith("libonnxruntime") else "lib"
        dest = root / directory / name
        copy_file(path, dest)
        inventory[str(dest.relative_to(root))] = str(path.resolve())
        identity = (name, path.resolve())
        if identity in visited:
            continue
        visited.add(identity)
        queue.extend(dependencies(run(["ldd", path], env=env)))
    return inventory


def relocate(root, run=command):
    for directory, rpath in ((root / "lib", "$ORIGIN:$ORIGIN/../ort/lib"),
                             (root / "ort/lib", "$ORIGIN:$ORIGIN/../../lib")):
        for path in sorted(directory.iterdir()):
            run(["patchelf", "--set-rpath", rpath, path])
    run(["patchelf", "--set-rpath", "$ORIGIN/../lib:$ORIGIN/../ort/lib", root / "bin/hoop-gpuframe-smoke"])


def check_closure(root, env, run=command):
    root = root.resolve()
    files = list((root / "lib").iterdir()) + list((root / "ort/lib").iterdir())
    files.append(root / "bin/hoop-gpuframe-smoke")
    for path in files:
        for name, resolved in dependencies(run(["ldd", path], env=env)):
            if not HOST_LIBRARY.match(name) and not resolved.resolve().is_relative_to(root):
                raise ValueError(f"runtime still depends on builder file {name}: {resolved}")


def build(args):
    if platform.system() != "Linux" or platform.machine() != "x86_64":
        raise ValueError("GPU runtime builds require Linux amd64 with a real CUDA GPU")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", args.version):
        raise ValueError("invalid runtime asset version")
    cfg = json.loads(args.config.read_text())
    revision = cfg["turboocr_revision"]
    if not re.fullmatch(r"[a-f0-9]{40}", revision):
        raise ValueError("turboocr_revision must be a full reviewed commit SHA")
    engine = pin()
    turbo_commit = source_revision(args.turboocr_source, revision)
    ica_commit = source_revision(args.ica_source, engine)
    # A fresh directory records every configure/build/test; never overwrite a
    # previous successful artifact or failed attempt.
    work = args.work_dir.resolve()
    work.mkdir(parents=True, exist_ok=False)
    root = work / "runtime"
    root.mkdir()
    env = os.environ.copy()
    env.pop("LD_PRELOAD", None)
    env.pop("LD_AUDIT", None)
    env["TMPDIR"] = str(work / "tmp")
    env["TRT_ENGINE_CACHE"] = str(work / "engine-cache")
    Path(env["TMPDIR"]).mkdir()
    Path(env["TRT_ENGINE_CACHE"]).mkdir()
    # Select by nvidia-smi ordinal, then use its UUID as CUDA's visibility
    # mask. This avoids confusing a physical ordinal with a remapped CUDA one.
    device = int(cfg.get("device", 0))
    gpu = command(["nvidia-smi", f"--id={device}", "--query-gpu=name,compute_cap,driver_version,uuid", "--format=csv,noheader"], env=env).strip().split(",")
    if len(gpu) != 4:
        raise ValueError("nvidia-smi did not report exactly one GPU")
    gpu_name, capability, driver, gpu_uuid = [item.strip() for item in gpu]
    env["CUDA_VISIBLE_DEVICES"] = gpu_uuid
    meta = dict(cfg["metadata"])
    if not isinstance(meta.get("gpu_model"), str) or not meta["gpu_model"].strip():
        raise ValueError("metadata.gpu_model is required")
    for key in ("cuda", "cudnn", "tensorrt", "ort", "min_driver", "compute_capability"):
        version(meta[key])
    if meta["compute_capability"] != capability or meta["gpu_model"] not in gpu_name:
        raise ValueError(f"GPU differs from the configured asset profile: {gpu_name}, sm {capability}")
    if version(driver) < version(meta["min_driver"]):
        raise ValueError("builder NVIDIA driver is below min_driver")
    sdk = {key: Path(cfg[key]).resolve(strict=True) for key in ("cuda_root", "tensorrt_root", "cudnn_root", "ort_root", "pdfium_root")}
    ortlib = sdk["ort_root"] / "lib"
    observed = {
        "cuda": defines(header(sdk["cuda_root"], "cuda.h"), ["CUDA_VERSION"]),
        "cudnn": defines(header(sdk["cudnn_root"], "cudnn_version.h"), ["CUDNN_MAJOR", "CUDNN_MINOR", "CUDNN_PATCHLEVEL"]),
        "tensorrt": defines(header(sdk["tensorrt_root"], "NvInferVersion.h"), ["NV_TENSORRT_MAJOR", "NV_TENSORRT_MINOR", "NV_TENSORRT_PATCH", "NV_TENSORRT_BUILD"]),
        "ort": ort_version(ortlib / "libonnxruntime.so"),
    }
    cuda_number = int(observed["cuda"])
    observed["cuda"] = f"{cuda_number // 1000}.{(cuda_number % 1000) // 10}"
    for key, actual in observed.items():
        if meta[key] != actual:
            raise ValueError(f"{key} SDK {actual} differs from configured {meta[key]}")
    dirs = [ortlib]
    for key in ("cuda_root", "cudnn_root", "tensorrt_root"):
        dirs.extend(sdk[key] / sub for sub in ("lib64", "lib/x86_64-linux-gnu", "lib", "targets/x86_64-linux/lib") if (sdk[key] / sub).is_dir())
    dirs.extend(Path(p).resolve(strict=True) for p in cfg.get("runtime_library_dirs", []))
    env["LD_LIBRARY_PATH"] = ":".join(str(p) for p in dirs)
    env["PATH"] = str(sdk["cuda_root"] / "bin") + os.pathsep + env.get("PATH", "")
    for required in ("libonnxruntime.so", "libonnxruntime_providers_cuda.so", "libonnxruntime_providers_shared.so"):
        if not (ortlib / required).is_file():
            raise ValueError(f"missing CUDA ORT library {required}")
    logs = work / "logs"
    logs.mkdir()
    def recorded(label, argv, active_env=env):
        try:
            result = command(argv, env=active_env, cwd=work)
        except subprocess.CalledProcessError as error:
            (logs / f"{label}.log").write_text(error.stdout or "")
            raise
        (logs / f"{label}.log").write_text(result)
        print(f"PASS {label}", flush=True)
        return result
    arch = capability.replace(".", "")
    turbo_build = work / "turboocr-build"
    recorded("configure-turboocr", ["cmake", "-S", args.turboocr_source, "-B", turbo_build,
        "-DCMAKE_BUILD_TYPE=Release", "-DNATIVE_ARCH=OFF", "-DUSE_CPU_ONLY=OFF", "-DFETCH_MODELS=OFF",
        "-DTURBO_BUILD_FASTPDF2PNG=OFF", "-DFETCHCONTENT_FULLY_DISCONNECTED=ON",
        f"-DCMAKE_CUDA_ARCHITECTURES={arch}", f"-DCUDAToolkit_ROOT={sdk['cuda_root']}",
        f"-DCMAKE_CUDA_COMPILER={sdk['cuda_root'] / 'bin/nvcc'}", f"-DTENSORRT_DIR={sdk['tensorrt_root']}",
        f"-DONNXRUNTIME_LIB={ortlib / 'libonnxruntime.so'}", f"-DONNXRUNTIME_INCLUDE_DIR={sdk['ort_root'] / 'include'}",
        f"-DONNXRUNTIME_CUDA_EP={ortlib / 'libonnxruntime_providers_cuda.so'}", f"-DTURBO_PDFIUM_DIR={sdk['pdfium_root']}"])
    recorded("build-turboocr", ["cmake", "--build", turbo_build, "--parallel", args.jobs, "--target", "turboocr-device", "turbo_ocr_tests"])
    recorded("test-device", [turbo_build / "turbo_ocr_tests", "[words_gpu][device]"])
    frame_build = work / "gpuframe-build"
    recorded("configure-gpuframe", ["cmake", "-S", args.ica_source / "native/gpuframe", "-B", frame_build,
        "-DCMAKE_BUILD_TYPE=Release", f"-DCMAKE_CUDA_ARCHITECTURES={arch}",
        f"-DCUDAToolkit_ROOT={sdk['cuda_root']}", f"-DCMAKE_CUDA_COMPILER={sdk['cuda_root'] / 'bin/nvcc'}",
        f"-DTURBOOCR_SOURCE_DIR={args.turboocr_source}", f"-DTURBOOCR_BUILD_DIR={turbo_build}"])
    recorded("build-gpuframe", ["cmake", "--build", frame_build, "--parallel", args.jobs, "--target", "hoop-gpuframe", "hoop-gpuframe-smoke"])
    seeds = [("libhoop-gpuframe.so", frame_build / "libhoop-gpuframe.so"), ("libturboocr-device.so", turbo_build / "libturboocr-device.so")]
    seeds.extend((p.name, p) for p in sorted(ortlib.glob("libonnxruntime.so*")))
    seeds.extend((name, ortlib / name) for name in ("libonnxruntime_providers_cuda.so", "libonnxruntime_providers_shared.so"))
    # ldd on the build products needs the freshly built device library too.
    env["LD_LIBRARY_PATH"] = str(turbo_build) + ":" + env["LD_LIBRARY_PATH"]
    seeds.extend(dependencies(command(["ldd", frame_build / "hoop-gpuframe-smoke"], env=env)))
    provenance = collect(root, seeds, dirs, env)
    copy_file(frame_build / "hoop-gpuframe-smoke", root / "bin/hoop-gpuframe-smoke")
    for name in ("det.engine", "rec.engine", "dict.txt"):
        copy_file(Path(cfg["engines_dir"]) / name, root / "engines" / name)
    if (Path(cfg["engines_dir"]) / "cls.engine").is_file():
        copy_file(Path(cfg["engines_dir"]) / "cls.engine", root / "engines/cls.engine")
    copy_file(Path(cfg["smoke_fixture"]), root / "smoke/fixture.ppm")
    copy_file(Path(cfg["smoke_expected"]), root / "smoke/expected.txt")
    expected = (root / "smoke/expected.txt").read_text().strip()
    if not expected:
        raise ValueError("synthetic smoke expected text is empty")
    licenses = Path(cfg["licenses_dir"])
    if not (licenses / "NOTICE").is_file():
        raise ValueError("licenses_dir needs NOTICE and the included components' license files")
    for path in licenses.rglob("*"):
        if path.is_file():
            copy_file(path, root / "licenses" / path.relative_to(licenses))
    copy_file(args.turboocr_source / "LICENSE", root / "licenses/TurboOCR-LICENSE")
    for name in ("LICENSE", "NOTICE"):
        copy_file(args.ica_source / name, root / "licenses" / ("libhoop-ica-" + name))
    relocate(root)
    isolated = env.copy()
    isolated["LD_LIBRARY_PATH"] = str(root / "lib") + ":" + str(root / "ort/lib")
    check_closure(root, isolated)
    smoke = [root / "bin/hoop-gpuframe-smoke", "--device", "0", "--det", root / "engines/det.engine",
             "--rec", root / "engines/rec.engine", "--dict", root / "engines/dict.txt", "--image", root / "smoke/fixture.ppm", "--expect", expected]
    if (root / "engines/cls.engine").exists():
        smoke.extend(["--cls", root / "engines/cls.engine"])
    for mode in ("conservative", "matches-only"):
        result = recorded("smoke-" + mode, smoke + ["--policy", mode], isolated)
        if "PASS:" not in result:
            raise ValueError("GPU smoke did not report PASS")
    meta.update(format=1, arch="amd64", version=args.version, libhoop_ica=engine,
                turboocr_revision=turbo_commit, gpu_model=gpu_name, driver_tested=driver,
                libhoop_ica_revision=ica_commit, glibc=platform.libc_ver()[1],
                validation={"device_tests": True, "conservative_smoke": True, "matches_only_smoke": True})
    (root / "manifest.json").write_text(json.dumps(meta, indent=2) + "\n")
    (root / "build-provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
    name = f"ica-gpu-runtime_{args.version}_Linux_amd64_sm{arch}.tar.gz"
    archive = work / name
    assets.pack(root, archive)
    assets.verify(archive, engine=engine)
    with archive.open("rb") as stream:
        sha = assets.digest(stream)
    (work / (name + ".sha256")).write_text(f"{sha}  {name}\n")
    print(f"GPU runtime asset: {archive}\nSHA256: {sha}", flush=True)
    return archive


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True, help="prepared builder SDK/model/profile JSON")
    parser.add_argument("--turboocr-source", type=Path, required=True)
    parser.add_argument("--ica-source", type=Path, required=True)
    parser.add_argument("--work-dir", type=Path, required=True, help="new retained build/output directory")
    parser.add_argument("--version", required=True)
    parser.add_argument("--jobs", default="4", help="bounded CMake parallelism")
    args = parser.parse_args()
    if not args.jobs.isdigit() or not 1 <= int(args.jobs) <= 256:
        parser.error("--jobs must be 1..256")
    args.turboocr_source = args.turboocr_source.resolve()
    args.ica_source = args.ica_source.resolve()
    try:
        build(args)
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"build-gpu-runtime: {error}; retained logs are under --work-dir\n")


if __name__ == "__main__":
    main()

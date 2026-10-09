#!/usr/bin/env python3
"""Package the Go binaries as platform wheels (stdlib only).

usage: build_wheels.py VERSION BIN_DIR OUT_DIR
BIN_DIR holds dokploytools-<os>-<arch>[.exe] as built by the release workflow.
"""
import base64, hashlib, os, sys, zipfile

PLATFORMS = {  # binary suffix -> wheel platform tag
    "linux-amd64": "manylinux_2_17_x86_64.manylinux2014_x86_64",
    "linux-arm64": "manylinux_2_17_aarch64.manylinux2014_aarch64",
    "darwin-amd64": "macosx_10_9_x86_64",
    "darwin-arm64": "macosx_11_0_arm64",
    "windows-amd64": "win_amd64",
}

LAUNCHER = '''import os, subprocess, sys

def main():
    exe = os.path.join(os.path.dirname(__file__), "bin", "dokploytools" + (".exe" if os.name == "nt" else ""))
    if os.name == "nt":
        sys.exit(subprocess.call([exe] + sys.argv[1:]))
    os.chmod(exe, 0o755)
    os.execv(exe, [exe] + sys.argv[1:])
'''


def build(version, bin_dir, out_dir):
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    readme = open(os.path.join(root, "README.md"), encoding="utf-8").read()
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    os.makedirs(out_dir, exist_ok=True)
    for key, plat in PLATFORMS.items():
        exe = "dokploytools-%s%s" % (key, ".exe" if key.startswith("windows") else "")
        src = os.path.join(bin_dir, exe)
        if not os.path.exists(src):
            sys.exit("missing binary: " + src)
        di = "dokploytools-%s.dist-info" % version
        meta = [
            "Metadata-Version: 2.1", "Name: dokploytools", "Version: " + version,
            "Summary: CLI to manage multiple Dokploy panels (binary build; alias: dpt)",
            "Requires-Python: >=3.8", "Description-Content-Type: text/markdown",
        ]
        if repo:
            meta.append("Project-URL: Source, https://github.com/" + repo)
        files = [  # (arcname, bytes, mode)
            ("dokploytools/__init__.py", LAUNCHER.encode(), 0o644),
            ("dokploytools/bin/" + ("dokploytools.exe" if key.startswith("windows") else "dokploytools"), open(src, "rb").read(), 0o755),
            (di + "/METADATA", ("\n".join(meta) + "\n\n" + readme).encode(), 0o644),
            (di + "/WHEEL", ("Wheel-Version: 1.0\nGenerator: build_wheels\nRoot-Is-Purelib: false\n" + "".join("Tag: py3-none-%s\n" % t for t in plat.split("."))).encode(), 0o644),
            (di + "/entry_points.txt", b"[console_scripts]\ndokploytools = dokploytools:main\ndpt = dokploytools:main\n", 0o644),
        ]
        record = []
        for name, data, _ in files:
            h = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
            record.append("%s,sha256=%s,%d" % (name, h, len(data)))
        record.append(di + "/RECORD,,")
        files.append((di + "/RECORD", ("\n".join(record) + "\n").encode(), 0o644))
        path = os.path.join(out_dir, "dokploytools-%s-py3-none-%s.whl" % (version, plat))
        with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
            for name, data, mode in files:
                zi = zipfile.ZipInfo(name, (2020, 1, 1, 0, 0, 0))
                zi.external_attr = (0o100000 | mode) << 16
                zi.compress_type = zipfile.ZIP_DEFLATED
                z.writestr(zi, data)
        print("built", path)


if __name__ == "__main__":
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    build(*sys.argv[1:])

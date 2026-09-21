#!/usr/bin/env python3
"""Authenticate release products, then exercise them on their native Linux architecture."""
import argparse
import hashlib
import itertools
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import subprocess
import tarfile
import time

REPOSITORY = "EpicBlackWolfZ/microfat"
PRODUCTS = ("microfat", "microfat-stub", "microfat-stub-minimal")
ARCHITECTURES = {"x86_64": "amd64", "aarch64": "arm64"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


class Commands:
    def __init__(self, output):
        self.output = output
        self.environment = {key: value for key, value in os.environ.items()
                            if not key.startswith("MICROFAT_") and key not in ("GOGC", "GOMEMLIMIT", "GOMAXPROCS")}

    def run(self, arguments, environment=None, success=True):
        arguments = list(map(str, arguments))
        overrides = environment or {}
        started = time.monotonic()
        try:
            result = subprocess.run(arguments, env=self.environment | overrides, capture_output=True,
                                    text=True, timeout=120, check=False)
        except subprocess.TimeoutExpired:
            self.record(dict(arguments=arguments, environment=overrides, timeout=True))
            raise
        self.record(dict(arguments=arguments, environment=overrides, exit=result.returncode,
                         seconds=time.monotonic() - started, stdout=result.stdout, stderr=result.stderr))
        require(result.returncode == 0 if success else result.returncode > 0,
                f"unexpected exit {result.returncode}: {arguments}")
        require("panic:" not in result.stderr and "fatal error:" not in result.stderr, "command crashed")
        return result.stdout

    def record(self, value):
        with (self.output / "commands.jsonl").open("a") as stream:
            stream.write(json.dumps(value) + "\n")


def authenticate(args, commands):
    identity = f"https://github.com/{REPOSITORY}/.github/workflows/release.yml@refs/tags/v{args.version}"
    commands.run(["cosign", "verify-blob", "--certificate-identity", identity,
                  "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
                  "--certificate-github-workflow-sha", args.source,
                  "--bundle", args.dist / "checksums.txt.sig", args.dist / "checksums.txt"])
    expected = {f"microfat_{args.version}_linux_{arch}.tar.gz{suffix}"
                for arch, suffix in itertools.product(("amd64", "arm64"), ("", ".spdx.json", ".cyclonedx.json"))}
    rows = [line.split() for line in (args.dist / "checksums.txt").read_text().splitlines() if line.strip()]
    require(len(rows) == len(expected) and all(len(row) == 2 for row in rows), "unexpected checksum inventory")
    require({row[1].removeprefix("*") for row in rows} == expected, "checksum names differ from release contract")
    verified = {}
    for checksum, name in rows:
        name = name.removeprefix("*")
        path = args.dist / name
        require(path.is_file() and not path.is_symlink(), f"not a regular release asset: {name}")
        require(re.fullmatch("[0-9a-f]{64}", checksum) and digest(path) == checksum, f"checksum mismatch: {name}")
        if name.endswith(".json"):
            document = json.loads(path.read_text())
            require(isinstance(document, dict), f"invalid SBOM object: {name}")
            if name.endswith(".cyclonedx.json"):
                require(document.get("bomFormat") == "CycloneDX" and document.get("specVersion") == "1.5",
                        f"wrong CycloneDX schema: {name}")
            else:
                require(document.get("spdxVersion") == "SPDX-2.3", f"wrong SPDX schema: {name}")
        verified[name] = checksum
    return verified


def extract(args):
    destination = args.output / "products"
    destination.mkdir()
    archive = args.dist / f"microfat_{args.version}_linux_{args.arch}.tar.gz"
    with tarfile.open(archive) as stream:
        members = stream.getmembers()
        names = [member.name for member in members]
        require(len(names) == len(set(names)), "duplicate archive entries")
        for member in members:
            path = PurePosixPath(member.name)
            require(member.isfile() and not path.is_absolute() and ".." not in path.parts,
                    f"unsafe archive entry: {member.name}")
        stream.extractall(destination, filter="data")
    for name in (*PRODUCTS, "README.md", "LICENSE", "SECURITY.md"):
        require((destination / name).is_file(), f"missing archive entry: {name}")
    for name in PRODUCTS:
        require((destination / name).stat().st_mode & 0o111, f"non-executable product: {name}")
    return destination


def build_payloads(args, commands, cli):
    detected = json.loads(commands.run([cli, "detect", "--json"]))
    require(detected["arch"] == args.arch, "downloaded CLI architecture differs from runner")
    baseline = "v1" if args.arch == "amd64" else "v8.0"
    source = args.output / "payload.go"
    source.write_text('''package main
import ("encoding/json"; "os"; "runtime")
var variant string
func main() {
 value := map[string]any{"arch":runtime.GOARCH,"variant":variant,"args":os.Args[1:],"origin":os.Getenv("MICROFAT_ORIGINAL_EXE")}
 if err := json.NewEncoder(os.Stdout).Encode(value); err != nil { panic(err) }
}
''')
    commands.run(["gofmt", "-w", source])
    variants = {}
    for level in dict.fromkeys((baseline, detected["level"])):
        path = args.output / f"payload-{level}"
        environment = {"GOOS": "linux", "GOARCH": args.arch, "CGO_ENABLED": "0",
                       "GOAMD64" if args.arch == "amd64" else "GOARM64": level}
        commands.run(["go", "build", "-buildvcs=false", f"-ldflags=-s -w -X main.variant={level}",
                      "-o", path, source], environment)
        variants[level] = path
    return detected, variants


def dispatch(commands, packed, detected, output):
    for mode in ("auto", "memfd", "cache"):
        environment = {"MICROFAT_EXEC_MODE": mode, "MICROFAT_CACHE_DIR": str(output / f"cache-{mode}")}
        for _ in range(2 if mode == "cache" else 1):
            value = json.loads(commands.run([packed, "argument with spaces", "*literal*"], environment))
            require(value["arch"] == detected["arch"] and value["variant"] == detected["level"], "wrong variant")
            require(value["args"] == ["argument with spaces", "*literal*"], "arguments changed")
            require(value["origin"] == str(packed), "wrong original executable hint")


def snapshot(directory):
    return {path.name: (path.stat().st_mode, digest(path)) for path in directory.iterdir()}


def full_operations(commands, packed, output, arch):
    commands.run([packed, "--microfat:info"])
    cache = output / "prewarm"
    environment = {"MICROFAT_CACHE_DIR": str(cache)}
    commands.run([packed, "--microfat:prewarm=all,json"], environment)
    before = snapshot(cache)
    commands.run([packed, "--microfat:prewarm=all,verify,json"], environment)
    require(snapshot(cache) == before, "cache verification changed files or permissions")
    native = output / "native"
    commands.run([packed, "--microfat:optimize-to=" + str(native)])
    require(json.loads(commands.run([native]))["arch"] == arch, "wrong optimized payload architecture")


def reject_corruption(commands, cli, packed, index, output):
    damaged = bytearray(packed.read_bytes())
    for variant in index["variants"]:
        damaged[variant["offset"]] ^= 0xFF
    corrupt = output / "corrupt"
    corrupt.write_bytes(damaged)
    corrupt.chmod(0o700)
    json.loads(commands.run([cli, "verify", corrupt, "--json"], success=False))
    for mode in ("memfd", "cache"):
        commands.run([corrupt], {"MICROFAT_EXEC_MODE": mode,
                                "MICROFAT_CACHE_DIR": str(output / "corrupt-cache")}, success=False)


def matrix(args, commands, products):
    cli = products / "microfat"
    for mode in ("auto", "memfd", "cache"):
        version = commands.run([cli, "--version"], {"MICROFAT_EXEC_MODE": mode,
                               "MICROFAT_CACHE_DIR": str(args.output / f"cli-cache-{mode}")})
        require(re.search(rf"(?<![\w.+-]){re.escape(args.version)}(?![\w.+-])", version), "wrong release version")
    detected, variants = build_payloads(args, commands, cli)
    results = []
    for version, profile, codec in itertools.product((1, 2), ("full", "minimal"), ("none", "lz4", "zstd")):
        for dictionary in ((False, True) if codec == "zstd" else (False,)):
            name = f"v{version}-{profile}-{codec}-dict{int(dictionary)}"
            output = args.output / name
            output.mkdir()
            packed = output / "packed"
            stub = products / ("microfat-stub" if profile == "full" else "microfat-stub-minimal")
            arguments = [cli, "pack", "--arch", args.arch, "--format-version", version,
                         "--compression", codec, "-o", packed]
            if (version, profile, codec) != (2, "full", "none"):
                arguments.extend(["--stub", stub])
            if dictionary:
                arguments.append("--dict")
            for level, path in variants.items():
                arguments.extend(["-v", f"{level}={path}"])
            commands.run(arguments)
            require(packed.read_bytes().startswith(stub.read_bytes()), "packed image did not use the downloaded stub")
            index = json.loads(commands.run([cli, "inspect", packed, "--json"]))
            require(index["version"] == version and index["arch"] == args.arch, "wrong packed format")
            json.loads(commands.run([cli, "verify", packed, "--json"]))
            dispatch(commands, packed, detected, output)
            if profile == "full":
                full_operations(commands, packed, output, args.arch)
            else:
                commands.run([packed, "--microfat:help"], success=False)
            reject_corruption(commands, cli, packed, index, output)
            results.append(dict(case=name, arch=args.arch, status="pass", packed_sha256=digest(packed)))
            (args.output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
            print(f"{args.arch}: {name} passed", flush=True)
    require(len(results) == 16, "incomplete native matrix")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    args = parser.parse_args()
    require(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", args.version), "invalid version")
    require(re.fullmatch("[0-9a-f]{40}", args.source), "invalid source SHA")
    require(platform.system() == "Linux" and ARCHITECTURES.get(platform.machine()) == args.arch,
            "audit requires a matching native Linux runner")
    args.dist, args.output = args.dist.resolve(), args.output.resolve()
    args.output.mkdir(parents=True, exist_ok=False)
    commands = Commands(args.output)
    verified = authenticate(args, commands)
    products = extract(args)
    require(commands.run(["go", "version"]).startswith("go version go1.27.1 "), "Go 1.27.1 required")
    record = dict(version=args.version, source=args.source, arch=args.arch, assets=verified,
                  products={name: digest(products / name) for name in PRODUCTS})
    (args.output / "verification.json").write_text(json.dumps(record, indent=2) + "\n")
    matrix(args, commands, products)


if __name__ == "__main__":
    main()

# SPDX-License-Identifier: AGPL-3.0-only
import os
import re
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path, PurePosixPath

LEGAL_FILE = re.compile(
    r"(?:licen[sc]es?|copying|copyrights?|notices?|patents|authors|contributors|third[-_ ]party[-_ ](?:notices?|licen[sc]es?))"
    r"(?:[._-].+)?",
    re.IGNORECASE,
)


def go(*args):
    return subprocess.check_output(["go", *args], text=True)


def dependency_licenses(binary, revision):
    goroot = Path(go("env", "GOROOT").strip())
    version = go("env", "GOVERSION").strip()
    cache = Path(go("env", "GOMODCACHE").strip()).resolve()
    modules = set()
    prefixes = {goroot: f"Go/{version}"}
    scopes = {goroot: {goroot}}
    for row in go("list", "-deps", "-mod=readonly", "-trimpath", "-f",
                  "{{if .Standard}}{{printf \"std\\t%s\" .Dir}}"
                  "{{else if .Module}}{{if not .Module.Main}}"
                  "{{if .Module.Replace}}replace{{else}}{{printf \"mod\\t%s\\t%s\\t%s\\t%s\" "
                  ".Module.Path .Module.Version .Module.Dir .Dir}}{{end}}{{end}}{{end}}", ".").splitlines():
        if not row:
            continue
        kind, *fields = row.split("\t")
        if kind == "std":
            directory = Path(fields[0])
            scopes[goroot].add(directory)
        elif kind == "mod":
            module, module_version, root, directory = fields
            root = Path(root)
            root.resolve().relative_to(cache)
            modules.add((module, module_version))
            prefixes[root] = f"{module}@{module_version}"
            scopes.setdefault(root, set()).add(Path(directory))
        else:
            raise SystemExit("Release dependencies must use versioned modules without replacements.")

    build_info = go("version", "-m", binary).splitlines()
    linked = {tuple(line.split()[1:3]) for line in build_info if line.startswith("\tdep\t")}
    if linked != modules or build_info[0].split()[-1] != version:
        raise SystemExit("The binary's dependency modules differ from go list.")

    files = {}
    for root, directories in scopes.items():
        ancestors = {root}
        for directory in directories:
            directory.relative_to(root)
            ancestors.add(directory)
            ancestors.update(parent for parent in directory.parents if root in parent.parents)
        for directory in ancestors:
            for file in directory.iterdir():
                if LEGAL_FILE.fullmatch(file.name) and file.is_file() and not file.is_symlink():
                    name = PurePosixPath("third-party", prefixes[root], file.relative_to(root).as_posix()).as_posix()
                    files[name] = file
        if not any(LEGAL_FILE.fullmatch(file.name) and file.name.lower().startswith(("licen", "copying"))
                   and file.parent == root for file in files.values()):
            raise SystemExit(f"Missing upstream license for {prefixes[root]}.")

    target = f"{os.environ['GOOS']}/{os.environ['GOARCH']}"
    inventory = "\n".join([
        f"Target: {target}",
        f"Go toolchain: {version}",
        f"Provider source: https://github.com/Webriot/terraform-provider-irisbeat/tree/{revision}",
        "Upstream modules (path and version):",
        *(f"{module} {module_version}" for module, module_version in sorted(linked)),
        "",
    ]).encode("utf-8")
    return files, inventory


def zip_entry(name, mode=0o644):
    entry = zipfile.ZipInfo(name)
    entry.create_system = 3
    entry.external_attr = (0o100000 | mode) << 16
    entry.compress_type = zipfile.ZIP_DEFLATED
    return entry


def main():
    archive, binary, revision = sys.argv[1:]
    files, inventory = dependency_licenses(binary, revision)
    files.update({name: Path(name) for name in (binary, "LICENSE", "NOTICE")})
    with zipfile.ZipFile(archive, "x") as release:
        release.writestr(zip_entry("third-party/dependencies.txt"), inventory)
        for name, file in sorted(files.items()):
            with file.open("rb") as source, release.open(zip_entry(name, 0o755 if name == binary else 0o644), "w") as target:
                shutil.copyfileobj(source, target)


if __name__ == "__main__":
    main()

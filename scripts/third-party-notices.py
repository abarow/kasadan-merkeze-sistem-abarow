#!/usr/bin/env python3
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

"""third-party-notices.py writes THIRD_PARTY_NOTICES, the licenses of the code that ships inside
abarow's images and screens, and checks that the file is current (docs/LICENSES.md).

    python3 scripts/third-party-notices.py            # rewrites THIRD_PARTY_NOTICES
    python3 scripts/third-party-notices.py --check    # exit 1 if the file is out of date or a
                                                      # dependency has a license that needs approval
    python3 scripts/third-party-notices.py --patroni-image abarow-patroni:dev
                                                      # also rereads the Patroni image's Python packages

What it covers:
  - the Go modules the services are built from (go list -deps, every module in go.work) and the
    Go standard library, with the license texts from the module cache;
  - the npm packages whose code the Vite build puts into the screens (UI_PACKAGES);
  - the Patroni image (deploy/container/patroni.Containerfile): kept between markers in the file, and
    rewritten only with --patroni-image (it needs Docker).

Writing needs the Go module cache and ui/apps/retail/node_modules (npm ci); --check needs the
modules and the lock files only. The licenses allowed without a decision are ALLOWED.
"""

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "THIRD_PARTY_NOTICES"
UI_APPS = ["headquarters", "restaurant", "retail", "store"]
# The packages whose code the build puts into the screens: svelte's runtime (with the two small
# packages it bundles) and Vite's own helper code (the modulepreload polyfill). The rest of
# svelte's and Vite's dependencies run only while building.
UI_PACKAGES = ["svelte", "esm-env", "clsx", "vite"]
ALLOWED = {"Apache-2.0", "MIT", "BSD-2-Clause", "BSD-3-Clause", "ISC"}
PATRONI_BEGIN = "<<<PATRONI-IMAGE-BEGIN>>>"
PATRONI_END = "<<<PATRONI-IMAGE-END>>>"
LINE = "-" * 79

PATRONI_PROBE = """
import importlib.metadata as m, json
out = []
for d in sorted(m.distributions(), key=lambda d: d.metadata["Name"].lower()):
    md = d.metadata
    out.append({"name": md["Name"], "version": md["Version"],
                "license": (md.get("License-Expression") or md.get("License") or "")[:60],
                "libs": sorted({str(f).split("/")[-1] for f in (d.files or []) if ".libs/" in str(f)})})
print(json.dumps(out))
"""


def kind(text):
    t = text[:6000]
    if "Apache License" in t and "Version 2.0" in t:
        return "Apache-2.0"
    if "Mozilla Public License" in t:
        return "MPL-2.0"
    if "GNU AFFERO" in t.upper():
        return "AGPL"
    if "GNU LESSER" in t.upper():
        return "LGPL"
    if "GNU GENERAL PUBLIC" in t.upper():
        return "GPL"
    if "Permission is hereby granted, free of charge" in t:
        return "MIT"
    if "Redistribution and use in source and binary forms" in t:
        return "BSD-3-Clause" if re.search(r"Neither the name|endorse or promote", t) else "BSD-2-Clause"
    if "Permission to use, copy, modify, and/or distribute" in t:
        return "ISC"
    return "UNKNOWN"


def read_text(path):
    return Path(path).read_text(encoding="utf-8", errors="replace").replace("\r\n", "\n").strip("\n")


def go_modules():
    """{module path: (version, directory)} of the external modules the services are built from."""
    dirs = subprocess.run(["go", "list", "-m", "-f", "{{.Dir}}"], cwd=ROOT, capture_output=True, text=True, check=True).stdout.split()
    mods = {}
    for d in dirs:
        r = subprocess.run(["go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}", "./..."],
                           cwd=d, capture_output=True, text=True, env={**os.environ, "GOWORK": "off"}, check=True)
        for line in r.stdout.splitlines():
            p = line.split("|")
            if len(p) == 3 and p[2] and not p[0].startswith("abarow.com"):
                mods[p[0]] = (p[1], p[2])
    return mods


def license_files(d):
    """{file name: text} of the license, notice and patent files at the root of a directory."""
    out = {}
    for f in sorted(os.listdir(d)):
        if re.match(r"(LICEN[SC]E|COPYING|NOTICE|PATENTS)", f, re.I) and os.path.isfile(os.path.join(d, f)):
            out[f] = read_text(os.path.join(d, f))
    return out


def npm_versions():
    """{package: (version, license)} from the lock files; the four apps must agree."""
    seen = {}
    for app in UI_APPS:
        lock = json.loads((ROOT / "ui" / "apps" / app / "package-lock.json").read_text(encoding="utf-8"))["packages"]
        for name in UI_PACKAGES:
            e = lock["node_modules/" + name]
            version, lic = seen.get(name, (e["version"], "UNKNOWN"))
            if version != e["version"]:
                sys.exit(f"{name} differs between the UI apps: {version} and {e['version']} ({app})")
            # not every lock file has the license field; one that has it is enough
            seen[name] = (version, lic if lic != "UNKNOWN" else e.get("license", "UNKNOWN"))
    return seen


def inventory(mods, npm):
    return sorted([f"go {p} {v}" for p, (v, _) in mods.items()] + [f"npm {n} {v}" for n, (v, _) in npm.items()])


def problems(mods, npm):
    out = []
    for p, (v, d) in sorted(mods.items()):
        files = license_files(d)
        lic = [t for f, t in files.items() if not f.upper().startswith(("NOTICE", "PATENTS"))]
        k = kind(lic[0]) if lic else "NONE"
        if k not in ALLOWED:
            out.append(f"go {p} {v}: license {k}")
    for n, (v, lic) in sorted(npm.items()):
        if lic not in ALLOWED:
            out.append(f"npm {n} {v}: license {lic}")
    return out


def group(items):
    """items: [(label, text)] -> [(text, [labels])], the order of the first appearance."""
    groups = {}
    for label, text in items:
        groups.setdefault(text, []).append(label)
    return list(groups.items())


def go_section(mods):
    goroot = subprocess.run(["go", "env", "GOROOT"], capture_output=True, text=True, check=True).stdout.strip()
    goversion = subprocess.run(["go", "version"], capture_output=True, text=True, check=True).stdout.split()[2]
    lines = ["1. GO STANDARD LIBRARY AND MODULES", "", f"Every service is a static Go program built with {goversion}; the standard library",
             "(BSD-3-Clause, The Go Authors) and these modules are inside it.", "", "Modules:"]
    items, notices = [], []
    for p, (v, d) in sorted(mods.items()):
        files = license_files(d)
        lic = [(f, t) for f, t in files.items() if not f.upper().startswith("NOTICE")]
        lines.append(f"  {p} {v}  [{kind(lic[0][1]) if lic else 'NONE'}]")
        for f, t in lic:
            items.append((f"{p} {v} ({f})", t))
        notices += [(f"{p} {v} ({f})", t) for f, t in files.items() if f.upper().startswith("NOTICE")]
    items.insert(0, (f"Go standard library ({goversion})", read_text(Path(goroot) / "LICENSE")))
    items.insert(1, (f"Go standard library ({goversion}) (PATENTS)", read_text(Path(goroot) / "PATENTS")))
    lines.append("")
    for text, labels in group(items):
        lines += [LINE, "Applies to:"] + [f"  - {x}" for x in labels] + [LINE, text, ""]
    lines += ["Notices that Apache-2.0 section 4(d) asks to be passed on (also in NOTICE):", ""]
    for label, text in notices:
        lines += [f"[{label}]", text, ""]
    return lines


def ui_section(npm):
    lines = ["2. SCREENS (npm packages bundled by the Vite build)", "",
             "The screens are built with Svelte and Vite; the code of these packages is in the bundles:", ""]
    lines += [f"  {n} {v}  [{lic}]" for n, (v, lic) in npm.items()]
    lines += ["", "The Inter font (SIL Open Font License 1.1) is listed in NOTICE; its license text is served with each",
              "screen (/licenses/Inter-OFL.txt).", ""]
    items = []
    for n in UI_PACKAGES:
        d = ROOT / "ui" / "apps" / "retail" / "node_modules" / n
        f = next(f for f in sorted(os.listdir(d)) if re.match(r"LICEN[SC]E", f, re.I))
        text = read_text(d / f)
        if n == "vite":  # the rest of the file is the licenses of the packages Vite bundles, which are not in the screens
            text = text.split("# Licenses of bundled dependencies")[0].strip("\n")
        items.append((f"{n} {npm[n][0]}", text))
    for text, labels in group(items):
        lines += [LINE, "Applies to:"] + [f"  - {x}" for x in labels] + [LINE, text, ""]
    return lines


def patroni_block(image):
    libs = []
    r = subprocess.run(["docker", "run", "--rm", "--entrypoint", "/opt/patroni/bin/python", image, "-c", PATRONI_PROBE],
                       capture_output=True, text=True, check=True)
    pkgs = json.loads(r.stdout)
    lines = ["3. PATRONI IMAGE (abarow-patroni)", "",
             "Built from docker.io/library/postgres:18-alpine (deploy/container/patroni.Containerfile). Besides abarow's own",
             "shell scripts (Apache-2.0) it holds the base image and these Python packages (as installed when the licenses",
             "were last read; Patroni and psycopg2-binary are pinned, the others are their dependencies). Each package",
             "carries its own license files inside the image, in the package's .dist-info folder.", ""]
    for p in pkgs:
        lic = p["license"].split("\n")[0] or "see its license file"
        lines.append(f"  {p['name']} {p['version']}  [{lic}]")
        libs += p["libs"]
    lines += ["", "Components with a copyleft license, and what that means here:", "",
              "  - psycopg2-binary: LGPL-3.0-or-later with the OpenSSL exception. It is a separate Python package in",
              "    /opt/patroni; it can be replaced with another build of psycopg2 (pip install in that folder). Its wheel carries",
              "    these shared libraries under their own licenses (libpq: PostgreSQL License; OpenSSL: Apache-2.0; MIT",
              "    Kerberos; OpenLDAP; Cyrus SASL): " + ", ".join(libs) + ".",
              "    The LGPL-3.0 text and the GPL-3.0 text it refers to are in /licenses/LGPL-3.0.txt and /licenses/GPL-3.0.txt",
              "    (deploy/container/patroni/licenses). Source: https://github.com/psycopg/psycopg2",
              "  - certifi: MPL-2.0, not modified. Source: https://github.com/certifi/python-certifi",
              "  - The Alpine Linux base: busybox (GPL-2.0-only), musl-utils (partly GPL-2.0-or-later), Python (PSF-2.0),",
              "    PostgreSQL (PostgreSQL License) and others, each under its own license. The source code of the Alpine",
              "    packages is at https://git.alpinelinux.org/aports and the source packages at https://dl-cdn.alpinelinux.org/alpine/;",
              "    the source of PostgreSQL is at https://www.postgresql.org/ftp/source/.", ""]
    return lines


def existing_patroni_block():
    if OUT.exists():
        t = OUT.read_text(encoding="utf-8")
        if PATRONI_BEGIN in t and PATRONI_END in t:
            return t.split(PATRONI_BEGIN + "\n")[1].split(PATRONI_END)[0].rstrip("\n").split("\n") + [""]
    sys.exit("THIRD_PARTY_NOTICES has no Patroni section yet: run once with --patroni-image <image>")


def render(mods, npm, patroni):
    head = ["abarow: THIRD-PARTY NOTICES", "",
            "abarow is under the Apache License 2.0 (LICENSE; NOTICE). The images and screens also hold the code of",
            "the projects below, each under its own license. This file reproduces their notices. It is made by",
            "scripts/third-party-notices.py (see docs/LICENSES.md) and ships in every image as /licenses/THIRD_PARTY_NOTICES.", ""]
    inv = ["INVENTORY (checked by scripts/third-party-notices.py --check)", ""] + ["  " + x for x in inventory(mods, npm)] + [""]
    parts = head + [LINE] + inv + [LINE, ""] + go_section(mods) + [LINE, ""] + ui_section(npm) + [LINE, "", PATRONI_BEGIN] + patroni + [PATRONI_END, ""]
    return "\n".join(parts)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--patroni-image")
    a = ap.parse_args()
    mods, npm = go_modules(), npm_versions()
    bad = problems(mods, npm)
    if bad:
        print("licenses that need a decision (allowed without one: " + ", ".join(sorted(ALLOWED)) + "):", file=sys.stderr)
        print("\n".join("  " + b for b in bad), file=sys.stderr)
        sys.exit(1)
    if a.check:
        have = []
        if OUT.exists():
            text = OUT.read_text(encoding="utf-8")
            have = [x.strip() for x in text.split("INVENTORY (checked")[1].split(LINE)[0].splitlines() if x.startswith("  ")] if "INVENTORY (checked" in text else []
        want = inventory(mods, npm)
        if have != want:
            print("THIRD_PARTY_NOTICES is out of date; run scripts/third-party-notices.py", file=sys.stderr)
            for x in sorted(set(want) - set(have)):
                print("  missing:", x, file=sys.stderr)
            for x in sorted(set(have) - set(want)):
                print("  not used any more:", x, file=sys.stderr)
            sys.exit(1)
        print(f"THIRD_PARTY_NOTICES is current ({len(want)} components)")
        return
    patroni = patroni_block(a.patroni_image) if a.patroni_image else existing_patroni_block()
    OUT.write_text(render(mods, npm, patroni), encoding="utf-8", newline="\n")
    print(f"wrote {OUT.name}: {len(mods)} Go modules, {len(npm)} npm packages, {OUT.stat().st_size // 1024} KB")


if __name__ == "__main__":
    main()

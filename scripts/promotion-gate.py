#!/usr/bin/env python3
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

"""promotion-gate.py is the gate of the promotion workflow (promotion.yml): until
its conditions are met no image is copied to a channel, signed, or announced.

What it checks (all must hold, otherwise the exit code is 1):
  1. The release workflow run (release.yml) is a successful "push" run that started
     from the v<version> tag.
  2. There is a CI (ci.yml) run for that commit, its latest run succeeded, and the
     jobs of the four end-to-end scenarios (standalone, headquarters, restaurant, update)
     succeeded. (The scenario list must equal the matrix in ci.yml; the test file checks this.)
  3. For every image digest being promoted, on every platform of the image
     (linux/amd64, linux/arm64) there are an SBOM (SPDX, a non-empty package list)
     and a provenance (SLSA) document, and the source commit in the provenance
     (the image.revision label or vcs:revision) is the commit of that release.

There is no option to bypass the gate. A release that cannot pass is fixed and
published again with a new tag.

Usage:
  scripts/promotion-gate.py --repo owner/repo --release-run 123456 --version 0.4.0 --digests directory
  (needs gh and docker buildx; gh with GH_TOKEN; docker logged in to ghcr.io.)
"""

import argparse
import json
import pathlib
import re
import subprocess
import sys

# The e2e matrix in ci.yml (scenario names); the test file checks they are equal.
E2E_SCENARIOS = ["standalone", "headquarters", "restaurant", "update"]
E2E_JOB = "End-to-end ({})"
PLATFORMS = ["linux/amd64", "linux/arm64"]
ATTESTATION = "attestation-manifest"
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")


def check_release_run(run: dict, repo: str, version: str) -> tuple[str, list[str]]:
    """Checks the release run; returns the commit and the errors."""
    errs: list[str] = []
    sha = str(run.get("head_sha") or "")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        errs.append("the commit of the release run could not be read")
    if str(run.get("path", "")).split("@")[0] != ".github/workflows/release.yml":
        errs.append(f"the run does not belong to the release workflow ({run.get('path')})")
    if run.get("status") != "completed" or run.get("conclusion") != "success":
        errs.append(f"the release run did not succeed (status {run.get('status')}, conclusion {run.get('conclusion')})")
    if run.get("event") != "push" or run.get("head_branch") != f"v{version}":
        errs.append(f"the run is not from a push of the v{version} tag (event {run.get('event')}, branch/tag {run.get('head_branch')})")
    if str((run.get("repository") or {}).get("full_name", repo)).lower() != repo.lower():
        errs.append("the run belongs to another repository")
    return sha, errs


def check_ci(runs: list[dict], jobs: list[dict] | None, sha: str) -> list[str]:
    """Checks the latest CI run for the commit and the e2e jobs."""
    if not runs:
        return [f"there is no CI run for commit {sha[:12]} (CI never ran on this commit)"]
    latest = max(runs, key=lambda r: (str(r.get("created_at", "")), int(r.get("id", 0))))
    errs: list[str] = []
    if latest.get("status") != "completed" or latest.get("conclusion") != "success":
        errs.append(f"CI run {latest.get('id')} did not succeed (status {latest.get('status')}, conclusion {latest.get('conclusion')})")
    by_name = {j.get("name"): j for j in (jobs or [])}
    for s in E2E_SCENARIOS:
        name = E2E_JOB.format(s)
        job = by_name.get(name)
        if job is None:
            errs.append(f"there is no '{name}' job in CI")
        elif job.get("conclusion") != "success":
            errs.append(f"'{name}' did not succeed in CI (conclusion {job.get('conclusion')})")
    return errs


def latest_ci_run_id(runs: list[dict]) -> int | None:
    if not runs:
        return None
    return int(max(runs, key=lambda r: (str(r.get("created_at", "")), int(r.get("id", 0)))).get("id", 0))


def normalize(doc, platforms: set[str], kind: str) -> dict:
    """Turns the imagetools output into a platform -> document dictionary (a single platform comes without a key)."""
    if not isinstance(doc, dict) or not doc:
        return {}
    if kind in doc:  # single platform: {"SPDX": {...}}
        return {next(iter(platforms)): doc} if len(platforms) == 1 else {}
    return {k: v for k, v in doc.items() if isinstance(v, dict)}


def find_values(node, key: str) -> list[str]:
    """Collects the values of the given key in a nested document (provenance versions keep it in different places)."""
    out: list[str] = []
    if isinstance(node, dict):
        for k, v in node.items():
            if k == key and isinstance(v, str):
                out.append(v)
            else:
                out += find_values(v, key)
    elif isinstance(node, list):
        for v in node:
            out += find_values(v, key)
    return out


def check_attestations(ref: str, index: dict, sbom, provenance, sha: str, repo: str) -> list[str]:
    """Checks the SBOM and provenance evidence of one image digest."""
    errs: list[str] = []
    if "manifests" not in index:
        return [f"{ref}: not a multi-platform image index (no SBOM/provenance attached)"]
    plats = {f"{(m.get('platform') or {}).get('os')}/{(m.get('platform') or {}).get('architecture')}"
             for m in index["manifests"] if (m.get("annotations") or {}).get("vnd.docker.reference.type") != ATTESTATION}
    image_digests = {m.get("digest") for m in index["manifests"]
                     if (m.get("annotations") or {}).get("vnd.docker.reference.type") != ATTESTATION}
    attested = {(m.get("annotations") or {}).get("vnd.docker.reference.digest") for m in index["manifests"]
                if (m.get("annotations") or {}).get("vnd.docker.reference.type") == ATTESTATION}
    if plats != set(PLATFORMS):
        errs.append(f"{ref}: platforms {sorted(plats)}, expected {PLATFORMS}")
    for d in sorted(image_digests - attested):
        errs.append(f"{ref}: there is no attestation manifest for {d[:19]}")

    sboms = normalize(sbom, plats, "SPDX")
    provs = normalize(provenance, plats, "SLSA")
    for p in sorted(plats):
        spdx = (sboms.get(p) or {}).get("SPDX")
        if not isinstance(spdx, dict) or not str(spdx.get("spdxVersion", "")).startswith("SPDX-"):
            errs.append(f"{ref} {p}: no SBOM (SPDX)")
        elif not spdx.get("packages"):
            errs.append(f"{ref} {p}: the SBOM has no packages")
        slsa = (provs.get(p) or {}).get("SLSA")
        if not isinstance(slsa, dict) or not slsa:
            errs.append(f"{ref} {p}: no provenance (SLSA)")
            continue
        # The commit comes from the label release.yml prints (label:org.opencontainers.image.revision)
        # or from buildx's local git info (vcs:revision); in real GitHub builds the
        # latter is not always present, so one of the two is enough, and all the values found must be equal.
        revs = set(find_values(slsa, "vcs:revision") + find_values(slsa, "label:org.opencontainers.image.revision"))
        if not revs:
            errs.append(f"{ref} {p}: the provenance has no source commit (vcs:revision or the image.revision label)")
        elif revs != {sha}:
            errs.append(f"{ref} {p}: the commit in the provenance is {sorted(r[:12] for r in revs)}, the commit of the release is {sha[:12]}")
        sources = find_values(slsa, "vcs:source") + find_values(slsa, "label:org.opencontainers.image.source")
        if sources and not any(repo.lower() in s.lower() for s in sources):
            errs.append(f"{ref} {p}: the provenance source {sources} is not this repository ({repo})")
    return errs


def run(cmd: list[str]) -> str:
    p = subprocess.run(cmd, capture_output=True, text=True, encoding="utf-8")
    if p.returncode != 0:
        raise RuntimeError(f"{' '.join(cmd[:4])}...: {p.stderr.strip() or p.stdout.strip()}")
    return p.stdout


def gh_api(path: str):
    return json.loads(run(["gh", "api", path]))


def main(argv: list[str]) -> int:
    for stream in (sys.stdout, sys.stderr):  # write the messages independent of the console encoding
        stream.reconfigure(encoding="utf-8", errors="replace")
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--repo", required=True)
    ap.add_argument("--release-run", required=True)
    ap.add_argument("--version", required=True)
    ap.add_argument("--digests", required=True)
    a = ap.parse_args(argv)
    if not re.fullmatch(r"\d+", a.release_run) or not re.fullmatch(r"\d+\.\d+\.\d+", a.version):
        print("::error::invalid version or run id")
        return 2

    errs: list[str] = []
    try:
        rel = gh_api(f"repos/{a.repo}/actions/runs/{a.release_run}")
        sha, e = check_release_run(rel, a.repo, a.version)
        errs += e
        if sha and not e:
            runs = gh_api(f"repos/{a.repo}/actions/workflows/ci.yml/runs?head_sha={sha}&per_page=100").get("workflow_runs", [])
            rid = latest_ci_run_id(runs)
            jobs = gh_api(f"repos/{a.repo}/actions/runs/{rid}/jobs?per_page=100&filter=latest").get("jobs", []) if rid else None
            errs += check_ci(runs, jobs, sha)
    except RuntimeError as ex:
        errs.append(f"GitHub query failed: {ex}")
        sha = ""

    files = sorted(pathlib.Path(a.digests).glob("*.json"))
    if not files:
        errs.append("there is no image record to promote")
    for f in files:
        rec = json.loads(f.read_text(encoding="utf-8"))
        if not DIGEST_RE.match(str(rec.get("digest", ""))) or not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+", str(rec.get("repo", ""))):
            errs.append(f"{f.name}: invalid record")
            continue
        ref = f"{rec['repo']}@{rec['digest']}"
        if not sha:
            continue
        try:
            index = json.loads(run(["docker", "buildx", "imagetools", "inspect", ref, "--raw"]))
            sbom = json.loads(run(["docker", "buildx", "imagetools", "inspect", ref, "--format", "{{json .SBOM}}"]))
            prov = json.loads(run(["docker", "buildx", "imagetools", "inspect", ref, "--format", "{{json .Provenance}}"]))
        except (RuntimeError, json.JSONDecodeError) as ex:
            errs.append(f"{rec.get('unit', f.name)}: the evidence could not be read: {ex}")
            continue
        errs += check_attestations(f"{rec.get('unit', f.name)} ({rec['digest'][:19]})", index, sbom, prov, sha, a.repo)

    if errs:
        for e in errs:
            print(f"::error::promotion gate: {e}")
        print(f"promotion gate CLOSED: {len(errs)} problems", file=sys.stderr)
        return 1
    print(f"promotion gate open: {len(files)} images, commit {sha[:12]}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

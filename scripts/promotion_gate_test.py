#!/usr/bin/env python3
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

"""Tests for promotion-gate.py. They need neither GitHub nor a registry; the input
formats are taken from real `docker buildx imagetools inspect` and GitHub API outputs.

Run: python3 scripts/promotion_gate_test.py
"""

import importlib.util
import pathlib
import re
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("promotion_gate", HERE / "promotion-gate.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)

SHA = "1dcd54d080f2d15d1c89f3d9c016d8c214721aa9"
REPO = "abarow/kasadan-merkeze-sistem-abarow"
AMD, ARM = "sha256:" + "a" * 64, "sha256:" + "b" * 64
ATT_AMD, ATT_ARM = "sha256:" + "c" * 64, "sha256:" + "d" * 64


def release_run(**over):
    run = {
        "head_sha": SHA, "path": ".github/workflows/release.yml", "status": "completed", "conclusion": "success",
        "event": "push", "head_branch": "v0.4.0", "repository": {"full_name": REPO},
    }
    run.update(over)
    return run


def ci_run(rid=10, conclusion="success", created="2026-11-06T10:00:00Z"):
    return {"id": rid, "status": "completed", "conclusion": conclusion, "created_at": created}


def ci_jobs(**over):
    jobs = {gate.E2E_JOB.format(s): "success" for s in gate.E2E_SCENARIOS}
    jobs["Go (gofmt, build, vet, test)"] = "success"
    jobs.update(over)
    return [{"name": n, "conclusion": c} for n, c in jobs.items() if c is not None]


def index(attest=True, platforms=("linux/amd64", "linux/arm64")):
    m = []
    digs = {"linux/amd64": (AMD, ATT_AMD), "linux/arm64": (ARM, ATT_ARM)}
    for p in platforms:
        os_, arch = p.split("/")
        m.append({"digest": digs[p][0], "platform": {"os": os_, "architecture": arch}})
    if attest:
        for p in platforms:
            m.append({"digest": digs[p][1], "platform": {"os": "unknown", "architecture": "unknown"},
                      "annotations": {"vnd.docker.reference.type": "attestation-manifest", "vnd.docker.reference.digest": digs[p][0]}})
    return {"manifests": m}


def sbom(platforms=("linux/amd64", "linux/arm64"), packages=15):
    return {p: {"SPDX": {"spdxVersion": "SPDX-2.3", "packages": [{"name": f"p{i}"} for i in range(packages)]}} for p in platforms}


def prov(platforms=("linux/amd64", "linux/arm64"), rev=SHA, source="https://github.com/abarow/kasadan-merkeze-sistem-abarow.git"):
    args = {"vcs:revision": rev, "vcs:source": source} if rev else {}
    doc = {"buildDefinition": {"externalParameters": {"request": {"root": {"request": {"args": args}}}}}, "runDetails": {}}
    return {p: {"SLSA": doc} for p in platforms}


class Release(unittest.TestCase):
    def test_ok(self):
        sha, errs = gate.check_release_run(release_run(), REPO, "0.4.0")
        self.assertEqual((sha, errs), (SHA, []))

    def test_rejects(self):
        for name, over in {
            "failed": {"conclusion": "failure"},
            "unfinished": {"status": "in_progress", "conclusion": None},
            "iptal": {"conclusion": "cancelled"},
            "other workflow": {"path": ".github/workflows/ci.yml"},
            "other tag": {"head_branch": "v0.3.9"},
            "branch, not tag": {"head_branch": "main", "event": "workflow_dispatch"},
            "other repository": {"repository": {"full_name": "baskasi/fork"}},
            "no commit": {"head_sha": ""},
        }.items():
            _, errs = gate.check_release_run(release_run(**over), REPO, "0.4.0")
            self.assertTrue(errs, name)

    def test_path_with_ref_suffix(self):
        _, errs = gate.check_release_run(release_run(path=".github/workflows/release.yml@refs/tags/v0.4.0"), REPO, "0.4.0")
        self.assertEqual(errs, [])


class CI(unittest.TestCase):
    def test_ok(self):
        self.assertEqual(gate.check_ci([ci_run()], ci_jobs(), SHA), [])

    def test_no_run(self):
        self.assertTrue(gate.check_ci([], None, SHA))

    def test_failed_run(self):
        self.assertTrue(gate.check_ci([ci_run(conclusion="failure")], ci_jobs(), SHA))

    def test_latest_run_counts(self):
        # An old successful run does not hide a new failed one; the reverse holds too.
        runs = [ci_run(1, "success", "2026-11-06T09:00:00Z"), ci_run(2, "failure", "2026-11-06T10:00:00Z")]
        self.assertTrue(gate.check_ci(runs, ci_jobs(), SHA))
        self.assertEqual(gate.latest_ci_run_id(runs), 2)
        runs = [ci_run(1, "failure", "2026-11-06T09:00:00Z"), ci_run(2, "success", "2026-11-06T10:00:00Z")]
        self.assertEqual(gate.check_ci(runs, ci_jobs(), SHA), [])

    def test_each_e2e_scenario_required(self):
        for s in gate.E2E_SCENARIOS:
            name = gate.E2E_JOB.format(s)
            self.assertTrue(gate.check_ci([ci_run()], ci_jobs(**{name: "failure"}), SHA), s)
            self.assertTrue(gate.check_ci([ci_run()], ci_jobs(**{name: "skipped"}), SHA), s)
            self.assertTrue(gate.check_ci([ci_run()], ci_jobs(**{name: None}), SHA), s)

    def test_scenarios_match_ci_workflow(self):
        text = (HERE.parent / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        m = re.search(r"scenario:\s*\[([^\]]*)\]", text)
        self.assertIsNotNone(m, "the scenario matrix was not found in ci.yml")
        self.assertEqual([s.strip() for s in m.group(1).split(",")], gate.E2E_SCENARIOS)
        self.assertIn("name: End-to-end (${{ matrix.scenario }})", text)


class Attestations(unittest.TestCase):
    def check(self, idx=None, sb=None, pv=None, sha=SHA):
        return gate.check_attestations("sales", idx or index(), sb if sb is not None else sbom(), pv if pv is not None else prov(), sha, REPO)

    def test_ok(self):
        self.assertEqual(self.check(), [])

    def test_missing_attestation_manifest(self):
        self.assertTrue(self.check(idx=index(attest=False)))

    def test_missing_platform(self):
        self.assertTrue(self.check(idx=index(platforms=("linux/amd64",)), sb=sbom(("linux/amd64",)), pv=prov(("linux/amd64",))))

    def test_not_an_index(self):
        self.assertTrue(self.check(idx={"config": {}, "layers": []}))

    def test_missing_or_empty_sbom(self):
        self.assertTrue(self.check(sb={}))
        self.assertTrue(self.check(sb={"linux/amd64": sbom()["linux/amd64"]}))  # arm64 eksik
        self.assertTrue(self.check(sb=sbom(packages=0)))
        self.assertTrue(self.check(sb={p: {"SPDX": {}} for p in ("linux/amd64", "linux/arm64")}))

    def test_missing_provenance(self):
        self.assertTrue(self.check(pv={}))
        self.assertTrue(self.check(pv={"linux/amd64": prov()["linux/amd64"]}))

    def test_provenance_without_revision(self):
        self.assertTrue(self.check(pv=prov(rev="")))

    def test_provenance_other_commit(self):
        errs = self.check(pv=prov(rev="f" * 40))
        self.assertTrue(errs)
        self.assertIn("commit", errs[0])

    def test_provenance_other_repo(self):
        self.assertTrue(self.check(pv=prov(source="https://github.com/baskasi/fork.git")))

    def test_single_platform_output_is_unkeyed(self):
        # For a single-platform image the imagetools output carries no platform key.
        idx = index(platforms=("linux/amd64",))
        errs = gate.check_attestations("x", idx, sbom(("linux/amd64",))["linux/amd64"], prov(("linux/amd64",))["linux/amd64"], SHA, REPO)
        # The gate of this project wants two platforms all the same.
        self.assertEqual(len(errs), 1)
        self.assertIn("platforms", errs[0])

    def test_revision_from_label_only(self):
        # In real GitHub builds vcs:revision may be missing; the label from release.yml is enough.
        def labeled(rev, src="https://github.com/abarow/kasadan-merkeze-sistem-abarow"):
            doc = {"buildDefinition": {"externalParameters": {"request": {"args": {
                "label:org.opencontainers.image.revision": rev, "label:org.opencontainers.image.source": src}}}}}
            return {p: {"SLSA": doc} for p in ("linux/amd64", "linux/arm64")}
        self.assertEqual(self.check(pv=labeled(SHA)), [])
        self.assertTrue(self.check(pv=labeled("f" * 40)))
        self.assertTrue(self.check(pv=labeled(SHA, "https://github.com/baskasi/fork")))

    def test_conflicting_revisions(self):
        doc = {"args": {"vcs:revision": SHA, "label:org.opencontainers.image.revision": "f" * 40}}
        self.assertTrue(self.check(pv={p: {"SLSA": doc} for p in ("linux/amd64", "linux/arm64")}))

    def test_provenance_v02_layout(self):
        # SLSA v0.2 keeps the source information under invocation.parameters; the search does not depend on the layout.
        v02 = {"SLSA": {"buildType": "x", "invocation": {"parameters": {"args": {"vcs:revision": SHA, "vcs:source": "https://github.com/abarow/kasadan-merkeze-sistem-abarow.git"}}}}}
        self.assertEqual(self.check(pv={p: v02 for p in ("linux/amd64", "linux/arm64")}), [])


class Main(unittest.TestCase):
    """main(): the gh and docker calls are faked; the gate behaves end to end."""

    def setUp(self):
        import json
        import tempfile
        self.json = json
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        (pathlib.Path(self.tmp.name) / "sales.json").write_text(
            json.dumps({"unit": "abarow-sales", "repo": "ghcr.io/abarow/abarow-sales", "digest": "sha256:" + "e" * 64}), encoding="utf-8")
        self.rel, self.runs, self.jobs, self.idx, self.sb, self.pv = release_run(), [ci_run()], ci_jobs(), index(), sbom(), prov()
        self.real_run = gate.run
        gate.run = self.fake
        self.addCleanup(lambda: setattr(gate, "run", self.real_run))

    def fake(self, cmd):
        j = self.json.dumps
        if cmd[0] == "gh":
            path = cmd[2]
            if "/actions/runs/" in path and path.endswith("/jobs?per_page=100&filter=latest"):
                return j({"jobs": self.jobs})
            if "/workflows/ci.yml/runs" in path:
                return j({"workflow_runs": self.runs})
            return j(self.rel)
        if "--raw" in cmd:
            return j(self.idx)
        return j(self.sb if ".SBOM" in cmd[-1] else self.pv)

    def run_main(self):
        return gate.main(["--repo", REPO, "--release-run", "99", "--version", "0.4.0", "--digests", self.tmp.name])

    def test_open(self):
        self.assertEqual(self.run_main(), 0)

    def test_closed_on_each_gap(self):
        for name, attr, val in [
            ("release failed", "rel", release_run(conclusion="failure")),
            ("ci yok", "runs", []),
            ("e2e failed", "jobs", ci_jobs(**{gate.E2E_JOB.format("headquarters"): "failure"})),
            ("sbom yok", "sb", {}),
            ("provenance yok", "pv", {}),
            ("no attestation manifest", "idx", index(attest=False)),
        ]:
            with self.subTest(name):
                setattr(self, attr, val)
                self.assertEqual(self.run_main(), 1)
                self.setUp()

    def test_bad_arguments(self):
        self.assertEqual(gate.main(["--repo", REPO, "--release-run", "x", "--version", "0.4.0", "--digests", self.tmp.name]), 2)
        self.assertEqual(gate.main(["--repo", REPO, "--release-run", "9", "--version", "../x", "--digests", self.tmp.name]), 2)

    def test_empty_digests_closed(self):
        with __import__("tempfile").TemporaryDirectory() as d:
            self.assertEqual(gate.main(["--repo", REPO, "--release-run", "9", "--version", "0.4.0", "--digests", d]), 1)


if __name__ == "__main__":
    unittest.main()

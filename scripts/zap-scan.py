#!/usr/bin/env python3
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

"""zap-scan.py scans the screens and APIs of the test stack with OWASP ZAP (dynamic scan;
docs/SECURITY-SCAN.md). It runs against the end-to-end stack with headquarters, never
against a real installation: the active scan writes junk data and tries attacks.

    bash scripts/e2e.sh up headquarters
    python3 scripts/zap-scan.py [--passive] [--out DIR] [register store headquarters]
    bash scripts/e2e.sh down headquarters

For every screen (the register's gateway, the store server, headquarters) it:
  1. signs in with the stack's test accounts (the register with a cashier's PIN and its
     token, the screens with their password and the session cookie) and gives ZAP the
     session in every request (the replacer); a probe request that answers 200 only with
     the session stops the scan if it did not work;
  2. requests every API route of the services behind that screen, taken from the code
     (the method patterns of http.ServeMux; a {parameter} becomes "zap1", a POST/PUT/PATCH
     body is "{}"), and spiders the screen: the screens are single-page applications and
     the spider alone finds no API; the writes with free text are sent once more with
     real bodies (BODIES), so that the active scan has fields to attack;
  3. runs the passive scan and, unless --passive is given, the active scan (at most
     ACTIVE_MINUTES per screen);
  4. writes ZAP's HTML and JSON reports into DIR (default zap-out/) and prints the alerts.

Sign-in, sign-out and the password change are left out of the scan: the active scan would
end the session, lock the test accounts (5 wrong tries) or change the password. ZAP runs in
Docker and reaches the stack through host.docker.internal (--host to change it).
"""

import argparse
import json
import os
import re
import subprocess
import sys
import urllib.request
from pathlib import Path

IMAGE = "ghcr.io/zaproxy/zaproxy:stable"
ACTIVE_MINUTES = 20
ROOT = Path(__file__).resolve().parent.parent

# The screens: port, a route that answers 200 only with the session (the probe), sign-in
# and the files whose routes are behind it (a screen also forwards to other services: the
# gateway to the retail module, the store server and headquarters to inventory).
TARGETS = {
    "register": {
        "port": 8091,
        "probe": "/api/shift",
        "login": {"cashier_id": "ayse", "pin": "1111"},
        "files": [
            "services/core/gateway/internal/httpapi/handler.go",
            "services/modules/retail/internal/adapters/in/httpapi/handler.go",
        ],
    },
    "store": {
        "port": 8090,
        "probe": "/api/registers",
        "login": {"password": "store-test"},
        "files": [
            "pkg/login/http.go",
            "services/store/internal/adapters/in/httpapi/handler.go",
            "services/store/internal/adapters/in/httpapi/office.go",
            "services/inventory/internal/adapters/in/httpapi/handler.go",
        ],
    },
    "headquarters": {
        "port": 9090,
        "probe": "/api/stores",
        "login": {"username": "headquarters", "password": "headquarters-test"},
        "files": [
            "pkg/login/http.go",
            "services/headquarters/reporting/internal/adapters/in/httpapi/handler.go",
            "services/headquarters/catalog/internal/adapters/in/httpapi/handler.go",
            "services/inventory/internal/adapters/in/httpapi/handler.go",
        ],
    },
}

# Requests with real bodies, sent after the routes: a "{}" body gives the active scan no field
# to attack. These are the writes with free text (names, supplier, reference) and numbers
# (price, quantity) the pentest preparation lists (docs/PENTEST-PREP.md); the ids are the
# seed data's (deploy/compose/seed) or are created by the request before.
STOCK = {"supplier": "Zap Tedarik", "ref": "ZAP-1", "to": "", "lines": [{"product_id": "ekmek", "qty_milli": 1000, "counted_milli": 0}]}
BODIES = {
    "register": [
        ("POST", "/api/module/baskets", {}),
        ("POST", "/api/module/baskets/zap1/items", {"barcode": "8690000000012"}),
    ],
    "store": [
        ("POST", "/api/cashiers", {"id": "zapcashier", "name": "Zap Kasiyer", "role": "cashier", "pin": "4321", "email": ""}),
        ("POST", "/api/cashiers/zapcashier", {"name": "Zap Kasiyer", "role": "cashier", "email": ""}),
        ("POST", "/api/prices/ekmek", {"unit_price_kurus": 1550}),
        ("POST", "/api/stock/receipts", STOCK),
        ("POST", "/api/stock/counts", STOCK),
    ],
    "headquarters": [
        ("POST", "/api/users", {"username": "zapuser", "password": "zap-test-password-1", "name": "Zap Kullanıcı", "role": "report", "all_stores": True, "stores": [], "email": ""}),
        ("POST", "/api/users/zapuser", {"name": "Zap Kullanıcı", "role": "report", "all_stores": True, "stores": [], "email": ""}),
        ("POST", "/api/catalog/products", {"code": "ZAP1", "name": "Zap Ürün", "unit": "piece", "tax_rate_bp": 2000, "barcodes": ["8690000099999"], "unit_price_kurus": 1000, "category": "", "station": ""}),
        ("POST", "/api/catalog/products/ekmek/prices", {"unit_price_kurus": 1600}),
        ("POST", "/api/stock/receipts", STOCK),
    ],
}

# Not requested and not scanned (see the docstring).
EXCLUDED = r".*/api/auth/(login|logout|password).*"  # for ZAP
EXCLUDED_ROUTE = re.compile(r"/api/auth/(login|logout|password)")
ROUTE = re.compile(r'Handle(?:Func)?\("(GET|POST|PUT|PATCH|DELETE) (/api/[^"]*)"')


def routes(files):
    """The (method, path) pairs registered in the files, the parameters filled in."""
    out = []
    for f in files:
        for method, path in ROUTE.findall((ROOT / f).read_text(encoding="utf-8")):
            path = re.sub(r"\{[^}]*\}", "zap1", path)
            if not EXCLUDED_ROUTE.search(path) and (method, path) not in out:
                out.append((method, path))
    return out


def sign_in(port, body):
    """Signs in on the local port; gives the header that carries the session."""
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}/api/auth/login",  # NOSONAR: the test stack is plain HTTP
        data=json.dumps(body).encode(),
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=10) as res:
        cookies = [c.split(";", 1)[0] for c in res.headers.get_all("Set-Cookie") or []]
        data = json.loads(res.read() or b"{}")
    if data.get("token"):
        return "Authorization", "Bearer " + data["token"]
    if cookies:
        return "Cookie", "; ".join(cookies)
    sys.exit(f"sign-in on port {port} gave neither a token nor a cookie")


def plan(name, base, probe, reqs, passive):
    """The ZAP automation plan of one screen (JSON is valid YAML). The session comes from the
    replacer only; the probe, sent first, shows that it works."""
    def req(m, path, data=None):
        if m not in ("POST", "PUT", "PATCH"):
            return {"url": base + path, "method": m}
        return {"url": base + path, "method": m, "headers": ["Content-Type:application/json"], "data": json.dumps(data or {}, ensure_ascii=False)}

    requests = [{"url": base + probe, "method": "GET", "responseCode": 200}]
    requests += [req(m, path) for m, path in reqs] + [req(m, path, data) for m, path, data in BODIES.get(name, [])]
    jobs = [
        {"type": "requestor", "requests": requests},
        {"type": "spider", "parameters": {"context": name, "maxDuration": 2}},
        {"type": "passiveScan-wait", "parameters": {"maxDuration": 5}},
    ]
    if not passive:
        jobs += [
            {"type": "activeScan", "parameters": {"context": name, "maxScanDurationInMins": ACTIVE_MINUTES, "threadPerHost": 4}},
            {"type": "passiveScan-wait", "parameters": {"maxDuration": 5}},
        ]
    for template, ext in (("traditional-html", "html"), ("traditional-json", "json")):
        jobs.append({"type": "report", "parameters": {"template": template, "reportDir": "/zap/wrk", "reportFile": f"{name}.{ext}"}})
    return {
        "env": {
            "contexts": [{"name": name, "urls": [base + "/"], "includePaths": [re.escape(base) + ".*"], "excludePaths": [EXCLUDED]}],
            "parameters": {"failOnError": False, "failOnWarning": False, "progressToStdout": True},
        },
        "jobs": jobs,
    }


def replacer(header):
    """ZAP options that put the session header into every request ZAP sends."""
    opts = {"description": "session", "enabled": "true", "matchtype": "REQ_HEADER", "matchstr": header[0], "regex": "false", "replacement": header[1]}
    out = []
    for k, v in opts.items():
        out += ["-config", f"replacer.full_list(0).{k}={v}"]
    return out


def summary(report):
    """The alerts of a JSON report: (risk, name, count, an example URL)."""
    rows = []
    for site in json.loads(report.read_text(encoding="utf-8")).get("site", []):
        for a in site.get("alerts", []):
            inst = a.get("instances", [])
            rows.append((a.get("riskdesc", ""), a.get("name", ""), len(inst), inst[0].get("uri", "") if inst else ""))
    order = {"High": 0, "Medium": 1, "Low": 2, "Informational": 3}
    return sorted(rows, key=lambda r: order.get(r[0].split(" ")[0], 9))


def main():
    p = argparse.ArgumentParser()
    p.add_argument("targets", nargs="*", default=list(TARGETS))
    p.add_argument("--passive", action="store_true", help="no active scan")
    p.add_argument("--out", default="zap-out")
    p.add_argument("--host", default="host.docker.internal")
    a = p.parse_args()
    out = Path(a.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    # ZAP writes as its own user (uid 1000); the folder holds the reports of the test stack only.
    os.chmod(out, 0o777)  # NOSONAR
    for name in a.targets:
        t = TARGETS[name]
        header = sign_in(t["port"], t["login"])
        reqs = routes(t["files"])
        base = f"http://{a.host}:{t['port']}"  # NOSONAR: the test stack is plain HTTP
        (out / f"plan-{name}.yaml").write_text(json.dumps(plan(name, base, t["probe"], reqs, a.passive), indent=1), encoding="utf-8")
        print(f"== {name}: {len(reqs)} routes, {'passive' if a.passive else 'active'} scan", flush=True)
        cmd = ["docker", "run", "--rm", "--add-host", "host.docker.internal:host-gateway", "-v", f"{out}:/zap/wrk:rw", IMAGE,
               "zap.sh", "-cmd", "-autorun", f"/zap/wrk/plan-{name}.yaml"] + replacer(header)
        with open(out / f"{name}.log", "w", encoding="utf-8") as log:
            subprocess.run(cmd, stdout=log, stderr=subprocess.STDOUT, check=False, env={**os.environ, "MSYS_NO_PATHCONV": "1"})
        if "Difference in response code values for message GET " + base + t["probe"] + " " in (out / f"{name}.log").read_text(encoding="utf-8"):
            sys.exit(f"the session did not reach ZAP's requests on {name} (the probe {t['probe']} did not answer 200); see {out / (name + '.log')}")
        report = out / f"{name}.json"
        if not report.exists():
            sys.exit(f"ZAP wrote no report for {name}; see {out / (name + '.log')}")
        for risk, alert, n, uri in summary(report):
            print(f"  {risk:<26} {n:>4}  {alert}  ({uri})")


if __name__ == "__main__":
    main()

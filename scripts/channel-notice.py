#!/usr/bin/env python3
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

"""channel-notice.py produces the (unsigned) channel notice of a release.

The release workflow (release.yml) uploads a {unit, repo, digest} record for
every image. An image can be tied to several Quadlet units (the same update
image runs at the register, the store server and headquarters under different
unit names); this script expands every record to the units under
deploy/quadlet that use that image. Third-party images (postgres, nats, caddy,
zot) are not images abarow publishes, so they do not enter the notice.

Usage:
  scripts/channel-notice.py --digests directory --channel prod --version 0.4.0 --seq 12       [--urgency security] [--notes "..."] [--valid-hours 720] > notice.json

`update-server sign-channel <role> <key> notice.json` signs the output.
"""

import argparse
import json
import pathlib
import re
import sys
from datetime import datetime, timedelta, timezone

IMAGE_LINE = re.compile(r"^Image=(?P<repo>ghcr\.io/[^:@\s]+):", re.M)


def units_by_repo(quadlet_dir: pathlib.Path) -> dict[str, list[str]]:
    out: dict[str, list[str]] = {}
    for f in sorted(quadlet_dir.rglob("abarow-*.container")):
        m = IMAGE_LINE.search(f.read_text(encoding="utf-8"))
        if m:
            out.setdefault(m["repo"], []).append(f.stem)
    return out


def build(digests: pathlib.Path, quadlet: pathlib.Path, channel: str, version: str, seq: int,
          urgency: str, notes: str, valid_hours: int, now: datetime) -> dict:
    by_repo = units_by_repo(quadlet)
    images = []
    for f in sorted(digests.glob("*.json")):
        rec = json.loads(f.read_text(encoding="utf-8"))
        for unit in by_repo.get(rec["repo"], []):
            images.append({"unit": unit, "repo": rec["repo"], "digest": rec["digest"]})
    if not images:
        raise SystemExit("no image matched a Quadlet unit")
    return {
        "channel": channel,
        "version": version,
        "seq": seq,
        "issued": now.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "expires": (now + timedelta(hours=valid_hours)).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "urgency": urgency,
        "notes": notes,
        "images": images,
    }


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--digests", required=True, type=pathlib.Path)
    ap.add_argument("--quadlet", default=pathlib.Path(__file__).resolve().parent.parent / "deploy" / "quadlet", type=pathlib.Path)
    ap.add_argument("--channel", required=True, choices=["test", "pilot", "prod"])
    ap.add_argument("--version", required=True)
    ap.add_argument("--seq", required=True, type=int)
    ap.add_argument("--urgency", default="normal", choices=["normal", "security"])
    ap.add_argument("--notes", default="")
    ap.add_argument("--valid-hours", default=720, type=int)
    a = ap.parse_args()
    m = build(a.digests, a.quadlet, a.channel, a.version, a.seq, a.urgency, a.notes, a.valid_hours, datetime.now(timezone.utc))
    json.dump(m, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()

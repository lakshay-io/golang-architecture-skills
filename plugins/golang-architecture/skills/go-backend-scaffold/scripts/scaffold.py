#!/usr/bin/env python3
"""Create a Go backend service from the standard architecture template.

    scaffold.py <dest> --module github.com/acme/policy-service [--name policy-service] [--force]

Copies assets/template into <dest>, replacing the template's module path
(example.com/goservice) and service name (goservice). <dest> may be an
existing repository: files that already exist are never overwritten unless
--force is given; they are listed so they can be merged by hand.
"""

import argparse
import pathlib
import re
import sys

TEMPLATE = pathlib.Path(__file__).resolve().parent.parent / "assets" / "template"
TEMPLATE_MODULE = "example.com/goservice"
TEMPLATE_NAME = "goservice"
SKIP_PARTS = {".DS_Store", "__pycache__", "bin", "coverage.out"}

MODULE_RE = re.compile(r"^[a-z0-9][a-z0-9.\-]*(/[A-Za-z0-9._~\-]+)+$")
NAME_RE = re.compile(r"^[a-z][a-z0-9\-]{1,62}$")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("dest", type=pathlib.Path, help="directory to create the service in")
    ap.add_argument("--module", required=True, help="Go module path, e.g. github.com/acme/policy-service")
    ap.add_argument("--name", help="service name (default: last element of --module)")
    ap.add_argument("--force", action="store_true", help="overwrite files that already exist in <dest>")
    args = ap.parse_args()

    name = args.name or args.module.rstrip("/").rsplit("/", 1)[-1]
    problems = []
    if not MODULE_RE.match(args.module):
        problems.append(f"--module {args.module!r} is not a valid module path (e.g. github.com/acme/policy-service)")
    if not NAME_RE.match(name):
        problems.append(f"service name {name!r} must be lowercase letters, digits and dashes (pass --name)")
    if not TEMPLATE.is_dir():
        problems.append(f"template not found at {TEMPLATE}")
    if problems:
        print("\n".join(f"error: {p}" for p in problems), file=sys.stderr)
        return 2

    dest = args.dest.expanduser().resolve()
    dest.mkdir(parents=True, exist_ok=True)
    service_word = re.compile(rf"\b{re.escape(TEMPLATE_NAME)}\b")

    created, skipped = [], []
    for src in sorted(TEMPLATE.rglob("*")):
        rel = src.relative_to(TEMPLATE)
        if src.is_dir() or SKIP_PARTS.intersection(rel.parts):
            continue
        out = dest / rel
        if out.exists() and not args.force:
            skipped.append(rel)
            continue
        out.parent.mkdir(parents=True, exist_ok=True)

        data = src.read_bytes()
        if src.name != "go.sum":  # checksums never mention our own module
            try:
                text = data.decode("utf-8")
            except UnicodeDecodeError:
                pass  # binary asset: copy as-is
            else:
                text = text.replace(TEMPLATE_MODULE, args.module)  # module path first…
                text = service_word.sub(name, text)  # …then the bare service name
                data = text.encode("utf-8")
        out.write_bytes(data)
        created.append(rel)

    print(f"Created {len(created)} files in {dest} (module {args.module}, service {name}).")
    if skipped:
        print("\nAlready existed, left untouched (merge by hand or re-run with --force):")
        for rel in skipped:
            print(f"  {rel}")
    print(f"\nNext:\n  cd {dest}\n  make verify          # gofmt, vet, race tests, build\n  cp .env.example .env && make run")
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Verification-only documentation checks for #12095. Never merge this branch."""
import argparse
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
CHECKS = ("test_doc_paths.py", "test_doc_go_paths.py", "test_doc_sections.py", "test_doc_wire.py")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("shard", choices=["other"])
    parser.add_argument("--database-file", required=True)
    parser.parse_args()
    status = 0
    for check in CHECKS:
        try:
            result = subprocess.run([sys.executable, "-B", "scripts/test/" + check], cwd=ROOT, timeout=180)
            code = result.returncode
        except subprocess.TimeoutExpired:
            code = 124
        print("DOC_CHECK=" + check + " EXIT=" + str(code), flush=True)
        if code and not status:
            status = code
    print("DOC_CHECKS_COMPLETED=" + str(len(CHECKS)), flush=True)
    return status


if __name__ == "__main__":
    raise SystemExit(main())

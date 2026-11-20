#!/usr/bin/env python3
"""Verification-only harness for #12095; this branch is never merged."""
import argparse
from collections import defaultdict, deque
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
REQUIRED = {
    "proto": ["TestStopCauseJSONAndResumeEligibility"],
    "transcript": ["TestClaudeAPIErrorKinds", "TestGatewayUsageLimitReset", "TestUserInterruptClearsGatewayRecovery"],
    "node": ["TestStopRPCPersistsItsProvenance", "TestAssistantGatewayQuotaIsRecovered",
             "TestGuardedResumeRejectsStaleRowsAndPermissionOverrides",
             "TestOwnerResumeStillUsesItsExplicitPathAfterUserStop",
             "TestResumeConsumesItsPreviousStopEvenWhenStartFails"],
    "assistant": ["TestGatewayLimitsUseExistingContinuation", "TestAssistantResumeProvenanceMatrix",
                  "TestAssistantResumeRejectsIneligibleProvenance", "TestUsageLimitIsContinuedAfterTheReset",
                  "TestUsageLimitLeftAlone"],
    "db": ["TestNodeSessionStopProvenancePersistence"],
}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("shard", choices=["other"])
    parser.add_argument("--database-file", required=True)
    args = parser.parse_args()
    wanted = {(package, name) for package, names in REQUIRED.items() for name in names}
    required_cases = {("node", "TestStopRPCPersistsItsProvenance/gateway/" + shape)
                      for shape in ("quota", "capacity")}
    pattern = "^(" + "|".join(re.escape(name) for _, name in sorted(wanted)) + ")$"
    command = [sys.executable, str(ROOT / "scripts/test/server_test_env.py"), "--database-file", args.database_file,
               "--", "go", "test", "-json", "-count=1", "-p", "1", "-timeout=4m", "-run", pattern]
    command += ["./internal/" + package for package in REQUIRED]
    process = subprocess.Popen(command, cwd=ROOT / "server", stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, text=True)
    passed = set()
    skipped = set()
    failed = []
    output = defaultdict(lambda: deque(maxlen=10))
    for line in process.stdout:
        try:
            event = json.loads(line)
        except ValueError:
            print(line, end="", flush=True)
            continue
        package = event.get("Package", "").rsplit("/", 1)[-1]
        test = event.get("Test", "")
        if event.get("Action") == "output":
            message = event.get("Output", "")
            print(message, end="", flush=True)
            output[(package, test)].append(message)
        if event.get("Action") == "pass" and (package, test) in wanted | required_cases:
            passed.add((package, test))
        if event.get("Action") == "skip" and (package, test.split("/", 1)[0]) in wanted:
            skipped.add((package, test))
        if event.get("Action") == "fail":
            failed.append((package, test))
    code = process.wait()
    if code:
        for package, test in failed:
            print("--- FAIL: " + package + " " + (test or "package"), flush=True)
            for message in output[(package, test)]:
                print(message, end="", flush=True)
        return code
    missing = (wanted | required_cases) - passed
    if missing or skipped:
        print("Missing actual passing regression functions: " + repr(sorted(missing)), file=sys.stderr)
        print("Skipped required regression cases: " + repr(sorted(skipped)), file=sys.stderr)
        return 2
    for package, test in sorted(passed):
        print("VERIFIED_PASS=" + package + "/" + test, flush=True)
    print("VERIFIED_REGRESSION_FUNCTIONS=" + str(len(wanted)), flush=True)
    print("VERIFIED_GATEWAY_SUBCASES=" + str(len(required_cases)), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

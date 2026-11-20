#!/usr/bin/env python3
"""Verification-only harness for #12095; this branch is never merged."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
REQUIRED = {
    "proto": ["TestStopCauseJSONAndResumeEligibility"],
    "transcript": ["TestClaudeAPIErrorKinds", "TestGatewayUsageLimitReset", "TestUserInterruptClearsGatewayRecovery"],
    "node": ["TestGatewayStoppedProvenance", "TestAssistantGatewayQuotaIsRecovered",
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
    pattern = "^(" + "|".join(re.escape(name) for _, name in sorted(wanted)) + ")$"
    command = [sys.executable, str(ROOT / "scripts/test/server_test_env.py"), "--database-file", args.database_file,
               "--", "go", "test", "-json", "-count=1", "-p", "1", "-timeout=4m", "-run", pattern]
    command += ["./internal/" + package for package in REQUIRED]
    process = subprocess.Popen(command, cwd=ROOT / "server", stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, text=True)
    passed = set()
    for line in process.stdout:
        print(line, end="", flush=True)
        try:
            event = json.loads(line)
        except ValueError:
            continue
        package = event.get("Package", "").rsplit("/", 1)[-1]
        test = event.get("Test", "")
        if event.get("Action") == "pass" and (package, test) in wanted:
            passed.add((package, test))
    code = process.wait()
    if code:
        return code
    missing = wanted - passed
    if missing:
        print("Missing actual passing regression functions: " + repr(sorted(missing)), file=sys.stderr)
        return 2
    print("VERIFIED_REGRESSION_FUNCTIONS=" + str(len(passed)), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

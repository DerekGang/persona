#!/bin/bash
set -euo pipefail
probe="$RUNNER_TEMP/persona-face-probe"
mkdir -p "$probe/App" "$probe/Tests" "$probe/shots"
cp .github/face-probe/FaceProbe.yml "$probe/project.yml"
cp .github/face-probe/ProbeApp.swift "$probe/App"
cp apple/Shared/Design/{Palette,PersonaFace}.swift apple/Shared/StatusPalette.swift "$probe/App"
cp apple/Persona/Views/Assistant/PersonaFaceMotion.swift "$probe/App"
cp apple/PersonaTests/PersonaFaceMotionPixelTests.swift .github/face-probe/diagnose.swift "$probe/Tests"
if [[ "${PERSONA_FACE_PROBE_MATCH_SCALE:-no}" = yes ]]; then
  python3 - "$probe/Tests/PersonaFaceMotionPixelTests.swift" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
source = path.read_text()
window_anchor = '            window.overrideUserInterfaceStyle ='
assert source.count(window_anchor) == 1
source = source.replace(window_anchor, '            window.traitOverrides.displayScale = scale\n' + window_anchor)
capture_anchor = '            return try captureOnce()'
assert source.count(capture_anchor) == 1
source = source.replace(capture_anchor, '            #expect(host.view.traitCollection.displayScale == scale)\n' + capture_anchor)
path.write_text(source)
PY
fi
xcodegen generate --spec "$probe/project.yml" --project "$probe"

runtime_json="$probe/runtimes.json"
version=${PERSONA_FACE_PROBE_VERSION:-18.6}
get_runtime() {
  xcrun simctl list runtimes -j > "$runtime_json"
  PYTHONPATH=scripts/ci python3 - "$runtime_json" "$version" <<'PY'
import json, sys
from apple_runtime import select
with open(sys.argv[1]) as handle:
    match = select(json.load(handle)["runtimes"], "iOS", sys.argv[2])
if match is None:
    sys.exit(1)
print(match)
PY
}
if ! runtime=$(get_runtime); then
  xcodebuild -downloadPlatform iOS -buildVersion "$version"
  runtime=$(get_runtime)
fi
device_type=com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro
if [[ "$version" = 26.2 ]]; then device_type=com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro; fi
if [[ "${PERSONA_FACE_PROBE_TABLET:-no}" = yes ]]; then
  device_type=com.apple.CoreSimulator.SimDeviceType.iPad-10th-generation
fi
device=$(xcrun simctl create 'Persona Face Probe' "$device_type" "$runtime")
cleanup() {
  xcrun simctl shutdown "$device" >/dev/null 2>&1 || true
  xcrun simctl delete "$device" >/dev/null 2>&1 || true
}
trap cleanup EXIT
xcrun simctl boot "$device"
export TEST_RUNNER_PERSONA_TEST_ARTIFACTS="$probe/shots"
xcodebuild test -project "$probe/FaceProbe.xcodeproj" -scheme FaceProbe \
  -destination "id=$device" -derivedDataPath "$probe/derived" -resultBundlePath "$probe/result.xcresult" \
  -parallel-testing-enabled NO CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual \
  DEVELOPMENT_TEAM= PROVISIONING_PROFILE_SPECIFIER= "ARCHS=$(uname -m)" ONLY_ACTIVE_ARCH=YES \
  2>&1 | tee "$probe/face-test.log"

#!/bin/bash
set -euo pipefail
probe="$RUNNER_TEMP/persona-face-probe"
mkdir -p "$probe/App" "$probe/Tests" "$probe/shots"
cp .github/face-probe/FaceProbe.yml "$probe/project.yml"
cp .github/face-probe/ProbeApp.swift "$probe/App"
cp apple/Shared/Design/{Palette,PersonaFace}.swift apple/Shared/StatusPalette.swift "$probe/App"
cp apple/Persona/Views/Assistant/PersonaFaceMotion.swift "$probe/App"
cp apple/PersonaTests/PersonaFaceMotionPixelTests.swift .github/face-probe/diagnose.swift "$probe/Tests"
xcodegen generate --spec "$probe/project.yml" --project "$probe"

runtime_json="$probe/runtimes.json"
xcrun simctl list runtimes -j > "$runtime_json"
if ! runtime=$(python3 scripts/ci/apple_runtime.py "$runtime_json" --version 18.6); then
  xcodebuild -downloadPlatform iOS -buildVersion 18.6
  xcrun simctl list runtimes -j > "$runtime_json"
  runtime=$(python3 scripts/ci/apple_runtime.py "$runtime_json" --version 18.6)
fi
device=$(xcrun simctl create 'Persona Face Probe' com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro "$runtime")
cleanup() {
  xcrun simctl shutdown "$device" >/dev/null 2>&1 || true
  xcrun simctl delete "$device" >/dev/null 2>&1 || true
}
trap cleanup EXIT
export TEST_RUNNER_PERSONA_TEST_ARTIFACTS="$probe/shots"
xcodebuild test -project "$probe/FaceProbe.xcodeproj" -scheme FaceProbe \
  -destination "id=$device" -derivedDataPath "$probe/derived" -resultBundlePath "$probe/result.xcresult" \
  -parallel-testing-enabled NO CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual \
  DEVELOPMENT_TEAM= PROVISIONING_PROFILE_SPECIFIER= "ARCHS=$(uname -m)" ONLY_ACTIVE_ARCH=YES \
  2>&1 | tee "$probe/face-test.log"

#!/usr/bin/python3
"""Run one read-only structured reviewer in an isolated Herdr tab.

Usage: herdr_review.py CONFIG -- [CLI_ARG ...]

The watcher owns semantic validation. This helper only transports the fixed
prompt and interactive terminal result, allocates bounded slots, and reclaims
its own tab and scope. It does not start, stop or supervise Herdr itself.
"""
import atexit
import fcntl
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import signal
import subprocess
import sys
import tempfile
import time
import uuid

_cleanup_tab = None
_cleanup_scope = None
_terminated = False
_config = None


def command(*args, timeout=90):
    result = subprocess.run(
        [_config["herdr_command"], "--session", _config["session"], *args],
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, timeout=timeout, check=False,
    )
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        if result.returncode == 0 and not result.stdout.strip() and args[:2] in (("pane", "run"), ("pane", "send-keys")):
            return {}
        raise RuntimeError("Herdr did not return JSON (%s %s): %s"
                           % (args[:2], result.returncode, (result.stderr or result.stdout)[-500:])) from error
    if not isinstance(value, dict):
        raise RuntimeError("Herdr returned an unexpected response")
    if result.returncode or "error" in value:
        detail = value.get("error", {})
        raise RuntimeError(str(detail.get("message") or detail.get("code") or result.stderr[-500:]))
    return value.get("result", value)


def interactive_command(launcher, flags):
    """Translate the watcher's validated print-only request to a visible agent.

    Interactive mode cannot use --json-schema; the watcher still validates the
    returned object and every changed-line finding. Read/plan/safe mode keep the
    terminal reviewer within the same tool boundary as the one-shot transport.
    """
    options_with_values = {"--json-schema", "--tools", "--setting-sources", "--permission-mode"}
    launch_flags = []
    schema = None
    index = 0
    while index < len(flags):
        flag = flags[index]
        if flag in {"-p", "--output-format"}:
            if flag == "--output-format":
                if index + 1 >= len(flags) or flags[index + 1] != "json":
                    raise RuntimeError("interactive reviewer requires JSON output")
                index += 1
            index += 1
            continue
        if flag in options_with_values:
            if index + 1 >= len(flags):
                raise RuntimeError("missing reviewer option argument")
            if flag == "--json-schema":
                schema = flags[index + 1]
            else:
                launch_flags.extend(flags[index:index + 2])
            index += 2
            continue
        if flag == "--add-dir":
            if index + 1 >= len(flags):
                raise RuntimeError("missing reviewer add-dir argument")
            launch_flags.extend(flags[index:index + 2])
            index += 2
            continue
        if flag == "--no-session-persistence":
            # Print-only persistence is replaced by a unique interactive session
            # ID; it is never used to resume another PR's review.
            index += 1
            continue
        if flag in {"--safe-mode", "--strict-mcp-config", "--disable-slash-commands"}:
            launch_flags.append(flag)
            index += 1
            continue
        raise RuntimeError("unsupported interactive reviewer flag: " + flag)
    if schema is None:
        raise RuntimeError("interactive reviewer requires the watcher JSON schema")
    return launcher + " " + shlex.join(launch_flags), schema


def scoped_reviewer(wrapper, slot, unit, properties):
    """Run the visible shell in an owned, uniquely named user scope.

    The resource wrapper retains affinity and rlimits for cgroup v1 hosts.
    Redirections remain in the shell, never in systemd-run's option parser.
    """
    if not re.fullmatch(r"pr-review-r[0-9]{2}-[0-9]+-[a-f0-9]{8}\.scope", unit):
        raise RuntimeError("reviewer scope name is not owned by this helper")
    command = ["/usr/bin/systemd-run", "--user", "--scope", "--unit=" + unit[:-6], "--collect"]
    for key, value in sorted(properties.items()):
        if key not in {"CPUQuota", "CPUWeight", "MemoryHigh", "MemoryMax", "TasksMax"}:
            raise RuntimeError("unsupported reviewer scope property")
        if key == "CPUQuota":
            if not isinstance(value, str) or not re.fullmatch(r"[0-9]+%", value) or int(value[:-1]) <= 0:
                raise RuntimeError("CPUQuota must be a positive percentage")
        elif not isinstance(value, int) or isinstance(value, bool) or value <= 0:
            raise RuntimeError("reviewer scope properties must be positive integers")
        command.extend(["-p", key + "=" + str(value)])
    return [*command, *shlex.split(wrapper), "zsh", "-lic"]


def reviewer_command(wrapper, slot, unit, properties):
    parts = shlex.split(wrapper)
    if "--slot" in parts:
        parts = [str(slot) if argument == "--slot" else argument for argument in parts]
    elif parts:
        parts.append(str(slot))
    if parts:
        parts.append("--")
    return scoped_reviewer(shlex.join(parts), slot, unit, properties)


def scope_command(action, unit, timeout=20):
    if not unit:
        return
    subprocess.run(["/usr/bin/systemctl", "--user", action, unit],
                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                   stderr=subprocess.DEVNULL, timeout=timeout, check=False)


def cleanup_scope():
    # --collect reaps a normally completed scope. stop is used only to abort
    # a live scope owned by this helper on error or termination.
    scope_command("stop", _cleanup_scope, timeout=10)


def cleanup_tab():
    global _cleanup_tab
    tab = _cleanup_tab
    _cleanup_tab = None
    if tab:
        try:
            command("tab", "close", tab, timeout=10)
        except (RuntimeError, subprocess.TimeoutExpired):
            pass


def terminate_signal(signum, frame):
    global _terminated
    _terminated = True
    cleanup_tab()
    cleanup_scope()
    raise SystemExit(128 + signum)


def scope_state(unit):
    result = subprocess.run(
        ["/usr/bin/systemctl", "--user", "show", unit, "--property=LoadState,ActiveState"],
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, timeout=10, check=False)
    values = dict(line.split("=", 1) for line in result.stdout.splitlines() if "=" in line)
    return values.get("LoadState"), values.get("ActiveState")


def wait_scope_start(unit, deadline):
    """Scope registration races pane.run; absence is not yet a process exit."""
    while time.monotonic() < deadline:
        load, active = scope_state(unit)
        if active == "active":
            return True
        time.sleep(.2)
    raise TimeoutError("interactive reviewer scope did not start")


def scope_running(unit):
    load, active = scope_state(unit)
    return load != "not-found" and active not in ("inactive", "failed")


def wait_agent(pane, unit, timeout=180):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if not scope_running(unit):
            screen = terminal_text(pane, "visible", 45)
            raise RuntimeError("interactive reviewer exited before becoming ready: "
                               + json.dumps(screen, ensure_ascii=False)[-1200:])
        agents = command("agent", "list", timeout=20).get("agents", [])
        agent = next((item for item in agents if item.get("pane_id") == pane), None)
        if agent is not None:
            if agent.get("agent") != "claude":
                raise RuntimeError("unexpected reviewer executable")
            screen = terminal_text(pane, "visible", 45)
            if "Yes, I trust this folder" in screen and "Quick safety check" in screen:
                command("pane", "send-keys", pane, "down", "enter", timeout=20)
                time.sleep(2)
                continue
            if agent.get("agent_status") == "blocked":
                screen = terminal_text(pane, "visible", 45)
                warning = _config.get("startup_warning", "")
                acceptance = _config.get("startup_acceptance", "")
                if not warning or not acceptance or warning not in screen or acceptance not in screen:
                    raise RuntimeError("reviewer requested an unexpected interaction")
                command("pane", "send-keys", pane, "right", "enter", timeout=20)
                time.sleep(2)
                continue
            if agent.get("agent_status") in ("idle", "working"):
                return
            if agent.get("agent_status") == "done":
                time.sleep(1)
                continue
            raise RuntimeError("reviewer startup state is not usable")
        time.sleep(1)
    raise RuntimeError("reviewer did not become ready")


def forwarded_flags(arguments, forbidden):
    output = []
    options_with_values = {"--output-format", "--json-schema", "--tools", "--setting-sources", "--permission-mode"}
    index = 0
    while index < len(arguments):
        flag = arguments[index]
        if flag in forbidden:
            raise RuntimeError("reviewer cannot override " + flag)
        if flag in options_with_values:
            if index + 1 >= len(arguments):
                raise RuntimeError("missing reviewer option argument")
            output.extend(arguments[index:index + 2])
            index += 2
            continue
        if flag == "--add-dir":
            if index + 1 >= len(arguments):
                raise RuntimeError("missing reviewer add-dir argument")
            output.extend(arguments[index:index + 2])
            index += 2
            continue
        if flag not in output:
            output.append(flag)
        index += 1
    return output


def interactive_prompt(prompt, schema, marker):
    return (prompt + "\n\nComplete this review now. Your final response must contain no prose, "
            "markdown, tool calls or further turns. Return exactly one JSON object matching this "
            "schema, with a unique transport marker on the line before and after it:\n"
            + marker + "\n<JSON object matching the schema below>\n" + marker
            + "\nSchema:\n" + schema)


def terminal_text(pane, source="recent-unwrapped", lines=None):
    result = subprocess.run(
        [_config["herdr_command"], "--session", _config["session"], "pane", "read", pane,
         "--source", source, "--lines", str(lines or int(_config.get("result_lines", 20000))),
         "--format", "text"],
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, timeout=60, check=False)
    if result.returncode:
        raise RuntimeError("cannot read interactive reviewer terminal: " + result.stderr[-500:])
    return result.stdout


def wait_result(pane, unit, marker, timeout):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        screen = terminal_text(pane)
        if screen.rsplit("●", 1)[-1].count(marker) >= 2:
            agents = command("agent", "list", timeout=20).get("agents", [])
            agent = next((item for item in agents if item.get("pane_id") == pane), None)
            if agent and agent.get("agent_status") in ("idle", "done"):
                return screen
            if agent and agent.get("agent_status") == "blocked":
                raise RuntimeError("interactive reviewer requested an unexpected interaction")
        if not scope_running(unit):
            raise RuntimeError("interactive reviewer exited before returning its result")
        time.sleep(1)
    raise TimeoutError("interactive reviewer did not return a complete marked result")


def terminal_result(screen, marker, schema):
    """Accept only a marked assistant response, not the prompt or a diff's JSON."""
    if marker not in screen:
        raise RuntimeError("interactive reviewer returned no marked result")
    tail = screen.rsplit("●", 1)[-1]
    matches = list(re.finditer(re.escape(marker), tail))
    if len(matches) != 2:
        raise RuntimeError("interactive reviewer result markers are not unique: "
                           + screen[-1500:])
    text = tail[matches[0].end():matches[1].start()]
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    text = "\n".join(lines)
    try:
        value = json.loads(text)
    except json.JSONDecodeError as error:
        raise RuntimeError("interactive reviewer returned invalid marked JSON") from error
    try:
        import jsonschema
        jsonschema.validate(value, json.loads(schema))
    except ImportError as error:
        raise RuntimeError("interactive reviewer requires the jsonschema validator") from error
    except (ValueError, jsonschema.ValidationError) as error:
        raise RuntimeError("interactive reviewer result does not match the watcher schema") from error
    return value


def run(slot, flags):
    global _cleanup_tab, _cleanup_scope
    root = Path(_config["runtime_directory"]).expanduser()
    lease_path = root / ("slot-%02d.json" % slot)
    if lease_path.exists():
        try:
            old = json.loads(lease_path.read_text(encoding="utf-8"))
            pid = old.get("pid")
            if isinstance(pid, int) and pid > 0:
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    tab = old.get("tab")
                    if isinstance(tab, str) and tab.startswith("w") and ":t" in tab:
                        command("tab", "close", tab, timeout=20)
        except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired):
            pass
    name = "pr-r%02d-%d" % (slot, os.getpid())
    unit = "pr-review-r%02d-%d-%s.scope" % (slot, os.getpid(), secrets.token_hex(4))
    marker = "PR_REVIEW_RESULT_" + secrets.token_hex(12)
    session_id = str(uuid.uuid4())
    launch, schema = interactive_command(_config["reviewer_launcher"], flags)
    launch += " --session-id " + session_id
    prompt = interactive_prompt(sys.stdin.read(), schema, marker)
    with tempfile.TemporaryDirectory(prefix="slot-%02d-" % slot, dir=root):
        with lease_path.open("w", encoding="utf-8") as lease:
            json.dump({"pid": os.getpid(), "slot": slot}, lease)
            lease.flush()
            os.fsync(lease.fileno())
            try:
                created = command("tab", "create", "--workspace", _config.get("workspace", "w1"),
                                  "--cwd", os.getcwd(), "--label", name, "--no-focus", timeout=30)
                tab = created["tab"]["tab_id"]
                pane = created["root_pane"]["pane_id"]
                _cleanup_tab = tab
                lease.seek(0)
                lease.truncate()
                json.dump({"pid": os.getpid(), "slot": slot, "tab": tab, "pane": pane,
                           "scope": unit}, lease)
                lease.flush()
                os.fsync(lease.fileno())
                bounded = reviewer_command(_config.get("process_wrapper", ""), slot, unit,
                                           _config.get("scope_properties", {}))
                bounded.append(launch)
                _cleanup_scope = unit
                command("pane", "run", pane, " ".join(shlex.quote(arg) for arg in bounded), timeout=30)
                if not wait_scope_start(unit, time.monotonic() + 180):
                    screen = terminal_text(pane, "visible", 45)
                    raise RuntimeError("interactive reviewer exited before becoming ready: "
                                       + json.dumps(screen, ensure_ascii=False)[-1200:])
                wait_agent(pane, unit)
                command("agent", "prompt", pane, prompt, "--wait", "--until", "working",
                        "--until", "blocked",
                        "--timeout", str(int(_config.get("lifetime_seconds", 7200)) * 1000),
                        timeout=int(_config.get("prompt_timeout_seconds", 120)))
                screen = wait_result(pane, unit, marker, int(_config.get("lifetime_seconds", 7200)))
                value = terminal_result(screen, marker, schema)
                result = {"is_error": False, "subtype": "success", "result": "",
                          "structured_output": value, "permission_denials": []}
                sys.stdout.write(json.dumps(result, separators=(",", ":")))
            finally:
                cleanup_tab()
                cleanup_scope()


def main():
    global _config
    signal.signal(signal.SIGTERM, terminate_signal)
    signal.signal(signal.SIGINT, terminate_signal)
    atexit.register(cleanup_tab)
    if len(sys.argv) < 4 or sys.argv[2] != "--":
        raise SystemExit("usage: herdr_review.py CONFIG -- [CLI_ARG ...]")
    _config = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
    for key in ("herdr_command", "session", "runtime_directory", "reviewer_launcher"):
        if not isinstance(_config.get(key), str) or not _config[key]:
            raise RuntimeError("missing reviewer configuration: " + key)
    root = Path(_config["runtime_directory"]).expanduser()
    if not root.is_absolute():
        raise RuntimeError("runtime_directory must be absolute")
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    flags = forwarded_flags(sys.argv[3:], set(_config.get("forbidden_flags", ["--model"])))
    required_flags = {
        "-p": None, "--output-format": "json", "--tools": "Read",
        "--permission-mode": "plan", "--json-schema": None,
    }
    for flag, value in required_flags.items():
        if flag not in flags:
            raise RuntimeError("reviewer requires " + flag)
        if value is not None:
            index = flags.index(flag)
            if index + 1 >= len(flags) or flags[index + 1] != value:
                raise RuntimeError("reviewer requires %s %s" % (flag, value))
    slots = int(_config.get("slots", 1))
    if not 1 <= slots <= 64:
        raise RuntimeError("reviewer slots must be between 1 and 64")
    locks = []
    try:
        for slot in range(slots):
            lock = (root / ("slot-%02d.lock" % slot)).open("a+")
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                lock.close()
                continue
            locks.append(lock)
            return run(slot, flags)
        raise RuntimeError("all %d bounded reviewer slots are occupied" % slots)
    finally:
        cleanup_tab()
        cleanup_scope()
        for lock in locks:
            fcntl.flock(lock, fcntl.LOCK_UN)
            lock.close()


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)

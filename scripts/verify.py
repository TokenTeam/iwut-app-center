#!/usr/bin/env python3
"""Local verification tiers. Standard library only; no repository mutations."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[1]
TIERS = ("quick", "backend", "cross-service")


def utc_now():
    return datetime.now(timezone.utc).isoformat()


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args], timeout=30)


def fingerprint(root):
    """Identify checked-out content, including nonignored untracked files.

    Store hashes, not diffs or file contents. Submodules are fingerprinted as
    separate sources. Ignored artifacts are intentionally outside this identity.
    """
    root = Path(root).resolve()
    head = git(root, "rev-parse", "HEAD").decode().strip()
    status = git(root, "status", "--porcelain=v1", "-z")
    digest = hashlib.sha256()
    digest.update(head.encode() + b"\0" + status)
    files = set(git(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").split(b"\0"))
    for raw in sorted(files - {b""}):
        path = root / os.fsdecode(raw)
        digest.update(raw + b"\0")
        if path.is_symlink():
            digest.update(b"link\0" + os.fsencode(os.readlink(path)))
        elif path.is_file():
            digest.update(str(path.stat().st_mode & 0o777).encode() + b"\0")
            with path.open("rb") as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    digest.update(chunk)
        else:
            digest.update(b"missing-or-submodule")
        digest.update(b"\0")
    return {"path": str(root), "head": head, "dirty": bool(status), "sha256": digest.hexdigest()}


def source_paths(root, docs, auth, tier):
    result = {"service": root, "api": root / "api", "docs": docs}
    if tier == "cross-service":
        result.update(auth=auth, auth_api=auth / "api")
    return result


def stages(root, docs, tier):
    plan = [
        ("script-tests", root, [sys.executable, "-B", "-m", "unittest", "discover", "-s", "scripts/tests"]),
    ]
    plan += [(f"syntax-{script}", root, ["bash", "-n", f"scripts/{script}.sh"])
             for script in ("integration-lib", "test-mongo-integration", "test-auth-app-integration")]
    plan += [(name, root, ["make", target]) for name, target in (
        ("format", "fmt-check"), ("build", "build"), ("unit-transport-architecture", "test"),
        ("vet", "vet"), ("wire", "wire-check"), ("proto", "api-check"))]
    plan += [
        ("docs-tests", docs, [sys.executable, "-B", "-m", "unittest", "discover", "-s", "tools/tests"]),
        ("brief-freshness", docs, [sys.executable, "-B", "tools/gen_brief.py", "--check", "--all"]),
        ("registry", docs, [sys.executable, "-B", "tools/registry.py", "--check"]),
    ]
    for label, path in (("service", root), ("api", root / "api"), ("docs", docs)):
        for cached in (False, True):
            plan.append((f"diff-{label}-{'staged' if cached else 'working'}", path,
                         ["git", "diff", "--check"] + (["--cached"] if cached else [])))
    if tier != "quick":
        plan += [("race", root, ["make", "test-race"]),
                 ("mongo-and-http-grpc-e2e-race", root, ["bash", "scripts/test-mongo-integration.sh", "-race"])]
    if tier == "cross-service":
        plan.append(("auth-app-e2e-race", root, ["bash", "scripts/test-auth-app-integration.sh"]))
    return plan


def preflight(root, docs, auth, tier):
    required = ["git", "go", "make", "gofmt", "bash", "protoc", "protoc-gen-go", "protoc-gen-go-grpc", "protoc-gen-go-http"]
    if tier != "quick":
        required += ["docker", "timeout", "python3"]
    if tier == "cross-service":
        required += ["openssl", "base64"]
    missing = [tool for tool in required if shutil.which(tool) is None]
    files = [root / "api/go.mod", docs / "tools/gen_brief.py", docs / "tools/registry.py"]
    if tier == "cross-service":
        files += [auth / "go.mod", auth / "cmd/auth-center/main.go", auth / "api/go.mod"]
    missing += [str(path) for path in files if not path.is_file()]
    if missing:
        raise RuntimeError("Missing prerequisites: " + ", ".join(missing))
    if tier != "quick":
        result = subprocess.run(["docker", "info"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)
        if result.returncode:
            raise RuntimeError("Docker daemon is unavailable")
    # No environment dump: only public tool versions are recorded.
    versions = {}
    for command in (["go", "version"], ["git", "--version"], ["protoc", "--version"],
                    ["protoc-gen-go", "--version"], ["protoc-gen-go-grpc", "--version"],
                    ["protoc-gen-go-http", "--version"]):
        versions[command[0]] = subprocess.check_output(command, stderr=subprocess.STDOUT, timeout=15).decode().strip()
    return versions


def stop_group(process):
    # Also terminate children (Docker CLI, go test and test binaries). Shell
    # cleanup gets time to remove containers before a hard timeout is enforced.
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=25)
    except subprocess.TimeoutExpired:
        pass
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def run_stage(name, cwd, command, output, timeout, environment):
    record = {"name": name, "cwd": str(cwd), "command": command, "started_at": utc_now(), "log": output.name}
    start = time.monotonic()
    process = None
    try:
        with output.open("wb") as log:
            process = subprocess.Popen(command, cwd=cwd, env=environment, stdout=log,
                                       stderr=subprocess.STDOUT, start_new_session=True)
            while True:
                remaining = timeout - (time.monotonic() - start)
                if remaining <= 0:
                    raise subprocess.TimeoutExpired(command, timeout)
                try:
                    code = process.wait(timeout=min(30, remaining))
                    break
                except subprocess.TimeoutExpired:
                    if time.monotonic() - start >= timeout:
                        raise
                    print(f"  {name}: still running ({int(time.monotonic() - start)}s)", flush=True)
        record.update(exit_code=code, status="passed" if code == 0 else "failed")
    except subprocess.TimeoutExpired:
        if process is not None:
            stop_group(process)
        record.update(exit_code=124, status="timeout")
    except (KeyboardInterrupt, InterruptedError):
        if process is not None:
            stop_group(process)
        record.update(exit_code=130, status="interrupted")
    except OSError as error:
        record.update(exit_code=127, status="failed", error=str(error))
    record["duration_seconds"] = round(time.monotonic() - start, 3)
    return record


def write_report(path, report):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n")
    temporary.replace(path)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tier", choices=TIERS, nargs="?", default="quick")
    parser.add_argument("--doctor", action="store_true", help="only check prerequisites; does not count as verification")
    parser.add_argument("--timeout", type=int, default=1200, help="seconds per gate (default 1200)")
    args = parser.parse_args(argv)
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    os.umask(0o077)
    docs = Path(os.environ.get("APP_CENTER_DOCS_DIR", ROOT / "../../docs")).resolve()
    auth = Path(os.environ.get("AUTH_CENTER_SOURCE_DIR", ROOT / "../iwut-auth-center-ddd")).resolve()
    artifacts = ROOT / ".artifacts/verification"
    artifacts.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix=datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ-"), dir=artifacts))
    report_path = directory / "report.json"
    report = {"tier": args.tier, "doctor_only": args.doctor, "started_at": utc_now(), "status": "running", "stages": [],
              "not_in_scope": (["race", "MongoDB/HTTP/gRPC E2E", "real Auth+App E2E"] if args.tier == "quick" else
                               ["real Auth+App E2E"] if args.tier == "backend" else [])}
    write_report(report_path, report)
    print(f"Verification report: {report_path}", flush=True)
    paths = source_paths(ROOT, docs, auth, args.tier)
    def interrupted(_signum, _frame):
        raise InterruptedError("verification interrupted")
    previous = signal.signal(signal.SIGTERM, interrupted)
    try:
        report["tool_versions"] = preflight(ROOT, docs, auth, args.tier)
        report["sources_start"] = {name: fingerprint(path) for name, path in paths.items()}
        if args.doctor:
            report["status"] = "doctor-passed"
        else:
            environment = os.environ.copy()
            for key in ("MONGODB_INTEGRATION_URI", "AUTH_CENTER_INTEGRATION_TARGET", "AUTH_CENTER_INTEGRATION_DATABASE"):
                environment.pop(key, None)
            environment["AUTH_CENTER_SOURCE_DIR"] = str(auth)
            plan = stages(ROOT, docs, args.tier)
            report["planned_stages"] = [name for name, _, _ in plan]
            for number, (name, cwd, command) in enumerate(plan, 1):
                print(f"[{number}/{len(plan)}] {name}", flush=True)
                environment["INTEGRATION_LOG_DIR"] = str(directory / f"{number:02d}-{name}-diagnostics")
                result = run_stage(name, cwd, command, directory / f"{number:02d}-{name}.log", args.timeout, environment)
                report["stages"].append(result)
                write_report(report_path, report)
                if result["exit_code"] != 0:
                    report["status"] = result["status"]
                    break
            else:
                report["status"] = "passed"
    except (KeyboardInterrupt, InterruptedError):
        report["status"] = "interrupted"
    except (OSError, RuntimeError, subprocess.SubprocessError) as error:
        report.update(status="failed", error=str(error))
    finally:
        try:
            if "sources_start" in report:
                report["sources_end"] = {name: fingerprint(path) for name, path in paths.items()}
                changed = [name for name in paths if report["sources_start"][name] != report["sources_end"][name]]
                report["changed_sources"] = changed
                if changed and report["status"] in ("passed", "doctor-passed"):
                    report["status"] = "source-changed"
        except (OSError, subprocess.SubprocessError) as error:
            report.update(status="failed", error=f"Cannot verify final source identity: {error}")
        report["finished_at"] = utc_now()
        write_report(report_path, report)
        signal.signal(signal.SIGTERM, previous)
    print(f"Result: {report['status']}; report: {report_path}", flush=True)
    if report.get("error"):
        print(report["error"], file=sys.stderr)
    return 0 if report["status"] in ("passed", "doctor-passed") else 1


if __name__ == "__main__":
    sys.exit(main())

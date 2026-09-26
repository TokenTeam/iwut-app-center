import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location("app_verify", Path(__file__).resolve().parents[1] / "verify.py")
verify = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verify)


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        self.call_git("config", "user.email", "test@example.invalid")
        self.call_git("config", "user.name", "Verification Test")
        (self.root / ".gitignore").write_text(".artifacts/\n")
        (self.root / "source.txt").write_text("original\n")
        self.call_git("add", ".")
        self.call_git("commit", "-qm", "fixture")

    def call_git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.root), *args])

    def test_identity_tracks_content_untracked_staging_and_ignores_reports(self):
        original = verify.fingerprint(self.root)
        (self.root / ".artifacts").mkdir()
        (self.root / ".artifacts/report.json").write_text("ignored")
        self.assertEqual(original, verify.fingerprint(self.root))
        (self.root / "source.txt").write_text("private-data-that-must-not-enter-the-report")
        changed = verify.fingerprint(self.root)
        self.assertNotEqual(original["sha256"], changed["sha256"])
        self.assertNotIn("private-data", json.dumps(changed))
        self.call_git("add", "source.txt")
        self.assertNotEqual(changed, verify.fingerprint(self.root))
        staged = verify.fingerprint(self.root)
        (self.root / "new.txt").write_text("new input")
        self.assertNotEqual(staged, verify.fingerprint(self.root))

    def test_tiers_include_only_their_promised_gates(self):
        plans = {tier: verify.stages(self.root, self.root / "docs", tier) for tier in verify.TIERS}
        quick, backend, cross = (plans[t] for t in verify.TIERS)
        self.assertEqual(backend[:len(quick)], quick)
        self.assertEqual(cross[:len(backend)], backend)
        self.assertNotIn("race", [name for name, _, _ in quick])
        mongo = next(command for name, _, command in backend if name == "mongo-and-http-grpc-e2e-race")
        self.assertIn("-race", mongo)
        self.assertNotIn("-run", mongo)
        self.assertEqual(cross[-1][0], "auth-app-e2e-race")
        for name, cwd, _ in quick:
            if name in ("docs-tests", "brief-freshness", "registry"):
                self.assertEqual(cwd, self.root / "docs")

    def test_stage_failure_and_timeout_are_not_passes(self):
        failure = verify.run_stage("failure", self.root, [sys.executable, "-c", "print('diagnostic'); raise SystemExit(7)"],
                                   self.root / "failed.log", 5, os.environ.copy())
        self.assertEqual(failure["exit_code"], 7)
        self.assertEqual(failure["status"], "failed")
        self.assertIn("diagnostic", (self.root / "failed.log").read_text())
        expired = verify.run_stage("timeout", self.root, [sys.executable, "-c", "import time; time.sleep(60)"],
                                   self.root / "timeout.log", 0.1, os.environ.copy())
        self.assertEqual(expired["exit_code"], 124)
        self.assertEqual(expired["status"], "timeout")

    def run_main(self, plan, *args, doctor_error=None):
        with mock.patch.object(verify, "ROOT", self.root), \
             mock.patch.object(verify, "preflight", side_effect=doctor_error, return_value={}), \
             mock.patch.object(verify, "source_paths", return_value={"service": self.root}), \
             mock.patch.object(verify, "stages", return_value=plan):
            code = verify.main(["quick", *args])
        paths = sorted((self.root / ".artifacts/verification").glob("*/report.json"))
        self.assertEqual(len(paths), 1)
        return code, json.loads(paths[0].read_text())

    def test_failure_stops_pipeline_and_records_unrun_stages(self):
        plan = [("fail", self.root, [sys.executable, "-c", "raise SystemExit(9)"]),
                ("must-not-run", self.root, [sys.executable, "-c", "raise Exception('wrong')"])]
        code, report = self.run_main(plan)
        self.assertNotEqual(code, 0)
        self.assertEqual(report["status"], "failed")
        self.assertEqual(len(report["stages"]), 1)
        self.assertEqual(len(report["planned_stages"]), 2)
        self.assertEqual(report["stages"][0]["exit_code"], 9)

    def test_source_change_invalidates_an_otherwise_successful_run(self):
        plan = [("mutating-child", self.root, [sys.executable, "-c", "from pathlib import Path; Path('source.txt').write_text('changed')"])]
        code, report = self.run_main(plan)
        self.assertNotEqual(code, 0)
        self.assertEqual(report["stages"][0]["status"], "passed")
        self.assertEqual(report["status"], "source-changed")
        self.assertEqual(report["changed_sources"], ["service"])

    def test_doctor_is_not_a_delivery_pass(self):
        code, report = self.run_main([], "--doctor")
        self.assertEqual(code, 0)
        self.assertEqual(report["status"], "doctor-passed")
        self.assertEqual(report["stages"], [])

    def test_missing_prerequisites_produce_failure_report(self):
        code, report = self.run_main([], doctor_error=RuntimeError("missing tool"))
        self.assertNotEqual(code, 0)
        self.assertEqual(report["status"], "failed")
        self.assertIn("missing tool", report["error"])

    def test_mongo_readiness_timeout_is_explicit_failure(self):
        library = Path(__file__).resolve().parents[1] / "integration-lib.sh"
        result = subprocess.run(["bash", "-c", 'source "$1"; startup_timeout=1; container_name=unused; timeout() { return 1; }; sleep() { :; }; wait_for_mongo primary test', "test", str(library)],
                                capture_output=True, timeout=5)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"primary timed out", result.stderr)


if __name__ == "__main__":
    unittest.main()

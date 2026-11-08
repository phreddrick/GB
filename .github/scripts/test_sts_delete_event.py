#!/usr/bin/env python3
"""Regression tests for sts-controller-delete-event.jq, the program with which
kind-e2e.sh proves that the StatefulSet controller itself deleted the quiesced
Pod during the CR-43 rolling update (ADR-0010). Synthetic events only, in the
shapes a core/v1 EventList takes on the wire:

* Kubernetes 1.36 (the pinned kind node and the development cluster): the
  legacy recorder, ``source.component`` and ``reportingComponent`` both set,
  ``lastTimestamp`` at second precision, ``eventTime`` null, and the verb of
  the message title-cased ("Delete Pod ... successful"). The first hosted runs
  of the check (42527820295, 42527827925) failed because the previous inline
  filter required the lower-case "delete Pod ..." of older releases while
  every other predicate held.
* the older lower-case message;
* an event written through events.k8s.io/v1 and read as core/v1 (``source``
  empty, ``reportingComponent`` set, ``eventTime`` with microseconds).

Every predicate is also shown to reject on its own (other StatefulSet UID,
other component, other Pod or verb, event before the rollout, without a
time or with an unparseable one), the report must carry expected and
observed values for each one, a `since` that is not a parseable
second-precision UTC instant or an input that is not an EventList must fail
with no report at all, and the shell block of kind-e2e.sh (extracted from the
script and run under set -euo pipefail with `k` stubbed) must pass on the
controller's event, name the mismatched predicate otherwise, and fail closed
when the program prints nothing, several values or a non-report. Requires jq
(the runner image and igor have it; a missing jq is a failure, not a skip).
"""
from __future__ import annotations

import copy
import json
import os
import re
import shutil
import subprocess
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
PROGRAM = os.path.join(HERE, "sts-controller-delete-event.jq")
KIND_E2E = os.path.join(HERE, "kind-e2e.sh")

UID = "11c05cc7-3724-410b-9a2d-5a43d23e6f34"
SINCE = "2026-11-08T22:05:13Z"

# Shape observed on a v1.36.4 API server (Stage 3 evidence, SuccessfulCreate
# of the same controller), with the verb and reason of the deletion.
EVENT_1_36 = {
    "apiVersion": "v1",
    "kind": "Event",
    "metadata": {"name": "gb-engine.18a4f0c2d3e5b7a9", "namespace": "gb-dev"},
    "involvedObject": {"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "gb-engine", "namespace": "gb-dev", "uid": UID},
    "reason": "SuccessfulDelete",
    "message": "Delete Pod gb-engine-0 in StatefulSet gb-engine successful",
    "source": {"component": "statefulset-controller"},
    "reportingComponent": "statefulset-controller",
    "reportingInstance": "",
    "firstTimestamp": "2026-11-08T22:05:40Z",
    "lastTimestamp": "2026-11-08T22:05:40Z",
    "eventTime": None,
    "count": 1,
    "type": "Normal",
}


def run(items, uid=UID, since=SINCE, set_name="gb-engine", pod="gb-engine-0", raw=None):
    """Run the program; return (returncode, parsed report or None, stderr, raw stdout)."""
    data = json.dumps({"apiVersion": "v1", "kind": "List", "items": items}) if raw is None else raw
    proc = subprocess.run(
        ["jq", "-f", PROGRAM, "--arg", "uid", uid, "--arg", "set", set_name, "--arg", "pod", pod, "--arg", "since", since],
        input=data, capture_output=True, text=True, check=False,
    )
    report = json.loads(proc.stdout) if proc.returncode == 0 and proc.stdout.strip() else None
    return proc.returncode, report, proc.stderr, proc.stdout


def variant(**changes):
    ev = copy.deepcopy(EVENT_1_36)
    for key, value in changes.items():
        if value is _ABSENT:
            ev.pop(key, None)
        else:
            ev[key] = value
    return ev


_ABSENT = object()


class StsControllerDeleteEventTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not shutil.which("jq"):
            raise RuntimeError("jq is required: the runner image and the development host provide it; a missing jq is a failure, not a skip")

    def assertOnlyFails(self, report, *failed):
        """Exactly one candidate, not matched, failing exactly the named predicates, each with values."""
        self.assertEqual(report["matched"], [])
        self.assertEqual(len(report["candidates"]), 1)
        checks = report["candidates"][0]["checks"]
        self.assertEqual(sorted(k for k, v in checks.items() if not v["ok"]), sorted(failed))
        for name, check in checks.items():
            self.assertIn("expected", check, name)
            self.assertIn("observed", check, name)

    def test_program_compiles_against_an_empty_list(self):
        rc, report, err, _ = run([])
        self.assertEqual(rc, 0, err)
        self.assertEqual(report["matched"], [])
        self.assertEqual(report["candidates"], [])
        self.assertEqual(report["expected"]["involvedObject_uid"], UID)
        self.assertEqual(report["expected"]["component"], "statefulset-controller")
        self.assertEqual(report["expected"]["not_before"], SINCE)

    def test_kubernetes_1_36_title_cased_message_matches(self):
        rc, report, err, _ = run([EVENT_1_36])
        self.assertEqual(rc, 0, err)
        self.assertEqual(report["matched"], [EVENT_1_36["metadata"]["name"]])
        checks = report["candidates"][0]["checks"]
        self.assertTrue(all(c["ok"] for c in checks.values()), checks)
        self.assertEqual(checks["message"]["observed"], "Delete Pod gb-engine-0 in StatefulSet gb-engine successful")
        self.assertEqual(checks["time"]["observed"], "2026-11-08T22:05:40Z")

    def test_older_lower_case_message_matches(self):
        rc, report, err, _ = run([variant(message="delete Pod gb-engine-0 in StatefulSet gb-engine successful")])
        self.assertEqual(rc, 0, err)
        self.assertEqual(len(report["matched"]), 1)

    def test_events_v1_shape_matches_and_same_second_microseconds_count(self):
        ev = variant(source={}, firstTimestamp=None, lastTimestamp=None, eventTime="2026-11-08T22:05:13.482913Z")
        rc, report, err, _ = run([ev])
        self.assertEqual(rc, 0, err)
        self.assertEqual(len(report["matched"]), 1)
        checks = report["candidates"][0]["checks"]
        self.assertEqual(checks["component"]["observed"], "statefulset-controller")
        self.assertEqual(checks["time"]["observed"], "2026-11-08T22:05:13.482913Z")

    def test_other_statefulset_uid_is_rejected_with_both_values(self):
        rc, report, err, _ = run([variant(involvedObject={**EVENT_1_36["involvedObject"], "uid": "00000000-0000-4000-8000-000000000000"})])
        self.assertEqual(rc, 0, err)
        self.assertOnlyFails(report, "uid")
        check = report["candidates"][0]["checks"]["uid"]
        self.assertEqual(check["expected"], UID)
        self.assertEqual(check["observed"], "00000000-0000-4000-8000-000000000000")

    def test_other_component_is_rejected(self):
        for source, reporting in ((
            {"component": "kubelet"}, "kubelet"),
            ({"component": "statefulset-controller-impostor"}, ""),
            ({}, ""),
        ):
            with self.subTest(source=source, reporting=reporting):
                rc, report, err, _ = run([variant(source=source, reportingComponent=reporting)])
                self.assertEqual(rc, 0, err)
                self.assertOnlyFails(report, "component")
                self.assertEqual(report["candidates"][0]["checks"]["component"]["expected"], "statefulset-controller")

    def test_other_pod_or_verb_is_rejected(self):
        for message in (
            "Delete Pod gb-engine-1 in StatefulSet gb-engine successful",
            "Create Pod gb-engine-0 in StatefulSet gb-engine successful",
            "Delete Pod gb-engine-0 in StatefulSet gb-engine failed error: boom",
            "Delete Pod gb-engine-0 in StatefulSet gb-engine-2 successful",
            "",
        ):
            with self.subTest(message=message):
                rc, report, err, _ = run([variant(message=message)])
                self.assertEqual(rc, 0, err)
                self.assertOnlyFails(report, "message")
                self.assertEqual(report["candidates"][0]["checks"]["message"]["expected"],
                                 "Delete Pod gb-engine-0 in StatefulSet gb-engine successful")
        rc, report, err, _ = run([variant(message=_ABSENT)])
        self.assertEqual(rc, 0, err)
        self.assertOnlyFails(report, "message")

    def test_event_before_the_rollout_or_without_a_time_is_rejected(self):
        for changes in (
            {"lastTimestamp": "2026-11-08T22:05:12Z"},
            {"lastTimestamp": None, "eventTime": "2026-11-08T22:05:12.999999Z"},
            {"lastTimestamp": None, "eventTime": None},
            {"lastTimestamp": _ABSENT, "eventTime": _ABSENT},
            {"lastTimestamp": "2026-11-08"},
        ):
            with self.subTest(changes=changes):
                rc, report, err, _ = run([variant(**changes)])
                self.assertEqual(rc, 0, err)
                self.assertOnlyFails(report, "time")
                self.assertEqual(report["candidates"][0]["checks"]["time"]["expected"], "not before " + SINCE)

    def test_same_second_as_the_rollout_start_is_accepted(self):
        rc, report, err, _ = run([variant(lastTimestamp=SINCE)])
        self.assertEqual(rc, 0, err)
        self.assertEqual(len(report["matched"]), 1)

    def test_multiple_predicates_are_reported_together(self):
        ev = variant(message="delete Pod gb-engine-1 in StatefulSet gb-engine successful", source={"component": "kubelet"}, reportingComponent="kubelet")
        rc, report, err, _ = run([ev])
        self.assertEqual(rc, 0, err)
        self.assertOnlyFails(report, "component", "message")

    def test_only_the_conforming_event_is_matched(self):
        bad = variant(involvedObject={**EVENT_1_36["involvedObject"], "uid": "other"}, metadata={"name": "gb-engine.bad"})
        good = variant(metadata={"name": "gb-engine.good"})
        rc, report, err, _ = run([bad, good])
        self.assertEqual(rc, 0, err)
        self.assertEqual(report["matched"], ["gb-engine.good"])
        self.assertEqual([c["name"] for c in report["candidates"]], ["gb-engine.bad", "gb-engine.good"])

    def test_malformed_since_or_input_fails_closed_with_no_output(self):
        for since in ("garbage", "2026-11-08T22:05:13", "2026-11-08T22:05:13.000Z", "2026-11-08 22:05:13Z", "",
                      "2026-00-00T00:00:00Z", "2026-11-08T24:00:00Z", "zzzz-zz-zzTzz:zz:zzZ"):
            with self.subTest(since=since):
                rc, _, err, out = run([EVENT_1_36], since=since)
                self.assertNotEqual(rc, 0)
                self.assertEqual(out, "", "a rejected `since` must produce no report at all")
                self.assertIn("since must be", err)
        for raw in ("[]", "{}", '{"items": {}}', "null", '"text"'):
            with self.subTest(raw=raw):
                rc, _, err, out = run([], raw=raw)
                self.assertNotEqual(rc, 0)
                self.assertEqual(out, "", "a rejected input must produce no report at all")
                self.assertIn("EventList", err)

    def test_unparseable_event_instant_fails_the_time_predicate(self):
        for stamp in ("zzzzzzzzzzzzzzzzzzz", "2026-00-00T00:00:00Z", "2026-11-08T25:05:40Z", "2026-11-08T22:05:40+01:00"):
            with self.subTest(stamp=stamp):
                rc, report, err, _ = run([variant(lastTimestamp=stamp)])
                self.assertEqual(rc, 0, err)
                self.assertOnlyFails(report, "time")
                self.assertEqual(report["candidates"][0]["checks"]["time"]["observed"], stamp)

    def test_hosted_run_42527827925_event_is_matched(self):
        """The event the failing step printed (owner-supplied log excerpt): the old filter rejected it on the message alone."""
        ev = variant(involvedObject={**EVENT_1_36["involvedObject"], "uid": "d8899d68-b1e5-446d-9906-d034120ef272"},
                     lastTimestamp="2026-11-08T22:01:50Z", firstTimestamp="2026-11-08T22:01:50Z")
        rc, report, err, _ = run([ev], uid="d8899d68-b1e5-446d-9906-d034120ef272", since="2026-11-08T22:01:48Z")
        self.assertEqual(rc, 0, err)
        self.assertEqual(len(report["matched"]), 1)
        self.assertTrue(report["candidates"][0]["checks"]["message"]["ok"])

    def test_kind_e2e_compiles_and_calls_the_program_with_every_argument(self):
        with open(KIND_E2E, encoding="utf-8") as f:
            script = f.read()
        self.assertIn('EVENT_PROGRAM="$HERE/sts-controller-delete-event.jq"', script)
        # Compiled before the cluster exists: the compile check precedes `kind create cluster`.
        compile_at = script.index('jq -e -f "$EVENT_PROGRAM"')
        self.assertLess(compile_at, script.index("kind create cluster"))
        call = re.search(r'^jq -f "\$EVENT_PROGRAM" (?P<args>.*) "\$WORK/events-rollout\.json"', script, re.M)
        self.assertIsNotNone(call, "kind-e2e.sh must evaluate the rollout events with the program")
        for arg in ('--arg uid "$sts_uid"', "--arg set gb-engine", "--arg pod gb-engine-0", '--arg since "$rollout_started"'):
            self.assertIn(arg, call.group("args"))
        # The decision is the program's `matched` list from exactly one report object, and a failure prints every predicate.
        self.assertIn("jq -e -r -s 'if length == 1 and (.[0] | type) == \"object\" and (.[0].matched | type) == \"array\" then (.[0].matched | length) else error(", script)
        self.assertIn('[[ "$matched" =~ ^[0-9]+$ ]] || fail', script)
        self.assertIn("MISMATCH", script)
        self.assertNotIn('test("delete Pod', script)

    # ---- the shell wiring itself, under set -euo pipefail ----------------
    def shell_block(self):
        """The rollout-event block of kind-e2e.sh, from the `k get events` line to its closing `fi`."""
        with open(KIND_E2E, encoding="utf-8") as f:
            lines = f.read().splitlines()
        start = next(i for i, l in enumerate(lines) if l.startswith("k get events --field-selector reason=SuccessfulDelete,involvedObject.name=gb-engine"))
        end = next(i for i in range(start, len(lines)) if lines[i] == "fi")
        return "\n".join(lines[start:end + 1]) + "\n"

    def run_block(self, events, program=PROGRAM, uid=UID, since=SINCE, k_fails=False):
        """Run the block with `k` stubbed to print the events file; return (rc, stderr)."""
        with tempfile.TemporaryDirectory() as work:
            events_path = os.path.join(work, "events.json")
            with open(events_path, "w", encoding="utf-8") as f:
                json.dump({"apiVersion": "v1", "kind": "List", "items": events}, f)
            script = textwrap.dedent(f"""\
                set -euo pipefail
                fail() {{ echo "FAIL $*" >&2; exit 1; }}
                k() {{ {"return 1" if k_fails else f'cat "{events_path}"'}; }}
                EVENT_PROGRAM={program!r}
                WORK={work!r}
                sts_uid={uid!r}
                rollout_started={since!r}
                """) + self.shell_block() + 'echo BLOCK-PASSED\n'
            proc = subprocess.run(["bash", "-c", script], capture_output=True, text=True, check=False)
            return proc.returncode, proc.stderr, proc.stdout

    def test_shell_block_passes_on_the_controller_event(self):
        rc, err, out = self.run_block([EVENT_1_36])
        self.assertEqual(rc, 0, err)
        self.assertIn("BLOCK-PASSED", out)

    def test_shell_block_names_the_mismatched_predicate(self):
        rc, err, out = self.run_block([EVENT_1_36], uid="00000000-0000-4000-8000-000000000000")
        self.assertEqual(rc, 1)
        self.assertNotIn("BLOCK-PASSED", out)
        self.assertIn('uid: MISMATCH  expected "00000000-0000-4000-8000-000000000000"  observed "' + UID + '"', err)
        self.assertIn("component: ok", err)
        self.assertIn("message: ok", err)
        self.assertIn("time: ok", err)
        self.assertIn("each of the 1 candidate event(s) listed above fails at least one predicate", err)
        self.assertIn("FAIL no SuccessfulDelete event by the statefulset-controller for StatefulSet uid 00000000-0000-4000-8000-000000000000", err)

    def test_shell_block_fails_when_no_event_exists(self):
        rc, err, out = self.run_block([])
        self.assertEqual(rc, 1)
        self.assertNotIn("BLOCK-PASSED", out)
        self.assertIn("0 candidate(s)", err)
        self.assertIn("no SuccessfulDelete event exists on gb-engine at all", err)

    def test_shell_block_fails_closed_on_an_absent_or_malformed_report(self):
        """A program that prints nothing, several values or a non-report must fail the step, not slip past the count."""
        with tempfile.TemporaryDirectory() as d:
            for name, body in (("empty.jq", "empty"),
                               ("two.jq", '{matched: ["a"]}, {matched: ["b"]}'),
                               ("text.jq", '"not a report"'),
                               ("nomatched.jq", '{expected: {}, candidates: []}'),
                               ("badmatched.jq", '{matched: "yes"}')):
                with self.subTest(program=name):
                    path = os.path.join(d, name)
                    with open(path, "w", encoding="utf-8") as f:
                        f.write(body + "\n")
                    rc, err, out = self.run_block([EVENT_1_36], program=path)
                    self.assertEqual(rc, 1, err)
                    self.assertNotIn("BLOCK-PASSED", out)
                    self.assertIn("FAIL the event program did not produce a report", err)
            path = os.path.join(d, "syntax.jq")
            with open(path, "w", encoding="utf-8") as f:
                f.write("{ this is not jq\n")
            rc, err, out = self.run_block([EVENT_1_36], program=path)
            self.assertEqual(rc, 1, err)
            self.assertIn("FAIL could not evaluate the SuccessfulDelete events", err)

    def test_shell_block_fails_when_the_event_listing_fails(self):
        rc, err, out = self.run_block([EVENT_1_36], k_fails=True)
        self.assertEqual(rc, 1)
        self.assertIn("FAIL could not list the SuccessfulDelete events", err)

    def test_shell_block_rejects_a_malformed_since(self):
        rc, err, out = self.run_block([EVENT_1_36], since="garbage")
        self.assertEqual(rc, 1)
        self.assertIn("FAIL could not evaluate the SuccessfulDelete events", err)
        self.assertIn("since must be", err)


if __name__ == "__main__":
    unittest.main()

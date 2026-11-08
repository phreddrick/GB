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
other component, other Pod or verb, event before the rollout or without a
time), the report must carry expected and observed values for each one,
malformed arguments or input must fail closed, and kind-e2e.sh must call the
program with every argument. Requires jq (the runner image and igor have it).
"""
from __future__ import annotations

import copy
import json
import os
import re
import shutil
import subprocess
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
    """Run the program; return (returncode, parsed report or None, stderr)."""
    data = json.dumps({"apiVersion": "v1", "kind": "List", "items": items}) if raw is None else raw
    proc = subprocess.run(
        ["jq", "-f", PROGRAM, "--arg", "uid", uid, "--arg", "set", set_name, "--arg", "pod", pod, "--arg", "since", since],
        input=data, capture_output=True, text=True, check=False,
    )
    report = json.loads(proc.stdout) if proc.returncode == 0 and proc.stdout.strip() else None
    return proc.returncode, report, proc.stderr


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
        rc, report, err = run([])
        self.assertEqual(rc, 0, err)
        self.assertEqual(report["matched"], [])
        self.assertEqual(report["candidates"], [])
        self.assertEqual(report["expected"]["involvedObject_uid"], UID)
        self.assertEqual(report["expected"]["component"], "statefulset-controller")
        self.assertEqual(report["expected"]["not_before"], SINCE)

    def test_kubernetes_1_36_title_cased_message_matches(self):
        rc, report, err = run([EVENT_1_36])
        self.assertEqual(rc, 0, err)
        self.assertEqual(report["matched"], [EVENT_1_36["metadata"]["name"]])
        checks = report["candidates"][0]["checks"]
        self.assertTrue(all(c["ok"] for c in checks.values()), checks)
        self.assertEqual(checks["message"]["observed"], "Delete Pod gb-engine-0 in StatefulSet gb-engine successful")
        self.assertEqual(checks["time"]["observed"], "2026-11-08T22:05:40Z")

    def test_older_lower_case_message_matches(self):
        rc, report, err = run([variant(message="delete Pod gb-engine-0 in StatefulSet gb-engine successful")])
        self.assertEqual(rc, 0, err)
        self.assertEqual(len(report["matched"]), 1)

    def test_events_v1_shape_matches_and_same_second_microseconds_count(self):
        ev = variant(source={}, firstTimestamp=None, lastTimestamp=None, eventTime="2026-11-08T22:05:13.482913Z")
        rc, report, err = run([ev])
        self.assertEqual(rc, 0, err)
        self.assertEqual(len(report["matched"]), 1)
        checks = report["candidates"][0]["checks"]
        self.assertEqual(checks["component"]["observed"], "statefulset-controller")
        self.assertEqual(checks["time"]["observed"], "2026-11-08T22:05:13.482913Z")

    def test_other_statefulset_uid_is_rejected_with_both_values(self):
        rc, report, err = run([variant(involvedObject={**EVENT_1_36["involvedObject"], "uid": "00000000-0000-4000-8000-000000000000"})])
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
                rc, report, err = run([variant(source=source, reportingComponent=reporting)])
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
                rc, report, err = run([variant(message=message)])
                self.assertEqual(rc, 0, err)
                self.assertOnlyFails(report, "message")
                self.assertEqual(report["candidates"][0]["checks"]["message"]["expected"],
                                 "Delete Pod gb-engine-0 in StatefulSet gb-engine successful")
        rc, report, err = run([variant(message=_ABSENT)])
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
                rc, report, err = run([variant(**changes)])
                self.assertEqual(rc, 0, err)
                self.assertOnlyFails(report, "time")
                self.assertEqual(report["candidates"][0]["checks"]["time"]["expected"], "not before " + SINCE)

    def test_same_second_as_the_rollout_start_is_accepted(self):
        rc, report, err = run([variant(lastTimestamp=SINCE)])
        self.assertEqual(rc, 0, err)
        self.assertEqual(len(report["matched"]), 1)

    def test_multiple_predicates_are_reported_together(self):
        ev = variant(message="delete Pod gb-engine-1 in StatefulSet gb-engine successful", source={"component": "kubelet"}, reportingComponent="kubelet")
        rc, report, err = run([ev])
        self.assertEqual(rc, 0, err)
        self.assertOnlyFails(report, "component", "message")

    def test_only_the_conforming_event_is_matched(self):
        bad = variant(involvedObject={**EVENT_1_36["involvedObject"], "uid": "other"}, metadata={"name": "gb-engine.bad"})
        good = variant(metadata={"name": "gb-engine.good"})
        rc, report, err = run([bad, good])
        self.assertEqual(rc, 0, err)
        self.assertEqual(report["matched"], ["gb-engine.good"])
        self.assertEqual([c["name"] for c in report["candidates"]], ["gb-engine.bad", "gb-engine.good"])

    def test_malformed_since_or_input_fails_closed(self):
        for since in ("garbage", "2026-11-08T22:05:13", "2026-11-08T22:05:13.000Z", "2026-11-08 22:05:13Z", ""):
            with self.subTest(since=since):
                rc, report, err = run([EVENT_1_36], since=since)
                self.assertNotEqual(rc, 0)
                self.assertIsNone(report)
                self.assertIn("since must be", err)
        for raw in ("[]", "{}", '{"items": {}}', "null", '"text"'):
            with self.subTest(raw=raw):
                rc, report, err = run([], raw=raw)
                self.assertNotEqual(rc, 0)
                self.assertIsNone(report)
                self.assertIn("EventList", err)

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
        # The decision is the program's `matched` list, and a failure prints every predicate.
        self.assertIn("'.matched | length'", script)
        self.assertIn("MISMATCH", script)
        self.assertNotIn('test("delete Pod', script)


if __name__ == "__main__":
    unittest.main()

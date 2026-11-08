# Did the StatefulSet controller itself delete a Pod of this StatefulSet after
# a given instant? Used by kind-e2e.sh for the quiesced rolling update
# (CR-43, ADR-0010) and compiled there before the cluster is created.
#
# Input: the core/v1 EventList that
#   kubectl get events --field-selector reason=SuccessfulDelete,involvedObject.name=<set> -o json
# returns (an object with an `items` array; anything else is an error).
#
# Named arguments:
#   uid    the StatefulSet's metadata.uid at the time the rollout began
#   set    the StatefulSet's name
#   pod    the Pod the controller must have deleted
#   since  the instant the rollout began, RFC 3339 UTC at second precision
#          (YYYY-MM-DDTHH:MM:SSZ, as `date -u +%Y-%m-%dT%H:%M:%SZ` prints it)
#
# Output: one object {expected, candidates, matched}. `candidates` carries
# every input event with the expected and observed value of each predicate
# and whether it held, so a failure names the mismatch instead of guessing;
# `matched` lists the names of the events for which every predicate held.
# The caller fails unless `matched` is non-empty.
#
# Predicates, and why each is required:
#   uid        binds the event to this StatefulSet object, not a namesake.
#   component  binds it to the statefulset-controller: a manual
#              `kubectl delete pod` records no event on the StatefulSet at
#              all, and nothing else writes SuccessfulDelete against it.
#   message    binds it to the replaced Pod. Compared case-insensitively:
#              kube-controller-manager title-cases the verb ("Delete Pod
#              gb-engine-0 in StatefulSet gb-engine successful";
#              pkg/controller/statefulset/stateful_pod_control.go,
#              recordPodEvent, cases.Title), older releases wrote "delete
#              Pod ...", and the first hosted runs of this check
#              (42527820295, 42527827925) failed on that difference alone.
#   time       binds it to this rollout: the event's instant, at second
#              precision, is not before `since`. The legacy recorder sets
#              lastTimestamp (seconds); an event written through
#              events.k8s.io/v1 and read as core/v1 carries eventTime
#              (microseconds) and reportingComponent instead of
#              source.component. Both forms are accepted.
def nonempty: if . == null or . == "" then empty else . end;
def component: ((.source.component // "") | nonempty) // ((.reportingComponent // "") | nonempty) // "";
def instant: ((.lastTimestamp // "") | nonempty) // ((.eventTime // "") | nonempty) // "";
def seconds: tostring | .[0:19];
def check($expected; $observed; $ok): {expected: $expected, observed: $observed, ok: $ok};

if ($since | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$") | not) then
  error("since must be an RFC 3339 UTC instant at second precision, got " + ($since | tojson))
elif (type != "object") or ((.items | type) != "array") then
  error("input is not an EventList with an items array")
else
  ("Delete Pod " + $pod + " in StatefulSet " + $set + " successful") as $message
  | {
      expected: {
        involvedObject_uid: $uid,
        component: "statefulset-controller",
        message: ($message + " (compared case-insensitively)"),
        not_before: $since
      },
      candidates: [
        .items[]
        | {
            name: (.metadata.name // "(unnamed event)"),
            checks: {
              uid: check($uid; .involvedObject.uid; (.involvedObject.uid == $uid)),
              component: check("statefulset-controller"; component; (component == "statefulset-controller")),
              message: check($message; .message; ((.message // "" | ascii_downcase) == ($message | ascii_downcase))),
              time: check(("not before " + $since); instant;
                          ((instant | length) >= 19 and (instant | seconds) >= ($since | seconds)))
            }
          }
        | .ok = ([.checks[] | .ok] | all)
      ]
    }
  | .matched = [.candidates[] | select(.ok) | .name]
end

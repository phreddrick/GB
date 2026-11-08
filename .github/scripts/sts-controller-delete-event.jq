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
#          (YYYY-MM-DDTHH:MM:SSZ, as `date -u +%Y-%m-%dT%H:%M:%SZ` prints it);
#          anything else, including a value in that shape that is not a
#          parseable instant, is an error
#
# Output: exactly one object {expected, candidates, matched}. `candidates`
# carries every input event with the expected and observed value of each
# predicate and whether it held, so a failure names the mismatch instead of
# guessing; `matched` lists the names of the events for which every
# predicate held. The caller requires exactly one report and a non-empty
# `matched`.
#
# Predicates, and why each is required:
#   uid        binds the event to this StatefulSet object, not a namesake.
#   component  binds it to the statefulset-controller: a manual
#              `kubectl delete pod` records no event on the StatefulSet,
#              and nothing else writes SuccessfulDelete against it.
#   message    binds it to the replaced Pod. Compared case-insensitively:
#              kube-controller-manager title-cases the verb ("Delete Pod
#              gb-engine-0 in StatefulSet gb-engine successful";
#              pkg/controller/statefulset/stateful_pod_control.go,
#              recordPodEvent, cases.Title), older releases wrote "delete
#              Pod ...", and the first hosted runs of this check
#              (42527820295, 42527827925) failed on that difference alone.
#   time       binds it to this rollout: the event's instant, parsed at
#              second precision, is not before `since`. The legacy recorder
#              sets lastTimestamp (seconds); an event written through
#              events.k8s.io/v1 and read as core/v1 carries eventTime
#              (microseconds) and reportingComponent instead of
#              source.component. Both forms are accepted; an instant that
#              does not parse fails the predicate.
#
# What a match proves: the controller issued the deletion of this Pod for
# this StatefulSet during this rollout. Kubernetes records SuccessfulDelete
# also when the controller's request finds the Pod already gone
# (DeleteStatefulPod treats NotFound as success), and the message names the
# Pod, not its UID. In the kind job nothing else deletes a Pod, so the event
# together with the job's own sequence is the evidence that no manual
# deletion was needed; it is not an audit-grade proof of who deleted.
def nonempty: if . == null or . == "" then empty else . end;
def component: ((.source.component // "") | nonempty) // ((.reportingComponent // "") | nonempty) // "";
def instant: ((.lastTimestamp // "") | nonempty) // ((.eventTime // "") | nonempty) // "";
# Seconds since the epoch of an RFC 3339 UTC instant (YYYY-MM-DDTHH:MM:SS[.fraction]Z) truncated to seconds,
# or null when the value has another shape or does not parse. Kubernetes serialises every timestamp in UTC.
def epoch: tostring | if test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$")
                      then (try (.[0:19] + "Z" | fromdateiso8601) catch null) else null end;
def check($expected; $observed; $ok): {expected: $expected, observed: $observed, ok: $ok};

($since | try fromdateiso8601 catch null) as $since_epoch
| if ($since | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$") | not) or $since_epoch == null then
    error("since must be a parseable RFC 3339 UTC instant at second precision (YYYY-MM-DDTHH:MM:SSZ), got " + ($since | tojson))
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
          | (instant | epoch) as $at
          | {
              name: (.metadata.name // "(unnamed event)"),
              checks: {
                uid: check($uid; .involvedObject.uid; (.involvedObject.uid == $uid)),
                component: check("statefulset-controller"; component; (component == "statefulset-controller")),
                message: check($message; .message; ((.message // "" | ascii_downcase) == ($message | ascii_downcase))),
                time: check(("not before " + $since); instant; ($at != null and $at >= $since_epoch))
              }
            }
          | .ok = ([.checks[] | .ok] | all)
        ]
      }
    | .matched = [.candidates[] | select(.ok) | .name]
  end

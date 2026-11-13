# Independent final PR78 promotion implementation review

Reviewer: separate read-only Codex agent `/root/pr78_readiness_review`. Final source/evidence review completed2026-11-13UTC. Reviewed worktree `/tmp/GB-pr78-promotion`, baseline1a31db4da30fa5953aeaf25ed7bcc3d5130f343c. Source and evidence hashes are in `review-final-inputs.json`.

## Verdict

**APPROVE FOR PR. Initial blocking finding R1 is resolved; R2 evidence completeness is resolved. No remaining blocking source/security defect was identified.** Maintainer acknowledgment `gate-change-reviewed` is required before the protected-tooling gate can pass. No publication, retained-release upload, deployment or operational action is approved by this review. Actual hosted publication/registry integration is deliberately unexecuted and requires separate owner authorization after review/merge/control CI.

## Design and security review

The implementation is a closed promotion of approved7a17a1bc7edf75091386f30944a529a0ccf48d8d, not a generic arbitrary-source publisher. Profile IDs, ZIP sizes/hashes/member sets, OCI/Docker archive identities, source/run/attempt and image manifests/configs agree with independently reviewed preserved artifacts. Current master/PR79 can supply control code/CI validation, but cannot supply promoted image bytes.

Both control-source CI and original43451208400/attempt1 seven-job provenance are checked. Fixed original downloads fail closed on expiry/unavailability; explicit retained-release delivery still requires current original CI evidence, correct repository, owner release/asset uploader IDs and exact pinned bytes. There is no implicit fallback or caller-supplied URL/source/image namespace. Downloads use bounded HTTPS reads/redirects, remove Authorization on redirects and suppress sensitive request/error details. Only allowlisted proof fields are handed off. The privileged job downloads only its current workflow run/attempt artifact and revalidates all pins, identities and safe archive structure before any write.

The workflow retains one packages-write job, the existing master guard/environment/concurrency and default current-master build behavior. Promotion excludes game builds, old master-tag publisher and automatic digest PR; conflicting inputs fail. The unchanged original publisher, record validator, scanner policy, provenance validator, static workflow policy and CI workflow were independently compared with HEAD and are byte-identical. No game/storage/authentication/Kubernetes source or schema changed.

Before promotion, actual fresh Trivy reports must match exact layouts/archives and pass the unchanged severity/secret policy. Report timestamps/version are checked, actual per-image public DB metadata/content hashes are recorded, and overdue/inconsistent metadata fails. The action's exact cache path and trivy-db JSON field casing were independently checked against pinned primary sources. Expected/observed config identities and version/DB evidence are now bound into the separate closed record. Original SPDX evidence is retained and source-bound; no fresh SBOM execution is falsely claimed.

Production registry operations permit only two fixed source tags. Both destinations are checked before upload; conflicting content refuses all writes, identical content is independently verified without rewriting. Each upload attempt is fsynced to a journal before invoking crane, so a failure/timeout is partial/unknown rather than rollback. Each image is retrieved by immutable digest and fully reverified through manifest/config/blob/ordered-DiffID checks. Pinned crane0.22.1 source independently confirms `pull --format=oci` produces the expected layout. There is no master-tag writer, automatic retry, rollback/delete or exactly-once claim. Registry compare-and-swap is unavailable: an effective owner-controlled exclusive writer window remains a prerequisite, and before/after master equality is only an observation, not proof against intervening ABA changes.

Final records use checked no-replace creation/readback. Diagnostic journals now have a distinct kind, explicit provisional/finalization phase and accepted-record flag. A failed record finalization cannot leave a journal accepted as a completed promotion record; confirmed registry effects remain visible and are not described as rolled back. Later reporting/upload failure cannot undo uploads. Abrupt runner loss can prevent evidence upload, as the documentation now states.

## Findings and correction history

- **R1, blocking, RESOLVED:** independent fault injection originally left no final file but a validator-accepted complete journal checkpoint. Source reproduction and original hashes are preserved. The corrected distinct journal contract plus permanent regression and fresh independent reproduction now produce `record_finalisation_failed`, accepted_record=false and validator rejection, while preserving confirmed registry effects.
- **R2, evidence completeness, RESOLVED:** explicit local/observed config digests and actual scanner DB/version evidence were added. Tests reject overdue/invalid DB evidence and record tampering; existing scanner policy is unchanged.
- No additional blocking finding arose from source, authority, transport, workflow, registry, record or test review. No unsafe source edit or intentional negative control was committed by this reviewer.

Historical unsuccessful attempts remain documented: author fixture-interface failures (including an initially masked shell exit), the review's rejected old sanitized job fixture lacking head_sha, and initial ad hoc ShellCheck info-level findings in untouched legacy scripts. The successful real-artifact rerun uses fresh full provenance, not invented fields. Exact existing CI ShellCheck severity was then run successfully; no suppression or threshold was changed.

## Independent execution and acceptance evidence

The reviewer personally executed final28promotion tests (0.54s wall) and all216workflow/helper tests (16.07s wall), with source hashes unchanged before/after. The real fixed ZIPs were independently admitted by the production profile/helper using fresh full original CI proof, offline and without executing images. These tests use synthetic API/registry behavior for writes; they are not a real upload or publication acceptance.

The reviewer inspected the author's timestamped full local gate results:

- Go1.27.4 build/gofmt/vet/module verify/tidy: PASS.
- Complete normal Go suite: PASS,44.527s wall.
- Exact complete race suite: PASS,326.372s wall; game315.152s.
- GB_LONG_TESTS=1 complete race suite: PASS,353.61s wall; game351.535s, approximately41.4% headroom below unchanged600s. The environment prefix and GOTOOLCHAIN=local are explicitly preserved in execution-notes.json; this supplements the original runner records rather than rewriting them.
- Original C fixture check, inventory, all15golden mutation scenarios and gate self-tests: PASS.
- Lint:1000baseline findings matched1000reported, zero new/stale; no baseline changed.
- Actionlint, exact CI ShellCheck `-S warning`, static workflow policy, offline manifests and git diff check: PASS. Manifests are local validation only, not cluster operations.
- Strict govulncheck: PASS, zero affected/reachable code;17noncalled module vulnerabilities are explicitly reported, not suppressed or described as vulnerability-free.
- Growth/acknowledgment gate: correctly refuses protected changes without the maintainer's gate-change-reviewed label (exit1). This is an outstanding approval condition, not a fabricated technical pass. No label was simulated/applied. No known-defect-added acknowledgment is required because inventory/allowances/baseline are unchanged.

Complete logs/commands/hashes are preserved privately and are to be bound into the tracked validation ledger after this report, avoiding a circular report hash. Hosted CI job results are not invented; they must be reported from the eventual PR separately. Hosted image builds/scans/kind remain GitHub Actions work. A promotion workflow dispatch itself remains unauthorized.

## Documentation and remaining boundaries

The implementation document accurately covers original versus control identity, expiry/retention transfer, owner permissions, exclusive publication window, uncertainty, unchanged master tags, scanner limits, record finalization and separate later digest/deployment approvals. It does not silently declare the existing deployment READY or the GitLab credential incident resolved. The historical live observation and recovery point remain dated evidence, not a fresh current-state assertion.

No live publication, registry mutation, workflow dispatch, retained-release upload, original GitLab credential retrieval/testing, image execution, deployment, database access or Kubernetes operation was performed by the reviewer. Existing worktrees/source were not edited. The sole writes were private review logs/results; disposable test fixtures were isolated.

Primary pinned-source references checked: https://raw.githubusercontent.com/aquasecurity/trivy-action/ed142fd0673e97e23eac54620cfb913e5ce36c25/action.yaml ; https://raw.githubusercontent.com/aquasecurity/trivy-db/cbebf2ecd6df/pkg/metadata/metadata.go . These establish parser/cache expectations, not proof of a production scan execution.

## Final reviewed source hashes

- `.github/release/pr78.json`: `e46f9bac1d354f1a55bb35149b1b2774e95b33735ebfaa016dbc239c2498950a`
- `.github/scripts/pr78_artifacts.py`: `747b5d68d9be2da4cff4c729ca9028b9f15ef935338a5399ba7153cc73e3f16a`
- `.github/scripts/pr78_promotion.py`: `41463d2fff09c846031c6381bd4879df00a8c496c8492bd13f4582a2d17ef2f4`
- `.github/scripts/test_pr78_promotion.py`: `020ea2ff0a406d11d0d662bbd159d5ab75ae473623054aef6205b3e4dfa846a0`
- `.github/workflows/publish-images.yml`: `b4d566d87b6fd83ad0cc1e5635f9b1fd5bb5da9a1f0706f2b3b0d70da55e4d22`
- `docs/testing/pr78-artifact-promotion.md`: `8542da809226792d4905915c1bd5684e156663e94754de892aca534d72acb6f3`

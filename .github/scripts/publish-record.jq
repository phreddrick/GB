# Publish record (gb-publish-record, schema_version 2), rendered by
# publish-push.sh with `jq -n -f publish-record.jq --arg ...`. The record is
# validated by publish_record.py before it is moved into place, so the
# consumer contract (status, complete, master_tag_applied, images[].*) is
# checked, not assumed. Compiled once at script start, before any push, so a
# syntax error here can never surface after an image has been published.
#
# Named arguments: status, master, sha, run, epoch, ns, trivy,
# s_state s_local s_remote s_config s_size (gb-server),
# c_state c_local c_remote c_config c_size (gbctl).
def nullable($v): if $v == "" then null else $v end;
def image($name; $target; $state; $local; $remote; $config; $size; $tagged):
  { repository: ($ns + "/" + $name),
    target: $target,
    state: $state,
    local_manifest_digest: nullable($local),
    published_manifest_digest: nullable($remote),
    config_digest: nullable($config),
    compressed_size: ($size | tonumber),
    tags: (if ($state == "verified" or $state == "pushed_unverified")
           then ([$sha] + (if $tagged == "true" then ["master"] else [] end))
           else [] end),
    pull: (if $state == "verified" then ($ns + "/" + $name + "@" + $remote) else null end) };
{ kind: "gb-publish-record",
  schema_version: 2,
  status: $status,
  complete: ($status == "published"),
  master_tag_applied: ($master == "true"),
  source_commit: $sha,
  workflow_run: $run,
  source_date_epoch: $epoch,
  platform: "linux/amd64",
  registry: "https://ghcr.io",
  dockerfile: "deploy/docker/Dockerfile",
  context: "go/",
  scan: { tool: $trivy,
          policy: "fail on CRITICAL with a fix available or any secret",
          status: "passed before push (see trivy-*.json in this artifact)" },
  sbom: { format: "spdx-json", artifact: "publish-<commit> artifact, sbom/<image>.spdx.json" },
  digest_kind: "single platform-specific OCI image manifest (no index: provenance and sbom attestations disabled); reference this digest from Kustomize",
  images: { "gb-server": image("gb-server"; "server"; $s_state; $s_local; $s_remote; $s_config; $s_size; $master),
            "gbctl":     image("gbctl";     "gbctl";  $c_state; $c_local; $c_remote; $c_config; $c_size; $master) } }
| . + { published: [ .images | to_entries[] | select(.value.state == "verified" or .value.state == "pushed_unverified")
                      | { name: .key, reference: (.value.repository + ":" + $sha), digest: .value.published_manifest_digest, state: .value.state } ] }

#!/usr/bin/env python3
"""Closed PR78 artifact admission. No registry writes, builds or arbitrary URLs.

CLI uses only the checked-in fixed profile; test functions may receive fixtures.
GitHub credentials remain in request headers, never redirected or diagnosed.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tarfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile

import ci_provenance
import docker_image_identity
import oci_digest

HERE = Path(__file__).resolve().parent


class Refused(Exception):
    pass


def require(condition, reason):
    if not condition:
        raise Refused(reason)


def pairs(items):
    result = {}
    for k, v in items:
        require(k not in result, 'duplicate JSON property')
        result[k] = v
    return result


def decode(data):
    return json.loads(data, object_pairs_hook=pairs)


PROFILE = decode((HERE.parent / 'release/pr78.json').read_bytes())


def sha(data):
    return hashlib.sha256(data).hexdigest()


def write_json(path, value):
    with open(path, 'x', encoding='utf-8') as f:
        os.chmod(path, 0o600)
        json.dump(value, f, indent=2)
        f.write('\n')


def context():
    require(os.environ.get('GITHUB_REPOSITORY') == PROFILE['repository'], 'wrong repository')
    require(os.environ.get('GITHUB_REPOSITORY_ID') == str(PROFILE['repository_id']) and os.environ.get('GITHUB_REPOSITORY_OWNER_ID') == '1794145', 'wrong immutable repository authority')
    require(os.environ.get('GITHUB_REF') == 'refs/heads/master', 'master dispatch only')
    require(os.environ.get('GITHUB_EVENT_NAME') == 'workflow_dispatch', 'manual dispatch only')
    require(os.environ.get('CONFIRM') == 'promote-pr78-exclusive', 'typed exclusive publication confirmation required')
    require(os.environ.get('OPEN_DIGEST_PR', 'false') == 'false', 'promotion cannot open a deployment PR')
    require(re.fullmatch('[0-9a-f]{40}', os.environ.get('GITHUB_SHA', '')), 'invalid control commit')
    require(os.environ.get('GITHUB_WORKFLOW_REF') == 'phreddrick/GB/.github/workflows/publish-images.yml@refs/heads/master', 'wrong control workflow')
    for key in ['GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT']:
        require(re.fullmatch('[1-9][0-9]*', os.environ.get(key, '')), 'invalid promotion run identity')


class Redirects(urllib.request.HTTPRedirectHandler):
    max_redirections = 3

    @staticmethod
    def allowed(url):
        u = urllib.parse.urlsplit(url)
        h = u.hostname or ''
        return (u.scheme == 'https' and not u.username and not u.password and u.port in (None, 443)
                and (h in ['api.github.com', 'github.com'] or h.endswith('.githubusercontent.com')
                     or h.endswith('.blob.core.windows.net')))

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        require(self.allowed(newurl), 'unsafe GitHub download redirect')
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        # urllib may otherwise forward the API Authorization header to signed storage.
        redirected.remove_header('Authorization')
        return redirected


class API:
    def __init__(self):
        self.opener = urllib.request.build_opener(Redirects())

    def get(self, endpoint, binary=False, limit=4_000_000):
        require(not endpoint.startswith('/') and '..' not in endpoint and ':' not in endpoint, 'invalid API endpoint')
        url = 'https://api.github.com/repos/' + PROFILE['repository'] + '/' + endpoint
        # Actions ZIP APIs negotiate JSON before redirecting to raw bytes;
        # only Release asset APIs need octet-stream to select binary content.
        accept = 'application/octet-stream' if binary and endpoint.startswith('releases/assets/') else 'application/vnd.github+json'
        headers = {'Accept': accept,
                   'X-GitHub-Api-Version': '2022-11-28', 'Authorization': 'Bearer ' + os.environ['GH_TOKEN']}
        try:
            with self.opener.open(urllib.request.Request(url, headers=headers), timeout=60) as r:
                require(Redirects.allowed(r.geturl()), 'unsafe download destination')
                data = r.read(limit + 1)
                require(len(data) <= limit, 'GitHub response exceeds bound')
                return data if binary else decode(data)
        except (urllib.error.URLError, TimeoutError) as e:
            # Exception text can contain signed URLs or headers: never print it.
            raise Refused('GitHub evidence unavailable (' + type(e).__name__ + ')') from None


def provenance(run, jobs, profile=PROFILE):
    require(run.get('id') == profile['run_id'] and run.get('run_attempt') == profile['attempt'], 'wrong original CI run/attempt')
    selected, _ = ci_provenance.select_run({'total_count': 1, 'workflow_runs': [run]},
        repository=profile['repository'], workflow_path='.github/workflows/ci.yml',
        sha=profile['source'], branch='master', event='push')
    require(selected is not None, 'no successful exact-source master push CI')
    ok, _ = ci_provenance.verify_jobs(jobs, run_id=profile['run_id'], run_attempt=profile['attempt'],
                                    sha=profile['source'], required=profile['required_jobs'])
    require(ok and len(jobs['jobs']) == 7, 'missing, failed or mixed seven-job CI evidence')


def artifact_metadata(data, name, profile=PROFILE):
    a = profile['artifacts'][name]
    require(data.get('id') == a['id'] and data.get('name') == name, 'wrong artifact identity')
    require(data.get('expired') is False, 'original artifact expired; explicit retained release required')
    require(data.get('digest') == 'sha256:' + a['sha256'] and data.get('size_in_bytes') == a['bytes'], 'artifact digest/size mismatch')
    r = data.get('workflow_run', {})
    require(r.get('id') == profile['run_id'] and r.get('head_sha') == profile['source']
            and r.get('head_branch') == 'master' and r.get('repository_id') == profile['repository_id']
            and r.get('head_repository_id') == profile['repository_id'], 'artifact from wrong CI/source/repository')
    try:
        expires = dt.datetime.fromisoformat(data['expires_at'].replace('Z', '+00:00'))
        require(expires > dt.datetime.now(dt.timezone.utc), 'original artifact expired')
    except (KeyError, ValueError, TypeError):
        raise Refused('missing artifact expiration') from None


def stage(root, api, retained_id='', profile=PROFILE):
    root.mkdir(mode=0o700)
    run = api.get('actions/runs/' + str(profile['run_id']))
    jobs = api.get('actions/runs/' + str(profile['run_id']) + '/attempts/1/jobs?per_page=100')
    provenance(run, jobs, profile)
    assets = None
    if retained_id:
        require(re.fullmatch('[1-9][0-9]{0,18}', retained_id), 'invalid retained release ID')
        release = api.get('releases/' + retained_id)
        require(release.get('id') == int(retained_id) and release.get('draft') is False
                and release.get('author', {}).get('login') == 'phreddrick' and release.get('author', {}).get('id') == 1794145, 'retention release must be owner-prepared and published')
        # Paginate the actual assets endpoint; never trust an embedded/truncated assets list.
        assets = []
        for page in range(1, 11):
            batch = api.get('releases/' + retained_id + '/assets?per_page=100&page=' + str(page))
            require(isinstance(batch, list), 'malformed release asset page')
            assets.extend(batch)
            if len(batch) < 100:
                break
        else:
            raise Refused('release assets exceed pagination bound')
    transport = {'kind': 'retained-release' if assets is not None else 'original-ci', 'release_id': retained_id or None, 'items': {}}
    for name, expected in profile['artifacts'].items():
        if assets is None:
            meta = api.get('actions/artifacts/' + str(expected['id']))
            artifact_metadata(meta, name, profile)
            endpoint = 'actions/artifacts/' + str(expected['id']) + '/zip'
        else:
            found = [a for a in assets if a.get('name') == name + '.zip']
            require(len(found) == 1, 'missing or duplicate retained artifact')
            meta = found[0]
            require(type(meta.get('id')) is int and meta['id'] > 0 and meta.get('state') == 'uploaded'
                    and meta.get('uploader', {}).get('login') == 'phreddrick' and meta.get('uploader', {}).get('id') == 1794145
                    and meta.get('size') == expected['bytes']
                    and meta.get('digest') == 'sha256:' + expected['sha256'], 'retained asset identity mismatch')
            endpoint = 'releases/assets/' + str(meta['id'])
        data = api.get(endpoint, binary=True, limit=expected['bytes'])
        require(len(data) == expected['bytes'] and sha(data) == expected['sha256'], 'artifact ZIP checksum mismatch')
        path = root / (name + '.zip')
        path.write_bytes(data)
        path.chmod(0o600)
        transport['items'][name] = {'id': meta['id'], 'sha256': sha(data)}
    # Save only the CI fields needed to independently validate the handoff.
    kept_run = {k: run[k] for k in ['id', 'run_attempt', 'path', 'head_sha', 'head_branch', 'event', 'status', 'conclusion']}
    for k in ['repository', 'head_repository']:
        kept_run[k] = {'full_name': run[k]['full_name']}
    kept_jobs = {'total_count': jobs['total_count'], 'jobs': [{k: j[k] for k in ['id', 'name', 'run_id', 'run_attempt', 'head_sha', 'status', 'conclusion']} for j in jobs['jobs']]}
    write_json(root / 'provenance.json', {'run': kept_run, 'jobs': kept_jobs, 'transport': transport})


def transport_proof(proof, profile=PROFILE):
    require(set(proof) == {'kind', 'release_id', 'items'} and proof['kind'] in ['original-ci', 'retained-release'], 'invalid artifact transport proof')
    if proof['kind'] == 'original-ci':
        require(proof['release_id'] is None, 'CI transport cannot claim a release')
    else:
        require(isinstance(proof['release_id'], str) and re.fullmatch('[1-9][0-9]{0,18}', proof['release_id']), 'invalid retained release identity')
    require(set(proof['items']) == set(profile['artifacts']), 'incomplete artifact transport proof')
    for name, item in proof['items'].items():
        require(set(item) == {'id', 'sha256'} and type(item['id']) is int and item['id'] > 0, 'invalid artifact transport item')
        require(item['sha256'] == profile['artifacts'][name]['sha256'], 'wrong transport checksum')
        if proof['kind'] == 'original-ci':
            require(item['id'] == profile['artifacts'][name]['id'], 'wrong original artifact ID')


def safe_tar(path):
    seen = set()
    with tarfile.open(path) as t:
        for m in t.getmembers():
            n = m.name.removeprefix('./').rstrip('/')
            require(n not in seen and (m.isfile() or m.isdir()), 'unsafe or duplicate TAR member')
            require(n in ['index.json', 'oci-layout', 'manifest.json', 'blobs', 'blobs/sha256']
                    or re.fullmatch('blobs/sha256/[0-9a-f]{64}', n), 'unexpected TAR path')
            seen.add(n)


def verify(root, out, profile=PROFILE):
    proof = decode((root / 'provenance.json').read_bytes())
    provenance(proof['run'], proof['jobs'], profile)
    transport_proof(proof['transport'], profile)
    out.mkdir(mode=0o700)
    for name, a in profile['artifacts'].items():
        zpath = root / (name + '.zip')
        require(zpath.is_file() and not zpath.is_symlink() and zpath.stat().st_size == a['bytes']
                and sha(zpath.read_bytes()) == a['sha256'], 'artifact ZIP checksum mismatch')
        dest = out / name
        dest.mkdir(mode=0o700)
        with zipfile.ZipFile(zpath) as z:
            members = z.infolist()
            require(len(members) == len(a['entries']) and {m.filename for m in members} == set(a['entries']), 'unexpected ZIP paths')
            require(sum(m.file_size for m in members) < 100_000_000, 'oversized ZIP')
            for m in members:
                require(stat.S_ISREG(m.external_attr >> 16) and not m.flag_bits & 1, 'ZIP link or unsupported entry')
                require('/' not in m.filename and '\\' not in m.filename and m.filename not in ['.', '..'], 'unsafe ZIP path')
                f = dest / m.filename
                f.write_bytes(z.read(m))
                f.chmod(0o600)
    record = decode((out / 'image-build-record/image-build-record.json').read_bytes())
    require(record.get('kind') == 'gb-image-build-record' and record.get('source_commit') == profile['source']
            and record.get('workflow_run') == 'https://github.com/phreddrick/GB/actions/runs/' + str(profile['run_id']) + '/attempts/1'
            and record.get('platform') == 'linux/amd64' and record.get('go_toolchain') == '1.27.4', 'build record source/provenance mismatch')
    identities = {}
    for name, a in profile['images'].items():
        archive = out / ('container-images/' + name + '.oci.tar')
        saved = out / ('container-images/' + name + '.docker.tar')
        for path, key in [(archive, 'oci_tar_sha256'), (saved, 'docker_tar_sha256')]:
            require(sha(path.read_bytes()) == a[key], 'image archive checksum mismatch')
            safe_tar(path)
        identity = oci_digest.inspect(str(archive))
        for k in ['manifest_digest', 'config_digest']:
            require(identity[k] == a[k] == record['images'][name][k], 'wrong approved image digest')
        require(identity['platform'] == {'os': 'linux', 'architecture': 'amd64'} and identity['user'] == '65532:65532', 'wrong platform or nonroot identity')
        require(identity['entrypoint'] == ['/usr/local/bin/' + name], 'wrong entrypoint')
        labels = identity['labels']
        require(labels.get('org.opencontainers.image.revision') == profile['source']
                and labels.get('org.opencontainers.image.version') == profile['source']
                and labels.get('org.opencontainers.image.source') == 'https://github.com/' + profile['repository'], 'wrong OCI source labels')
        docker_image_identity.verify(str(archive), str(saved), record['images'][name]['docker_image_id'], name + ':ci')
        sbom = decode((out / ('sboms/' + name + '.spdx.json')).read_bytes())
        described = [r['relatedSpdxElement'] for r in sbom['relationships'] if r['spdxElementId'] == sbom['SPDXID'] and r['relationshipType'] == 'DESCRIBES']
        source = [p for p in sbom['packages'] if p['SPDXID'] in described]
        require(sbom['spdxVersion'] == 'SPDX-2.3' and len(source) == 1 and source[0]['versionInfo'] == a['manifest_digest'], 'SBOM belongs to a different image')
        identities[name] = identity
    return proof, identities


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('operation', choices=['stage', 'verify'])
    ap.add_argument('input', type=Path)
    ap.add_argument('output', type=Path)
    args = ap.parse_args()
    try:
        context()
        if args.operation == 'stage':
            stage(args.input, API(), os.environ.get('RETAINED_RELEASE_ID', ''))
        verify(args.input, args.output)
        print('PR78 artifacts and exact seven-job source provenance verified; nothing published')
    except (Refused, ci_provenance.Malformed, oci_digest.ArchiveError, OSError, ValueError, KeyError, TypeError, tarfile.TarError, zipfile.BadZipFile) as e:
        print('PR78 artifact admission refused (' + type(e).__name__ + '): ' + str(e) if isinstance(e, Refused) else 'PR78 artifact admission refused (' + type(e).__name__ + ')')
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())

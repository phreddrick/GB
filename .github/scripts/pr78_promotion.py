#!/usr/bin/env python3
"""Commit-tag-only PR78 promotion. Separate contract; old publisher is untouched.

No build, master-tag writer, manifest editor, deployment or automatic retry.
Only the CLI's 'promote' operation writes a registry; tests inject a fake registry.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import hashlib
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile

import oci_digest
import oci_layout_dir
import pr78_artifacts as artifacts
import trivy_policy

PROFILE = artifacts.PROFILE
require = artifacts.require
Refused = artifacts.Refused
DIGEST = re.compile(r'sha256:[0-9a-f]{64}')


def database_evidence(cache, output):
    """Snapshot only public scanner-DB identity after one completed image scan."""
    def read_regular(path, limit):
        require(not path.is_symlink(), 'scanner DB evidence cannot be a link')
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, 'rb') as f:
            st = os.fstat(f.fileno())
            require(stat.S_ISREG(st.st_mode) and 0 < st.st_size <= limit, 'invalid scanner DB evidence file')
            digest = hashlib.sha256()
            content = bytearray()
            for chunk in iter(lambda: f.read(1 << 20), b''):
                digest.update(chunk)
                if limit == 65536:
                    content.extend(chunk)
            return bytes(content), digest.hexdigest()
    raw, metadata_hash = read_regular(cache / 'metadata.json', 65536)
    _, database_hash = read_regular(cache / 'trivy.db', 4 << 30)
    meta = artifacts.decode(raw)
    require(meta.get('Version') == 2, 'unexpected scanner DB schema')
    evidence = {'metadata_sha256': metadata_hash, 'database_sha256': database_hash,
                'Version': meta['Version'], 'UpdatedAt': meta['UpdatedAt'],
                'DownloadedAt': meta['DownloadedAt'], 'NextUpdate': meta['NextUpdate']}
    artifacts.write_json(output, evidence)


def validate_database(evidence, scan_time):
    require(set(evidence) == {'metadata_sha256', 'database_sha256', 'Version', 'UpdatedAt', 'DownloadedAt', 'NextUpdate'}, 'missing scanner DB evidence')
    require(type(evidence['Version']) is int and evidence['Version'] == 2 and all(re.fullmatch('[0-9a-f]{64}', evidence[k]) for k in ['metadata_sha256', 'database_sha256']), 'invalid scanner DB evidence')
    stamps = {k: dt.datetime.fromisoformat(evidence[k].replace('Z', '+00:00')) for k in ['UpdatedAt', 'DownloadedAt', 'NextUpdate']}
    require(stamps['UpdatedAt'] <= stamps['DownloadedAt'] <= scan_time + dt.timedelta(minutes=5), 'invalid DB dates')
    require(stamps['UpdatedAt'] < scan_time + dt.timedelta(minutes=5) and stamps['NextUpdate'] > scan_time, 'scanner DB overdue at scan')


def scan_gate(images, layout, scans, started, profile=PROFILE):
    now = dt.datetime.now(dt.timezone.utc)
    start = dt.datetime.fromisoformat(started.replace('Z', '+00:00'))
    require(dt.timedelta(0) <= now - start <= dt.timedelta(hours=2), 'invalid fresh scan window')
    version_file = scans / 'trivy-version.txt'
    require('Version: 0.76.1' in version_file.read_text(), 'missing scanner version evidence')
    result = {}
    for name in profile['images']:
        path = scans / ('trivy-' + name + '.json')
        report = trivy_policy.load(str(path))
        trivy_policy.validate(report, str(path))
        trivy_policy.check_layout_identity(report, str(path), str(layout / name), str(images / (name + '.oci.tar')))
        stamp = dt.datetime.fromisoformat(report['CreatedAt'].replace('Z', '+00:00'))
        require(start <= stamp <= now + dt.timedelta(minutes=5), 'scan is stale or future-dated')
        require(report.get('Trivy', {}).get('Version') == '0.76.1', 'wrong scanner version')
        _, violations, secrets, _ = trivy_policy.evaluate(report)
        require(not violations and not secrets, 'existing Trivy policy failed')
        database = artifacts.decode((scans / ('db-' + name + '.json')).read_bytes())
        validate_database(database, stamp)
        result[name] = {'sha256': artifacts.sha(path.read_bytes()), 'created_at': report['CreatedAt'], 'database': database}
    return {'tool': 'Trivy 0.76.1', 'started_at': started, 'version_evidence_sha256': artifacts.sha(version_file.read_bytes()), 'reports': result}


class Registry:
    """Only permitted write is push(layout, exact approved source tag)."""
    def call(self, args):
        try:
            return subprocess.run(['crane'] + args, capture_output=True, timeout=240)
        except (OSError, subprocess.TimeoutExpired):
            raise Refused('registry operation unavailable; outcome may be unknown') from None

    def digest(self, ref, allow_missing=False):
        r = self.call(['digest', ref])
        if r.returncode:
            # Only an explicit missing-manifest response means absent, never auth/network failure.
            missing = b'MANIFEST_UNKNOWN' in r.stderr or b'NAME_UNKNOWN' in r.stderr
            denied = any(x in r.stderr for x in [b'UNAUTHORIZED', b'DENIED', b'401', b'403'])
            if allow_missing and missing and not denied:
                return None
            raise Refused('registry identity unavailable')
        value = r.stdout.decode().strip()
        require(DIGEST.fullmatch(value), 'invalid registry digest response')
        return value

    def push(self, layout, ref):
        require(ref in ['ghcr.io/phreddrick/' + n + ':' + PROFILE['source'] for n in PROFILE['images']], 'unauthorized tag write')
        require(self.call(['push', str(layout), ref]).returncode == 0, 'upload failed; registry outcome unknown')

    def verify(self, ref, dest, expected):
        require(self.digest(ref) == expected['manifest_digest'], 'registry digest mismatch')
        r = self.call(['pull', ref, str(dest), '--format=oci'])
        require(r.returncode == 0, 'immutable registry retrieval failed')
        actual = oci_digest.inspect(str(dest))
        # Full remote compressed/decompressed blob verification happens in inspect().
        for k in ['manifest_digest', 'config_digest', 'layer_digests', 'diff_ids', 'platform', 'user', 'labels', 'entrypoint']:
            require(actual[k] == expected[k], 'registry OCI content mismatch')
        return actual['config_digest']


def validate_record(record, profile=PROFILE):
    require(set(record) == {'kind', 'status', 'complete', 'rebuilt', 'source', 'original_ci', 'control', 'transport',
                            'master_tags_before', 'master_tags_after', 'master_tags_unchanged', 'scan', 'sbom_zip_sha256',
                            'images', 'failure', 'recorded_at'}, 'unexpected promotion record fields')
    require(record['kind'] == 'gb-pr78-artifact-promotion-v1' and record['rebuilt'] is False, 'not an artifact promotion record')
    require(record['source'] == profile['source'] and record['original_ci'] == {'run_id': profile['run_id'], 'attempt': 1}, 'wrong promotion source/CI')
    recorded = dt.datetime.fromisoformat(record['recorded_at'])
    require(type(record['original_ci']['run_id']) is int and type(record['original_ci']['attempt']) is int, 'invalid original CI identity types')
    control = record['control']
    require(set(control) == {'commit', 'run_id', 'attempt'}, 'wrong control identity fields')
    require(re.fullmatch('[0-9a-f]{40}', control['commit']) and control['commit'] != profile['source'], 'control and image source identities conflated')
    require(type(control['run_id']) is int and type(control['attempt']) is int and control['run_id'] > 0 and control['attempt'] > 0, 'invalid control run')
    artifacts.transport_proof(record['transport'], profile)
    require(record['sbom_zip_sha256'] == profile['artifacts']['sboms']['sha256'], 'wrong SBOM evidence')
    require(set(record['scan']) == {'tool', 'started_at', 'version_evidence_sha256', 'reports'} and record['scan']['tool'] == 'Trivy 0.76.1', 'missing fresh scan evidence')
    require(re.fullmatch('[0-9a-f]{64}', record['scan']['version_evidence_sha256']), 'missing scanner evidence hash')
    require(set(record['scan']['reports']) == set(profile['images']), 'incomplete scan pair')
    scan_start = dt.datetime.fromisoformat(record['scan']['started_at'].replace('Z', '+00:00'))
    require(dt.timedelta(0) <= recorded - scan_start <= dt.timedelta(hours=2), 'invalid record scan window')
    for report in record['scan']['reports'].values():
        stamp = dt.datetime.fromisoformat(report['created_at'].replace('Z', '+00:00'))
        require(scan_start <= stamp <= recorded + dt.timedelta(minutes=5), 'inconsistent scan timestamps')
        validate_database(report['database'], stamp)
        require(set(report) == {'sha256', 'created_at', 'database'} and re.fullmatch('[0-9a-f]{64}', report['sha256']), 'invalid scan record')
    require(set(record['images']) == set(profile['images']), 'incomplete image record')
    for which in ['master_tags_before', 'master_tags_after']:
        require(set(record[which]) <= set(profile['images']), 'invalid master tag record')
        require(all(DIGEST.fullmatch(v) for v in record[which].values()), 'malformed master digest')
    unchanged = (set(record['master_tags_before']) == set(profile['images'])
                 and record['master_tags_before'] == record['master_tags_after'])
    require(type(record['master_tags_unchanged']) is bool and record['master_tags_unchanged'] == unchanged, 'false unchanged-master claim')
    attempted = False
    for name, img in record['images'].items():
        require(set(img) == {'reference', 'expected_digest', 'observed_digest', 'expected_config_digest', 'observed_config_digest', 'state', 'push_attempted'}, 'unexpected image record')
        require(img['reference'] == 'ghcr.io/phreddrick/' + name + ':' + profile['source'], 'unauthorized tag in promotion record')
        require(img['expected_digest'] == profile['images'][name]['manifest_digest'], 'incorrect recorded image digest')
        require(img['state'] in ['not_attempted', 'attempted_unknown', 'verified'] and type(img['push_attempted']) is bool, 'invalid image outcome')
        require(img['state'] != 'attempted_unknown' or img['push_attempted'], 'contradictory uncertain upload')
        require(img['observed_digest'] is None or DIGEST.fullmatch(img['observed_digest']), 'invalid observed digest')
        require(img['state'] != 'verified' or img['observed_digest'] == img['expected_digest'], 'unverified registry digest')
        require(img['expected_config_digest'] == profile['images'][name]['config_digest'], 'incorrect recorded config digest')
        require(img['observed_config_digest'] is None or DIGEST.fullmatch(img['observed_config_digest']), 'invalid observed config digest')
        require(img['state'] != 'verified' or img['observed_config_digest'] == img['expected_config_digest'], 'unverified registry config')
        attempted |= img['push_attempted']
    complete = record['status'] == 'complete'
    require(type(record['complete']) is bool and record['complete'] == complete, 'false complete flag')
    require(record['status'] in ['complete', 'partial', 'failed'], 'unknown promotion status')
    if complete:
        require(unchanged and all(i['state'] == 'verified' for i in record['images'].values()) and record['failure'] is None, 'incomplete promotion reported as complete')
    else:
        require(record['status'] == ('partial' if attempted else 'failed') and isinstance(record['failure'], str), 'false aborted/partial outcome')


def finalise(path, record, profile=PROFILE):
    validate_record(record, profile)
    raw = (json.dumps(record, indent=2) + '\n').encode()
    require(not os.path.lexists(path), 'existing record must never be overwritten')
    fd, tmp = tempfile.mkstemp(prefix='.promotion-record-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as f:
            f.write(raw)
            f.flush()
            os.fsync(f.fileno())
        # link is atomic NO-REPLACE, including if a directory/symlink appears after preflight.
        os.link(tmp, path)
        require(path.is_file() and not path.is_symlink() and path.read_bytes() == raw, 'promotion record readback failed')
        validate_record(artifacts.decode(path.read_bytes()), profile)
    finally:
        os.unlink(tmp)


def promote(images, layouts, scan, proof, record_path, control, registry, profile=PROFILE):
    require(not os.path.lexists(record_path), 'existing promotion record')
    artifacts.provenance(proof['run'], proof['jobs'], profile)
    artifacts.transport_proof(proof['transport'], profile)
    record = {'kind': 'gb-pr78-artifact-promotion-v1', 'status': 'failed', 'complete': False, 'rebuilt': False,
              'recorded_at': dt.datetime.now(dt.timezone.utc).isoformat(),
              'source': profile['source'], 'original_ci': {'run_id': profile['run_id'], 'attempt': 1}, 'control': control,
              'transport': proof['transport'], 'master_tags_before': {}, 'master_tags_after': {}, 'master_tags_unchanged': False,
              'scan': scan, 'sbom_zip_sha256': profile['artifacts']['sboms']['sha256'], 'images': {}, 'failure': 'not started'}
    identities = {}
    for name, expected in profile['images'].items():
        identity = oci_digest.inspect(str(images / (name + '.oci.tar')))
        require(identity['manifest_digest'] == expected['manifest_digest'] and identity['config_digest'] == expected['config_digest'], 'wrong promotion image')
        identities[name] = identity
        record['images'][name] = {'reference': 'ghcr.io/phreddrick/' + name + ':' + profile['source'],
                                 'expected_digest': expected['manifest_digest'], 'observed_digest': None,
                                 'expected_config_digest': expected['config_digest'], 'observed_config_digest': None, 'state': 'not_attempted', 'push_attempted': False}
    # Validate record structure BEFORE any registry operation.
    validate_record(record, profile)
    journal = record_path.with_suffix('.journal.jsonl')
    require(not os.path.lexists(journal), 'existing promotion journal')

    def evidence(phase='in_progress', accepted_record=False):
        if not record['complete']:
            record['status'] = 'partial' if any(i['push_attempted'] for i in record['images'].values()) else 'failed'
        with open(journal, 'a', encoding='utf-8') as f:
            os.chmod(journal, 0o600)
            checkpoint = {'kind': 'gb-pr78-promotion-journal-v1', 'phase': phase,
                          'accepted_record': accepted_record, 'registry_state': record}
            f.write(json.dumps(checkpoint) + '\n')
            f.flush()
            os.fsync(f.fileno())

    try:
        for name in profile['images']:
            record['master_tags_before'][name] = registry.digest('ghcr.io/phreddrick/' + name + ':master')
        # Check BOTH destinations before writing either image.
        for img in record['images'].values():
            existing = registry.digest(img['reference'], allow_missing=True)
            require(existing in [None, img['expected_digest']], 'conflicting existing source tag')
        evidence()
        with tempfile.TemporaryDirectory(prefix='pr78-remote-') as remote:
            for name, img in record['images'].items():
                # Recheck; registry has no atomic tag compare-and-swap. Exclusive writer window required.
                existing = registry.digest(img['reference'], allow_missing=True)
                require(existing in [None, img['expected_digest']], 'source tag changed before upload')
                if existing is None:
                    img['state'], img['push_attempted'] = 'attempted_unknown', True
                    evidence()  # Must survive a nonzero/timeout AFTER a successful remote write.
                    registry.push(layouts / name, img['reference'])
                img['observed_digest'] = registry.digest(img['reference'])
                require(img['observed_digest'] == img['expected_digest'], 'uploaded tag digest mismatch')
                img['observed_config_digest'] = registry.verify('ghcr.io/phreddrick/' + name + '@' + img['expected_digest'], Path(remote) / name, identities[name])
                img['state'] = 'verified'
                evidence()
        record['failure'] = None
    except Exception as e:
        record['failure'] = str(e) if isinstance(e, Refused) else type(e).__name__  # no exception body, signed URLs or credentials
    finally:
        try:
            for name in profile['images']:
                record['master_tags_after'][name] = registry.digest('ghcr.io/phreddrick/' + name + ':master')
        except Exception:
            record['failure'] = 'master tag verification unavailable'
        record['master_tags_unchanged'] = (len(record['master_tags_before']) == 2
                                         and record['master_tags_before'] == record['master_tags_after'])
        if not record['master_tags_unchanged']:
            record['failure'] = 'master tag changed or unavailable'
        record['complete'] = record['failure'] is None and all(i['state'] == 'verified' for i in record['images'].values())
        record['status'] = 'complete' if record['complete'] else 'partial' if any(i['push_attempted'] for i in record['images'].values()) else 'failed'
        evidence('finalisation_pending')
        try:
            finalise(record_path, record, profile)
        except BaseException:
            evidence('record_finalisation_failed')
            raise
        evidence('record_finalised', accepted_record=True)
    require(record['complete'], 'promotion incomplete; inspect record and journal, do not retry automatically')
    return record


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest='operation', required=True)
    p = sub.add_parser('promote')
    for name in ['input', 'images', 'layout', 'scans', 'record', 'scan_start']:
        p.add_argument('--' + name.replace('_', '-'), type=Path, required=True)
    p = sub.add_parser('verify-record')
    p.add_argument('record', type=Path)
    p = sub.add_parser('db-evidence')
    p.add_argument('--cache', type=Path, required=True)
    p.add_argument('--out', type=Path, required=True)
    args = ap.parse_args()
    try:
        if args.operation == 'verify-record':
            validate_record(artifacts.decode(args.record.read_bytes()))
        elif args.operation == 'db-evidence':
            artifacts.context()
            database_evidence(args.cache, args.out)
        else:
            artifacts.context()
            with tempfile.TemporaryDirectory(prefix='pr78-revalidate-') as work:
                proof, identities = artifacts.verify(args.input, Path(work) / 'verified')
                for name in PROFILE['images']:
                    require(oci_digest.inspect(str(args.images / (name + '.oci.tar'))) == identities[name] |
                            {'archive': str(args.images / (name + '.oci.tar'))}, 'scanned archive differs from candidate')
            scan = scan_gate(args.images, args.layout, args.scans, args.scan_start.read_text().strip())
            control = {'commit': os.environ['GITHUB_SHA'], 'run_id': int(os.environ['GITHUB_RUN_ID']), 'attempt': int(os.environ['GITHUB_RUN_ATTEMPT'])}
            promote(args.images, args.layout, scan, proof, args.record, control, Registry())
        print('PR78 PROMOTION COMPLETE; master tags unchanged; nothing deployed' if args.operation == 'promote' else 'PR78 evidence valid')
    except (Exception, KeyboardInterrupt) as e:
        # Journal/record contains states. Never turn uncertain upload into rollback or leak stderr.
        print('PR78 promotion refused/incomplete (' + type(e).__name__ + '); inspect retained evidence')
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())

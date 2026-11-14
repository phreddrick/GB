"""PR78 promotion regressions. Synthetic archives/API/registry only; no network or Docker."""
from __future__ import annotations
import copy
import datetime as dt
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock
import urllib.error
import urllib.request
import zipfile

import yaml
import oci_digest
import oci_layout_dir
import pr78_artifacts as a
import pr78_promotion as p
import publish_record
from test_scripts import _layer_tar, trivy_report

SOURCE = a.PROFILE['source']
REPO = Path(__file__).resolve().parents[2]
NOW = dt.datetime.now(dt.timezone.utc)


def packed(files):
    b = io.BytesIO()
    with tarfile.open(fileobj=b, mode='w') as t:
        for name, data in files.items():
            m = tarfile.TarInfo(name); m.size = len(data); t.addfile(m, io.BytesIO(data))
    return b.getvalue()


class Fixture:
    def __init__(self, root):
        self.root = root; self.input = root / 'input'; self.input.mkdir()
        self.profile = copy.deepcopy(a.PROFILE)
        self.files = {n: {} for n in self.profile['artifacts']}
        self.run = {'id': self.profile['run_id'], 'run_attempt': 1, 'repository': {'full_name': 'phreddrick/GB'},
                    'head_repository': {'full_name': 'phreddrick/GB'}, 'head_sha': SOURCE, 'head_branch': 'master',
                    'path': '.github/workflows/ci.yml', 'event': 'push', 'status': 'completed', 'conclusion': 'success'}
        self.jobs = {'total_count': 7, 'jobs': [{'id': i + 1, 'run_id': self.run['id'], 'run_attempt': 1, 'head_sha': SOURCE,
                     'name': n, 'status': 'completed', 'conclusion': 'success'} for i, n in enumerate(self.profile['required_jobs'])]}
        record = {'kind': 'gb-image-build-record', 'source_commit': SOURCE, 'workflow_run': 'https://github.com/phreddrick/GB/actions/runs/43451208400/attempts/1', 'platform': 'linux/amd64', 'go_toolchain': '1.27.4', 'images': {}}
        for name in self.profile['images']:
            layer = gzip.compress(_layer_tar('usr/local/bin/' + name, b'not-executed-' + name.encode()), mtime=0)
            config = json.dumps({'os': 'linux', 'architecture': 'amd64', 'rootfs': {'type': 'layers', 'diff_ids': ['sha256:' + a.sha(gzip.decompress(layer))]},
                'config': {'User': '65532:65532', 'Entrypoint': ['/usr/local/bin/' + name], 'Labels': {
                    'org.opencontainers.image.source': 'https://github.com/phreddrick/GB', 'org.opencontainers.image.revision': SOURCE, 'org.opencontainers.image.version': SOURCE}}}).encode()
            manifest = json.dumps({'schemaVersion': 2, 'mediaType': oci_digest.IMAGE_MANIFEST,
                'config': {'digest': 'sha256:' + a.sha(config), 'size': len(config)},
                'layers': [{'mediaType': oci_digest.GZIP_LAYER if hasattr(oci_digest, 'GZIP_LAYER') else 'application/vnd.oci.image.layer.v1.tar+gzip', 'digest': 'sha256:' + a.sha(layer), 'size': len(layer)}]}).encode()
            md, cd = 'sha256:' + a.sha(manifest), 'sha256:' + a.sha(config)
            blobs = {'blobs/sha256/' + a.sha(manifest): manifest, 'blobs/sha256/' + a.sha(config): config, 'blobs/sha256/' + a.sha(layer): layer}
            files = {'oci-layout': b'{"imageLayoutVersion":"1.0.0"}', 'index.json': json.dumps({'manifests': [{'mediaType': oci_digest.IMAGE_MANIFEST, 'digest': md, 'size': len(manifest)}]}).encode(), **blobs}
            docker = {**files, 'manifest.json': json.dumps([{'Config': 'blobs/sha256/' + cd[7:], 'RepoTags': [name + ':ci'], 'Layers': ['blobs/sha256/' + a.sha(layer)]}]).encode()}
            ot, dtar = packed(files), packed(docker)
            self.files['container-images'][name + '.oci.tar'] = ot; self.files['container-images'][name + '.docker.tar'] = dtar
            self.profile['images'][name] = {'manifest_digest': md, 'config_digest': cd, 'oci_tar_sha256': a.sha(ot), 'docker_tar_sha256': a.sha(dtar)}
            record['images'][name] = {'manifest_digest': md, 'config_digest': cd, 'docker_image_id': md}
            sbom = {'SPDXID': 'SPDXRef-DOCUMENT', 'spdxVersion': 'SPDX-2.3', 'packages': [{'SPDXID': 'image', 'versionInfo': md}], 'relationships': [{'spdxElementId': 'SPDXRef-DOCUMENT', 'relationshipType': 'DESCRIBES', 'relatedSpdxElement': 'image'}]}
            self.files['sboms'][name + '.spdx.json'] = json.dumps(sbom).encode()
        self.files['image-build-record']['image-build-record.json'] = json.dumps(record).encode()
        for n, exp in self.profile['artifacts'].items():
            for entry in exp['entries']: self.files[n].setdefault(entry, b'fixture')
            self.zip(n)
        self.transport = {'kind': 'original-ci', 'release_id': None, 'items': {n: {'id': x['id'], 'sha256': x['sha256']} for n, x in self.profile['artifacts'].items()}}
        self.proof = {'run': self.run, 'jobs': self.jobs, 'transport': self.transport}
        self.save_proof()

    def save_proof(self):
        (self.input / 'provenance.json').write_text(json.dumps(self.proof))

    def zip(self, name, mode=None):
        path = self.input / (name + '.zip')
        with zipfile.ZipFile(path, 'w') as z:
            for fn, data in self.files[name].items():
                m = zipfile.ZipInfo(fn); m.create_system = 3; m.external_attr = (mode or 0o100600) << 16; z.writestr(m, data)
        self.profile['artifacts'][name]['sha256'] = a.sha(path.read_bytes()); self.profile['artifacts'][name]['bytes'] = path.stat().st_size
        if hasattr(self, 'transport'):
            self.transport['items'][name]['sha256'] = self.profile['artifacts'][name]['sha256']; self.save_proof()

    def prepared(self):
        out = self.root / 'verified'; proof, ids = a.verify(self.input, out, self.profile)
        self.images = out / 'container-images'; self.layout = self.root / 'layout'; self.layout.mkdir()
        self.scans = self.root / 'scan'; self.scans.mkdir()
        self.start = (NOW - dt.timedelta(minutes=1)).isoformat()
        (self.scans/'trivy-version.txt').write_text('Version: 0.76.1\n')
        for name, ident in ids.items():
            oci_layout_dir.materialise(str(self.images / (name + '.oci.tar')), str(self.layout / name))
            report = trivy_report()
            report['ArtifactName'] = str(self.layout / name)
            report['Metadata']['ImageID'] = ident['config_digest']
            report['CreatedAt'] = NOW.isoformat(); report['Trivy'] = {'Version': '0.76.1'}
            (self.scans / ('trivy-' + name + '.json')).write_text(json.dumps(report))
            database={'Version':2,'metadata_sha256':'1'*64,'database_sha256':'2'*64,'UpdatedAt':(NOW-dt.timedelta(hours=1)).isoformat(),'DownloadedAt':(NOW-dt.timedelta(minutes=2)).isoformat(),'NextUpdate':(NOW+dt.timedelta(hours=1)).isoformat()}
            (self.scans/('db-'+name+'.json')).write_text(json.dumps(database))
        return proof, p.scan_gate(self.images, self.layout, self.scans, self.start, self.profile)


class FakeAPI:
    def __init__(self, f, retained=False): self.f = f; self.retained = retained; self.calls = []; self.fail = None
    def get(self, endpoint, binary=False, limit=None):
        self.calls.append(endpoint)
        if self.fail and self.fail in endpoint: raise a.Refused('evidence unavailable')
        f = self.f
        if '/attempts/1/jobs' in endpoint: return copy.deepcopy(f.jobs)
        if endpoint == 'actions/runs/43451208400': return copy.deepcopy(f.run)
        if endpoint == 'releases/12': return {'id': 12, 'draft': False, 'author': {'login': 'phreddrick', 'id': 1794145}}
        if '/assets?' in endpoint:
            return [{'id': x['id'], 'name': n + '.zip', 'state': 'uploaded', 'uploader': {'login': 'phreddrick', 'id': 1794145}, 'size': x['bytes'], 'digest': 'sha256:' + x['sha256']} for n, x in f.profile['artifacts'].items()]
        aid = int(endpoint.split('/')[-2] if endpoint.endswith('/zip') else endpoint.split('/')[-1])
        n, x = next((n, x) for n, x in f.profile['artifacts'].items() if x['id'] == aid)
        if binary: return (f.input / (n + '.zip')).read_bytes()
        return {'id': aid, 'name': n, 'digest': 'sha256:' + x['sha256'], 'size_in_bytes': x['bytes'], 'expired': False,
                'expires_at': (NOW + dt.timedelta(days=1)).isoformat(), 'workflow_run': {'id': f.run['id'], 'head_sha': SOURCE, 'head_branch': 'master', 'repository_id': 4607577, 'head_repository_id': 4607577}}


class FakeRegistry:
    def __init__(self, f, mode='ok'):
        self.f = f; self.mode = mode; self.tags = {}; self.calls = []; self.master = 'sha256:' + 'a' * 64
    def digest(self, ref, allow_missing=False):
        self.calls.append(('digest', ref))
        if ref.endswith(':master'): return ('sha256:' + 'b' * 64) if self.mode == 'master-drift' and len(self.tags) == 2 else self.master
        name = 'gbctl' if '/gbctl' in ref else 'gb-server'
        if '@' in ref: return self.f.profile['images'][name]['manifest_digest']
        return self.tags.get(name)
    def push(self, layout, ref):
        self.calls.append(('push', ref)); name = layout.name
        self.tags[name] = self.f.profile['images'][name]['manifest_digest']
        if self.mode == 'first-ambiguous' or (self.mode == 'second-fails' and name == 'gbctl'): raise TimeoutError('write may have committed')
    def verify(self, ref, dest, expected):
        self.calls.append(('verify', ref))
        if self.mode == 'bad-registry': raise a.Refused('registry mismatch')
        assert '@' in ref
        return expected['config_digest']


class PromotionTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup); self.root = Path(self.tmp.name); self.f = Fixture(self.root)
    def rejected(self, fn, reason=None):
        with self.assertRaisesRegex((a.Refused, oci_digest.ArchiveError, ci_error()), reason or '.'):
            fn()
    def test_fixed_profile_owner_release_identity(self):
        self.assertEqual(SOURCE, '7a17a1bc7edf75091386f30944a529a0ccf48d8d')
        self.assertEqual(a.PROFILE['artifacts']['container-images']['sha256'], '09cc296ced40ead00aaabc4044409d1fee36db1faa51bf2134523efb3a0ddd1c')
        self.assertEqual(a.PROFILE['images']['gb-server']['manifest_digest'], 'sha256:66b72b138dbeff1b79044f44548adef3dddc476e35b1e3d0deb9ee9db73da96a')
    def test_complete_bundle_and_original_ci(self):
        proof, ids = a.verify(self.f.input, self.root / 'out', self.f.profile); self.assertEqual(set(ids), {'gb-server', 'gbctl'}); self.assertEqual(proof, self.f.proof)
    def test_wrong_source_run_attempt_event_repo_and_missing_ci(self):
        for key, value in [('head_sha', 'f'*40), ('id', 7), ('run_attempt', 2), ('event', 'pull_request'), ('head_branch', 'feature'), ('conclusion', 'failure'), ('repository', {'full_name': 'attacker/fork'})]:
            with self.subTest(key=key):
                r = copy.deepcopy(self.f.run); r[key] = value; self.rejected(lambda: a.provenance(r, self.f.jobs, self.f.profile))
        for mutation in ['missing', 'failed', 'mixed', 'duplicate']:
            with self.subTest(mutation=mutation):
                j = copy.deepcopy(self.f.jobs)
                if mutation == 'missing': j['jobs'].pop(); j['total_count'] -= 1
                elif mutation == 'failed': j['jobs'][0]['conclusion'] = 'failure'
                elif mutation == 'mixed': j['jobs'][0]['run_attempt'] = 2
                else: j['jobs'][0]['name'] = j['jobs'][1]['name']
                self.rejected(lambda: a.provenance(self.f.run, j, self.f.profile))
    def test_original_and_explicit_retained_transport(self):
        for retained in ['', '12']:
            with self.subTest(retained=retained):
                root = self.root / ('stage' + retained); api = FakeAPI(self.f); a.stage(root, api, retained, self.f.profile)
                a.verify(root, self.root / ('verify' + retained), self.f.profile)
                self.assertEqual(any('actions/artifacts/' in x for x in api.calls), not bool(retained))
    def test_expired_and_unavailable_artifacts_fail_without_fallback(self):
        d = FakeAPI(self.f).get('actions/artifacts/14049614280')
        for key, value in [('expired', True), ('expires_at', '2000-01-01T00:00:00Z'), ('digest', 'sha256:'+'f'*64), ('size_in_bytes', 1)]:
            with self.subTest(key=key):
                changed = {**d, key:value}; self.rejected(lambda: a.artifact_metadata(changed, 'container-images', self.f.profile))
        api = FakeAPI(self.f); api.fail = 'actions/artifacts'; self.rejected(lambda: a.stage(self.root/'failed', api, '', self.f.profile)); self.assertFalse(any('releases/' in x for x in api.calls))
    def test_retained_assets_still_require_ci_and_owner(self):
        api = FakeAPI(self.f); api.fail = 'actions/runs'; self.rejected(lambda: a.stage(self.root/'noci',api,'12',self.f.profile))
        for field in ['draft', 'author', 'duplicate', 'uploader', 'digest', 'missing']:
            with self.subTest(field=field):
                api=FakeAPI(self.f); orig=api.get
                def altered(endpoint, **kwargs):
                    d=orig(endpoint,**kwargs)
                    if endpoint=='releases/12':
                        if field=='draft': d['draft']=True
                        if field=='author': d['author']['id']=1
                    elif '/assets?' in endpoint:
                        if field=='duplicate': d.append(d[0])
                        if field=='missing': d.pop()
                        if field=='uploader': d[0]['uploader']['id']=1
                        if field=='digest': d[0]['digest']='sha256:'+'f'*64
                    return d
                api.get=altered; self.rejected(lambda:a.stage(self.root/field,api,'12',self.f.profile))
    def test_tampered_zip_and_handoff_proof(self):
        path=self.f.input/'container-images.zip'; path.write_bytes(path.read_bytes()+b'bad'); self.rejected(lambda:a.verify(self.f.input,self.root/'bad',self.f.profile),'checksum')
    def test_unsafe_zip_paths_and_links(self):
        for kind in ['path','link','duplicate']:
            with self.subTest(kind=kind):
                f=self.f
                n='trivy-reports'; original=copy.deepcopy(f.files[n]); exp=copy.deepcopy(f.profile['artifacts'][n])
                if kind=='path': f.files[n]['../escape']=b'x'
                f.zip(n, 0o120777 if kind=='link' else None)
                if kind=='duplicate':
                    with self.assertWarnsRegex(UserWarning, 'Duplicate name'):
                        with zipfile.ZipFile(f.input/(n+'.zip'),'a') as z:z.writestr(next(iter(f.files[n])),b'duplicate')
                    f.profile['artifacts'][n]['sha256']=a.sha((f.input/(n+'.zip')).read_bytes()); f.profile['artifacts'][n]['bytes']=(f.input/(n+'.zip')).stat().st_size; f.transport['items'][n]['sha256']=f.profile['artifacts'][n]['sha256'];f.save_proof()
                self.rejected(lambda:a.verify(f.input,self.root/('bad-'+kind),f.profile))
                f.files[n]=original;f.profile['artifacts'][n]=exp;f.zip(n)
    def test_wrong_manifest_and_corrupt_blob(self):
        self.f.profile['images']['gb-server']['manifest_digest']='sha256:'+'f'*64
        self.rejected(lambda:a.verify(self.f.input,self.root/'bad',self.f.profile),'digest')
    def test_invalid_transport_proof_and_duplicate_json(self):
        bad=copy.deepcopy(self.f.transport);bad['items'].pop('sboms');self.rejected(lambda:a.transport_proof(bad,self.f.profile))
        self.rejected(lambda:a.decode('{"a":1,"a":2}'),'duplicate')
    def test_redirect_never_forwards_credential(self):
        req=urllib.request.Request('https://api.github.com/x',headers={'Authorization':'Bearer disposable-test-value'})
        r=a.Redirects().redirect_request(req,None,302,'',{},'https://release-assets.githubusercontent.com/file')
        self.assertIsNone(r.get_header('Authorization'));self.assertNotIn('Authorization',r.unredirected_hdrs)
        for target in ['http://github.com/file','https://evil.example/file','https://github.com:444/file','https://user:pass@github.com/file']:
            self.rejected(lambda:a.Redirects().redirect_request(req,None,302,'',{},target))
    def test_api_errors_redacted_and_download_bounded(self):
        api=a.API()
        with mock.patch.dict(os.environ,{'GH_TOKEN':'disposable-test-value'}), mock.patch.object(api.opener,'open',side_effect=urllib.error.URLError('SECRET_SIGNED_URL')):
            with self.assertRaises(a.Refused) as e:api.get('actions/runs/1')
            self.assertNotIn('SECRET',str(e.exception))
        response=mock.MagicMock();response.__enter__.return_value=response;response.geturl.return_value='https://api.github.com/file';response.read.return_value=b'1234'
        with mock.patch.dict(os.environ,{'GH_TOKEN':'disposable-test-value'}),mock.patch.object(api.opener,'open',return_value=response):self.rejected(lambda:api.get('actions/artifacts/1/zip',True,3),'bound')
    def test_context_refuses_unauthorized_selection(self):
        env={'GITHUB_REPOSITORY_ID':'4607577','GITHUB_REPOSITORY_OWNER_ID':'1794145','GITHUB_REPOSITORY':'phreddrick/GB','GITHUB_REF':'refs/heads/master','GITHUB_EVENT_NAME':'workflow_dispatch','CONFIRM':'promote-pr78-exclusive','OPEN_DIGEST_PR':'false','GITHUB_SHA':'e'*40,'GITHUB_RUN_ID':'91','GITHUB_RUN_ATTEMPT':'1','GITHUB_WORKFLOW_REF':'phreddrick/GB/.github/workflows/publish-images.yml@refs/heads/master'}
        with mock.patch.dict(os.environ,env,clear=True):a.context()
        for k,v in [('CONFIRM','publish'),('OPEN_DIGEST_PR','true'),('GITHUB_REF','refs/heads/feature'),('GITHUB_REPOSITORY','attacker/GB'),('GITHUB_REPOSITORY_ID','1'),('GITHUB_REPOSITORY_OWNER_ID','1'),('GITHUB_EVENT_NAME','pull_request'),('GITHUB_WORKFLOW_REF','other')]:
            with self.subTest(k=k),mock.patch.dict(os.environ,{**env,k:v},clear=True):self.rejected(a.context)
    def test_fresh_scan_gate_rejects_stale_wrong_image_and_policy_failure(self):
        self.f.prepared(); path=self.f.scans/'trivy-gb-server.json'; original=json.loads(path.read_text())
        for mode in ['stale','wrong-image','secret','critical','wrong-version','empty']:
            with self.subTest(mode=mode):
                d=copy.deepcopy(original)
                if mode=='stale':d['CreatedAt']='2000-01-01T00:00:00Z'
                if mode=='wrong-image':d['Metadata']['ImageID']='sha256:'+'f'*64
                if mode=='wrong-version':d['Trivy']['Version']='0.1'
                if mode=='empty':d={}
                if mode=='secret':d['Results'][0]['Secrets']=[{'RuleID':'x','Title':'x','Severity':'HIGH','Match':'not-a-real-secret'}]
                if mode=='critical':d['Results'][0]['Vulnerabilities']=[{'VulnerabilityID':'CVE-TEST','PkgName':'x','InstalledVersion':'1','FixedVersion':'2','Severity':'CRITICAL'}]
                path.write_text(json.dumps(d))
                with self.assertRaises(Exception):p.scan_gate(self.f.images,self.f.layout,self.f.scans,self.f.start,self.f.profile)
    def test_database_evidence_requires_real_regular_files_and_nonexpired_metadata(self):
        cache=self.root/'db';cache.mkdir();out=self.root/'db-evidence.json'
        metadata={'Version':2,'UpdatedAt':(NOW-dt.timedelta(hours=1)).isoformat(),'DownloadedAt':(NOW-dt.timedelta(minutes=2)).isoformat(),'NextUpdate':(NOW+dt.timedelta(hours=1)).isoformat()}
        (cache/'metadata.json').write_text(json.dumps(metadata));(cache/'trivy.db').write_bytes(b'synthetic database bytes')
        p.database_evidence(cache,out);data=json.loads(out.read_text());p.validate_database(data,NOW)
        self.assertEqual(data['database_sha256'],a.sha(b'synthetic database bytes'))
        data['NextUpdate']=(NOW-dt.timedelta(minutes=1)).isoformat();self.rejected(lambda:p.validate_database(data,NOW),'overdue')
        (cache/'trivy.db').unlink();(cache/'trivy.db').symlink_to(cache/'metadata.json');self.rejected(lambda:p.database_evidence(cache,self.root/'bad-db'),'link')

    def run_promotion(self, mode='ok', existing=False):
        proof, scan=self.f.prepared(); registry=FakeRegistry(self.f,mode)
        if existing:registry.tags={n:x['manifest_digest'] for n,x in self.f.profile['images'].items()}
        path=self.root/'record.json';control={'commit':'e'*40,'run_id':91,'attempt':1}
        try:p.promote(self.f.images,self.f.layout,scan,proof,path,control,registry,self.f.profile)
        except a.Refused:pass
        return registry,json.loads(path.read_text())
    def test_complete_promotion_and_no_master_write(self):
        registry,record=self.run_promotion();self.assertEqual(record['status'],'complete');self.assertTrue(record['master_tags_unchanged']);self.assertFalse(record['rebuilt']);self.assertEqual(len([x for x in registry.calls if x[0]=='push']),2)
        self.assertTrue(all(not ref.endswith(':master') for cmd,ref in registry.calls if cmd=='push'));p.validate_record(record,self.f.profile)
        self.assertTrue(publish_record.validate(record, expect_status=None, expect_commit=None, expect_namespace=None, images_dir=None))  # old contract MUST reject promotion records
    def test_existing_identical_images_are_verified_without_writes(self):
        registry,record=self.run_promotion(existing=True);self.assertTrue(record['complete']);self.assertFalse(any(c[0]=='push' for c in registry.calls))
    def test_unknown_first_write_is_partial_not_rollback(self):
        registry,record=self.run_promotion('first-ambiguous');self.assertEqual(record['status'],'partial');self.assertTrue(record['images']['gb-server']['push_attempted']);self.assertEqual(record['images']['gb-server']['state'],'attempted_unknown');self.assertEqual(len([c for c in registry.calls if c[0]=='push']),1)
    def test_second_upload_failure_preserves_verified_first(self):
        _,r=self.run_promotion('second-fails');self.assertEqual(r['status'],'partial');self.assertEqual(r['images']['gb-server']['state'],'verified');self.assertEqual(r['images']['gbctl']['state'],'attempted_unknown')
    def test_invalid_registry_response_and_master_drift_fail(self):
        for mode in ['bad-registry','master-drift']:
            with self.subTest(mode=mode):
                case=self.root/mode;case.mkdir();old=self.f;self.f=Fixture(case)
                _,r=self.run_promotion(mode);self.assertFalse(r['complete']);self.assertEqual(r['status'],'partial')
                (self.root/'record.json').unlink();(self.root/'record.journal.jsonl').unlink();self.f=old
    def test_conflicting_tag_refused_before_any_write(self):
        proof,scan=self.f.prepared();reg=FakeRegistry(self.f);reg.tags['gbctl']='sha256:'+'f'*64
        self.rejected(lambda:p.promote(self.f.images,self.f.layout,scan,proof,self.root/'record',{'commit':'e'*40,'run_id':91,'attempt':1},reg,self.f.profile));self.assertFalse(any(x[0]=='push' for x in reg.calls))
    def test_record_tampering_and_no_overwrite(self):
        _,r=self.run_promotion()
        for mutate in ['source','complete','master','observed','config','scan','db','ref']:
            with self.subTest(mutate=mutate):
                d=copy.deepcopy(r)
                if mutate=='source':d['source']='f'*40
                if mutate=='complete':d['complete']=False
                if mutate=='master':d['master_tags_after']['gbctl']='sha256:'+'f'*64
                if mutate=='observed':d['images']['gbctl']['observed_digest']='sha256:'+'f'*64
                if mutate=='config':d['images']['gbctl']['observed_config_digest']='sha256:'+'f'*64
                if mutate=='scan':d['scan']['reports'].pop('gbctl')
                if mutate=='db':d['scan']['reports']['gbctl']['database']['NextUpdate']='2000-01-01T00:00:00Z'
                if mutate=='ref':d['images']['gbctl']['reference']='ghcr.io/phreddrick/gbctl:master'
                self.rejected(lambda:p.validate_record(d,self.f.profile))
        self.rejected(lambda:p.finalise(self.root/'record.json',r,self.f.profile));self.assertEqual(json.loads((self.root/'record.json').read_text()),r)
    def test_production_registry_rejects_unauthorized_tag_and_invalid_read(self):
        reg=p.Registry()
        with mock.patch.object(reg,'call') as call:
            for ref in ['ghcr.io/phreddrick/gb-server:master','ghcr.io/phreddrick/gbctl:latest','attacker/gbctl:'+SOURCE]:self.rejected(lambda:reg.push(self.root,ref))
            call.assert_not_called()
        for output in [b'',b'{}',b'sha256:wrong']:
            with mock.patch.object(reg,'call',return_value=subprocess.CompletedProcess([],0,output,b'')):self.rejected(lambda:reg.digest('safe-ref'))
        with mock.patch.object(reg,'call',return_value=subprocess.CompletedProcess([],1,b'',b'UNAUTHORIZED MANIFEST_UNKNOWN 401')):self.rejected(lambda:reg.digest('safe-ref',True))
    def test_production_registry_pulls_and_reverifies_immutable_content(self):
        self.f.prepared();reg=p.Registry();ident=oci_digest.inspect(str(self.f.images/'gb-server.oci.tar'))
        def command(args):
            if args[0]=='digest':return subprocess.CompletedProcess([],0,(ident['manifest_digest']+'\n').encode(),b'')
            self.assertEqual(args[0],'pull');self.assertIn('@sha256:',args[1]);self.assertEqual(args[-1],'--format=oci');shutil.copytree(self.f.layout/'gb-server',args[2]);return subprocess.CompletedProcess([],0,b'',b'')
        with mock.patch.object(reg,'call',side_effect=command):reg.verify('repo@'+ident['manifest_digest'],self.root/'retrieved',ident)
        bad={**ident,'user':'0'}
        with mock.patch.object(reg,'call',side_effect=command):self.rejected(lambda:reg.verify('repo@'+ident['manifest_digest'],self.root/'wrong',bad),'content')
    def test_record_finalization_failure_never_reports_complete(self):
        proof,scan=self.f.prepared();reg=FakeRegistry(self.f)
        with mock.patch.object(p,'finalise',side_effect=OSError('disk full')):
            with self.assertRaises(OSError):p.promote(self.f.images,self.f.layout,scan,proof,self.root/'record',{'commit':'e'*40,'run_id':91,'attempt':1},reg,self.f.profile)
        self.assertFalse((self.root/'record').exists());self.assertTrue((self.root/'record.journal.jsonl').exists())
        last=json.loads((self.root/'record.journal.jsonl').read_text().splitlines()[-1])
        self.assertEqual(last['phase'],'record_finalisation_failed');self.assertFalse(last['accepted_record'])
        self.rejected(lambda:p.validate_record(last,self.f.profile),'record fields')
        self.assertTrue(last['registry_state']['complete'])  # registry effects stand; reporting did not complete


def ci_error():
    import ci_provenance
    return ci_provenance.Malformed


class WorkflowBoundaryTests(unittest.TestCase):
    def setUp(self):self.doc=yaml.safe_load((REPO/'.github/workflows/publish-images.yml').read_text())
    def test_one_writer_default_and_boundary(self):
        doc=self.doc;on=doc.get('on',doc.get(True));self.assertEqual(set(on),{'workflow_dispatch'});self.assertEqual(on['workflow_dispatch']['inputs']['operation']['default'],'build-current-master')
        pub=doc['jobs']['publish'];self.assertEqual(pub['permissions'],{'contents':'read','packages':'write'});self.assertEqual(pub['environment'],'ghcr-publish');self.assertEqual(doc['concurrency'],{'group':'ghcr-publish','cancel-in-progress':False});self.assertIn("github.ref == 'refs/heads/master'",pub['if'])
    def test_promotion_skips_every_build_old_writer_and_deployment_path(self):
        pub=self.doc['jobs']['publish'];steps=pub['steps']
        legacy=[s for s in steps if 'docker/build-push-action@' in s.get('uses','') or 'publish-push.sh' in s.get('run','') or s.get('name','').startswith('SBOMs of the published')]
        self.assertGreaterEqual(len(legacy),4)
        for s in legacy:self.assertEqual(s.get('if'),"inputs.operation != 'promote-pr78'")
        promotion=next(s for s in steps if 'pr78_promotion.py promote' in s.get('run',''));self.assertEqual(promotion['if'],"inputs.operation == 'promote-pr78'")
        self.assertIn("inputs.operation != 'promote-pr78'",self.doc['jobs']['digest-pr']['if'])
        handoff=next(s for s in steps if s.get('name')=="Download only this run's admitted candidate");self.assertNotIn('run-id',handoff['with']);self.assertNotIn('github-token',handoff['with']);self.assertIn('github.run_attempt',handoff['with']['name'])
    def test_mode_selection_no_ambiguous_or_automatic_deployment(self):
        step=next(s for s in self.doc['jobs']['preflight']['steps'] if s.get('name')=='Validate operation selection')
        for mode,confirm,retained,dpr,code in [('build-current-master','publish','','false',0),('build-current-master','publish','12','false',1),('promote-pr78','promote-pr78-exclusive','12','false',0),('promote-pr78','publish','','false',1),('promote-pr78','promote-pr78-exclusive','','true',1),('invalid','publish','','false',1)]:
            with self.subTest(mode=mode,confirm=confirm,dpr=dpr):
                r=subprocess.run(['bash','-e','-c',step['run']],env={**os.environ,'OPERATION':mode,'CONFIRM':confirm,'RETAINED_RELEASE_ID':retained,'OPEN_DIGEST_PR':dpr});self.assertEqual(r.returncode,code)


if __name__=='__main__':unittest.main()

"""No live credentials or network; verify observable output and unchanged transport."""
import contextlib
import copy
import http.client
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.error
import urllib.request

import yaml

import pr78_artifacts as production
import pr78_http_diagnostic as diagnostic
import workflow_policy_check as policy


class Response(io.BytesIO):
    status = 200
    headers = {'X-RateLimit-Remaining': '4999'}

    def geturl(self):
        return 'https://api.github.com/fixed'


class DiagnosticTests(unittest.TestCase):
    def probe(self, error=None, data=b'{}', *, binary=False, limit=100):
        d = diagnostic.Diagnostic()
        response = Response(data)
        output = io.StringIO()
        with contextlib.redirect_stdout(output), mock.patch.dict(os.environ, {'GH_TOKEN': 'disposable-test-token'}), \
                mock.patch.object(d.opener.opener, 'open', side_effect=error, return_value=response) as request:
            result = d.probe('container-images-download' if binary else 'ci-run',
                             'actions/artifacts/14049614280/zip' if binary else 'actions/runs/43451208400',
                             binary=binary, limit=limit)
        return d, result, json.loads(output.getvalue()), request

    def test_status_classification_and_no_exception_disclosure(self):
        for status, category in [(401, 'authorization_or_access_restriction'),
                                 (403, 'authorization_or_access_restriction'),
                                 (404, 'unavailable'), (410, 'unavailable'),
                                 (415, 'content_negotiation'), (429, 'rate_limited'),
                                 (500, 'service_failure'), (502, 'service_failure'),
                                 (503, 'service_failure'), (504, 'service_failure'),
                                 (302, 'redirect_failure'), (418, 'http_failure')]:
            with self.subTest(status=status):
                error = urllib.error.HTTPError('https://storage.example/file?SECRET', status,
                    'SECRET', {'Authorization': 'SECRET', 'Location': 'SECRET',
                               'X-RateLimit-Remaining': '4999'}, io.BytesIO(b'SECRET'))
                d, result, record, _ = self.probe(error)
                self.assertIsNone(result)
                self.assertTrue(d.failed)
                self.assertEqual(record['http_status'], status)
                self.assertEqual(record['failure_category'], category)
                self.assertFalse(record['succeeded'])
                self.assertNotIn('SECRET', json.dumps(record))
                self.assertEqual(set(record), {'operation','http_status','failure_category','succeeded','rate_limit'})

    def test_rate_limit_fields_are_numeric_allowlist_only(self):
        headers = {'X-RateLimit-Limit':'5000', 'X-RateLimit-Remaining':'0',
                   'X-RateLimit-Used':'5000', 'X-RateLimit-Reset':'1794623000',
                   'Retry-After':'20', 'Authorization':'SECRET', 'Location':'SECRET',
                   'X-RateLimit-Resource':'SECRET'}
        error = urllib.error.HTTPError('https://api.github.com/x',403,'SECRET',headers,None)
        _,_,record,_ = self.probe(error)
        self.assertEqual(record['failure_category'],'rate_limited')
        self.assertEqual(record['rate_limit'],dict(limit=5000,remaining=0,used=5000,reset=1794623000,retry_after=20))
        for value in ['SECRET','1\nSECRET','-1','1'*21]:
            self.assertEqual(diagnostic.rates({'Retry-After':value}),{})

    def test_same_method_headers_timeout_and_bounds_as_production(self):
        for binary in [False,True]:
            d, result, record, opened = self.probe(binary=binary)
            self.assertFalse(d.failed)
            self.assertTrue(record['succeeded'])
            self.assertEqual(record['http_status'],200)
            self.assertEqual(result,b'{}' if binary else {})
            args, kwargs = opened.call_args
            request = args[0]
            self.assertEqual(request.get_method(),'GET')
            self.assertEqual(request.get_header('Accept'),'application/vnd.github+json')
            self.assertEqual(request.get_header('Authorization'),'Bearer disposable-test-token')
            self.assertEqual(request.get_header('X-github-api-version'),'2022-11-28')
            self.assertEqual(kwargs, {'timeout':60})
        _,_,record,_ = self.probe(data=b'1234',binary=True,limit=3)
        self.assertFalse(record['succeeded'])
        self.assertEqual(record['failure_category'],'response_validation_failed')

    def test_network_interruptions_and_malformed_body_are_redacted(self):
        for error in [urllib.error.URLError('SECRET'),TimeoutError('SECRET'),http.client.IncompleteRead(b'SECRET')]:
            with self.subTest(error=type(error).__name__):
                _,_,record,_=self.probe(error)
                self.assertEqual(record['failure_category'],'network_interruption')
                self.assertNotIn('SECRET',json.dumps(record))
        _,_,record,_=self.probe(data=b'SECRET')
        self.assertEqual(record['failure_category'],'response_validation_failed')
        self.assertNotIn('SECRET',json.dumps(record))

    def test_interrupted_response_read_keeps_status_without_body_or_exception(self):
        for error in [TimeoutError('SECRET'),http.client.IncompleteRead(b'SECRET')]:
            d=diagnostic.Diagnostic()
            response=Response(b'SECRET')
            output=io.StringIO()
            with contextlib.redirect_stdout(output),mock.patch.dict(os.environ,{'GH_TOKEN':'disposable-test-token'}), \
                    mock.patch.object(d.opener.opener,'open',return_value=response), \
                    mock.patch.object(response,'read',side_effect=error):
                self.assertIsNone(d.probe('ci-run','actions/runs/43451208400'))
            record=json.loads(output.getvalue())
            self.assertEqual(record['http_status'],200)
            self.assertEqual(record['failure_category'],'network_interruption')
            self.assertNotIn('SECRET',output.getvalue())

    def test_redirect_uses_production_validation_and_strips_auth(self):
        d=diagnostic.Diagnostic();d.operation='container-images-download'
        handler=diagnostic.Redirects(d)
        self.assertEqual(handler.max_redirections,production.Redirects.max_redirections)
        request=urllib.request.Request('https://api.github.com/fixed',headers={'Authorization':'Bearer disposable-test-token'})
        output=io.StringIO()
        with contextlib.redirect_stdout(output):
            redirected=handler.redirect_request(request,None,302,'SECRET',{'Location':'SECRET'},
                'https://release-assets.githubusercontent.com/file?SECRET')
        self.assertIsNone(redirected.get_header('Authorization'))
        record=json.loads(output.getvalue())
        self.assertEqual(record['operation'],'container-images-download-redirect')
        self.assertEqual(record['http_status'],302)
        self.assertNotIn('SECRET',output.getvalue())
        for target in ['http://github.com/SECRET','https://evil.example/SECRET',
                       'https://github.com:444/SECRET','https://user:SECRET@github.com/file']:
            output=io.StringIO()
            with contextlib.redirect_stdout(output),self.assertRaises(production.Refused):
                handler.redirect_request(request,None,302,'SECRET',{},target)
            self.assertEqual(json.loads(output.getvalue())['failure_category'],'redirect_refused')
            self.assertNotIn('SECRET',output.getvalue())

    def test_context_rejects_forks_other_events_and_profile_changes(self):
        with tempfile.TemporaryDirectory() as root:
            event=Path(root)/'event.json'
            repository={'id':4607577,'full_name':'phreddrick/GB'}
            event.write_text(json.dumps({'pull_request':{'head':{'repo':repository},'base':{'repo':repository}}}))
            env={'GITHUB_REPOSITORY':'phreddrick/GB','GITHUB_REPOSITORY_ID':'4607577',
                 'GITHUB_REPOSITORY_OWNER_ID':'1794145','GITHUB_EVENT_NAME':'pull_request',
                 'GITHUB_EVENT_PATH':str(event),'GH_TOKEN':'disposable-test-token'}
            with mock.patch.dict(os.environ,env,clear=True):
                diagnostic.context()
                changed=copy.deepcopy(production.PROFILE);changed['source']='0'*40
                with mock.patch.object(production,'PROFILE',changed),self.assertRaises(production.Refused):
                    diagnostic.context()
                for key,value in [('repository','other/repo'),('repository_id',1),('attempt',2)]:
                    changed=copy.deepcopy(production.PROFILE);changed[key]=value
                    with mock.patch.object(production,'PROFILE',changed),self.assertRaises(production.Refused):
                        diagnostic.context()
                changed=copy.deepcopy(production.PROFILE);changed['artifacts']['sboms']['id']=1
                with mock.patch.object(production,'PROFILE',changed),self.assertRaises(production.Refused):
                    diagnostic.context()
            for key,value in [('GITHUB_REPOSITORY','other/repo'),('GITHUB_REPOSITORY_ID','1'),
                              ('GITHUB_REPOSITORY_OWNER_ID','1'),('GITHUB_EVENT_NAME','workflow_dispatch'),('GH_TOKEN','')]:
                with self.subTest(key=key),mock.patch.dict(os.environ,{**env,key:value},clear=True),self.assertRaises(production.Refused):
                    diagnostic.context()
            event.write_text(json.dumps({'pull_request':{'head':{'repo':{'id':1,'full_name':'fork/repo'}},'base':{'repo':repository}}}))
            with mock.patch.dict(os.environ,env,clear=True),self.assertRaises(production.Refused):
                diagnostic.context()

    def test_fixed_ten_operations_continue_after_failure_without_retry(self):
        d=mock.Mock();d.failed=True
        d.probe.return_value=None
        with mock.patch.object(diagnostic,'context'),mock.patch.object(diagnostic,'Diagnostic',return_value=d):
            self.assertEqual(diagnostic.run(),1)
        self.assertEqual(d.probe.call_count,10)
        self.assertEqual({c.args[0] for c in d.probe.call_args_list},diagnostic.OPERATIONS-{'context'})
        for call in d.probe.call_args_list:
            self.assertNotIn('releases',call.args[1])
            if call.args[0].endswith('-download'):
                self.assertTrue(call.kwargs['binary'])
                name=call.args[0].removesuffix('-download')
                self.assertEqual(call.kwargs['limit'],production.PROFILE['artifacts'][name]['bytes'])
                with self.assertRaises(production.Refused):call.kwargs['validate'](b'wrong bytes')

    def test_outer_boundary_redacts_unexpected_exception_and_identifier(self):
        output=io.StringIO()
        with contextlib.redirect_stdout(output),mock.patch.object(diagnostic,'run',side_effect=RuntimeError('SECRET')):
            self.assertEqual(diagnostic.main(),1)
        self.assertNotIn('SECRET',output.getvalue())
        with self.assertRaises(ValueError):diagnostic.emit('SECRET',401,'none',False)
        with self.assertRaises(ValueError):diagnostic.emit('ci-run',401,'SECRET',False)


class WorkflowTests(unittest.TestCase):
    def test_exact_read_only_same_repository_pr_boundary(self):
        root=Path(__file__).resolve().parents[1]
        doc=yaml.safe_load((root/'workflows/pr78-artifact-diagnostic.yml').read_text())
        self.assertEqual(set(doc.get('on',doc.get(True))),{'pull_request'})
        self.assertEqual(doc['permissions'],{'contents':'read'})
        self.assertEqual(set(doc['jobs']),{'diagnose'})
        job=doc['jobs']['diagnose']
        self.assertEqual(job['permissions'],{'contents':'read','actions':'read'})
        self.assertIn("github.repository == 'phreddrick/GB'",job['if'])
        self.assertIn("head.repo.full_name == 'phreddrick/GB'",job['if'])
        self.assertEqual(job['steps'][0]['with']['persist-credentials'],False)
        self.assertEqual(job['steps'][1]['env'],{'GH_TOKEN':'${{ secrets.GITHUB_TOKEN }}'})
        self.assertEqual(job['steps'][1]['run'],'python3 .github/scripts/pr78_http_diagnostic.py')
        self.assertEqual(policy.PERMISSION_ALLOWLIST[('pr78-artifact-diagnostic.yml','diagnose')],job['permissions'])
        changed=copy.deepcopy(job['permissions']);changed['packages']='write'
        self.assertNotEqual(changed,policy.PERMISSION_ALLOWLIST[('pr78-artifact-diagnostic.yml','diagnose')])


if __name__=='__main__':
    unittest.main()

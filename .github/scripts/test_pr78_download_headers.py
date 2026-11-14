"""Exercise the real production opener and redirects without network credentials."""
import email.message
import io
import os
import unittest
from unittest import mock
import urllib.error
import urllib.request
import urllib.response

import pr78_artifacts as production


class NoHTTP(urllib.request.HTTPHandler):
    def http_open(self, request):
        raise AssertionError('unexpected HTTP transport; fixture must remain offline')


class GitHubFixture(urllib.request.HTTPSHandler):
    def __init__(self, endpoint, accept, payload, target=None, reject=False):
        super().__init__()
        self.endpoint, self.accept, self.payload = endpoint, accept, payload
        self.target = target or 'https://release-assets.githubusercontent.com/fixed?disposable-signature'
        self.reject = reject
        self.requests = []

    def https_open(self, request):
        self.requests.append(request)
        headers = email.message.Message()
        if len(self.requests) == 1:
            if request.get_header('Accept') != self.accept or self.reject:
                raise urllib.error.HTTPError(request.full_url, 415, 'SECRET', {}, io.BytesIO(b'SECRET'))
            if self.endpoint not in request.full_url:
                raise AssertionError('wrong fixed endpoint')
            if request.get_header('Authorization') != 'Bearer disposable-test-token':
                raise AssertionError('initial request needs its API token')
            headers['Location'] = self.target
            response = urllib.response.addinfourl(io.BytesIO(b''), headers, request.full_url, 302)
            response.msg = 'Found'
            return response
        if request.get_header('Authorization') is not None:
            raise AssertionError('redirect leaked API credentials')
        response = urllib.response.addinfourl(io.BytesIO(self.payload), headers, request.full_url, 200)
        response.msg = 'OK'
        return response


class DownloadHeadersTests(unittest.TestCase):
    def call(self, endpoint, accept, *, payload=b'PK\x03\x04\xff\x00not-JSON', limit=100, target=None, reject=False):
        fixture = GitHubFixture(endpoint, accept, payload, target, reject)
        api = production.API()
        api.opener = urllib.request.build_opener(production.Redirects(), fixture, NoHTTP())
        with mock.patch.dict(os.environ, {'GH_TOKEN': 'disposable-test-token'}):
            result = api.get(endpoint, binary=True, limit=limit)
        return result, fixture

    def test_actions_artifact_json_accept_preserves_raw_zip_after_redirect(self):
        payload=b'PK\x03\x04\xff\x00not-JSON'
        for artifact_id in [14049614280,14050462489,14051100529,14050950671]:
            with self.subTest(artifact_id=artifact_id):
                result,fixture=self.call('actions/artifacts/'+str(artifact_id)+'/zip',
                                         'application/vnd.github+json',payload=payload)
                self.assertEqual(result,payload)
                self.assertEqual(len(fixture.requests),2)
                self.assertTrue(all(r.get_method()=='GET' for r in fixture.requests))
                self.assertEqual(fixture.requests[0].get_header('X-github-api-version'),'2022-11-28')

    def test_retained_release_asset_keeps_octet_stream_and_raw_bytes(self):
        result,fixture=self.call('releases/assets/123','application/octet-stream')
        self.assertIsInstance(result,bytes)
        self.assertEqual(len(fixture.requests),2)
        self.assertEqual(fixture.requests[0].get_header('Accept'),'application/octet-stream')

    def test_release_asset_direct_binary_response_remains_supported(self):
        payload=b'PK\xffnot-JSON'
        response=urllib.response.addinfourl(io.BytesIO(payload),{},'https://api.github.com/fixed',200)
        api=production.API()
        with mock.patch.dict(os.environ,{'GH_TOKEN':'disposable-test-token'}),mock.patch.object(api.opener,'open',return_value=response) as opened:
            self.assertEqual(api.get('releases/assets/123',binary=True),payload)
        self.assertEqual(opened.call_args.args[0].get_header('Accept'),'application/octet-stream')

    def test_http_415_is_redacted_without_retry_or_fallback(self):
        fixture=GitHubFixture('actions/artifacts/14049614280/zip','application/vnd.github+json',b'',reject=True)
        api=production.API();api.opener=urllib.request.build_opener(production.Redirects(),fixture,NoHTTP())
        with mock.patch.dict(os.environ,{'GH_TOKEN':'disposable-test-token'}),self.assertRaises(production.Refused) as error:
            api.get('actions/artifacts/14049614280/zip',binary=True)
        self.assertIn('HTTPError',str(error.exception))
        self.assertNotIn('SECRET',str(error.exception))
        self.assertEqual(len(fixture.requests),1)

    def test_size_bound_and_unsafe_redirects_still_refuse(self):
        for endpoint,accept in [('actions/artifacts/14049614280/zip','application/vnd.github+json'),
                                ('releases/assets/123','application/octet-stream')]:
            with self.subTest(endpoint=endpoint),self.assertRaisesRegex(production.Refused,'bound'):
                self.call(endpoint,accept,payload=b'1234',limit=3)
            for target in ['http://github.com/file','https://evil.example/file',
                           'https://github.com:444/file','https://user:pass@github.com/file']:
                with self.subTest(endpoint=endpoint,target=target),self.assertRaisesRegex(production.Refused,'unsafe'):
                    self.call(endpoint,accept,target=target)


if __name__=='__main__':
    unittest.main()

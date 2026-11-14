#!/usr/bin/env python3
"""Fixed PR78 GET probes; output contains no URLs, bodies or raw exceptions.

This observes the unchanged production API client. It cannot publish, retry,
select another source, or save downloaded archives. Only sanitized records
are printed; unexpected exceptions are redacted at the outermost boundary.
"""
from __future__ import annotations

import json
import http.client
import os
from pathlib import Path
import re
import urllib.error
import urllib.request

import pr78_artifacts as production


ARTIFACTS = {'container-images': 14049614280, 'image-build-record': 14050462489,
             'trivy-reports': 14051100529, 'sboms': 14050950671}
OPERATIONS = {'context', 'ci-run', 'ci-jobs'} | {
    name + suffix for name in ARTIFACTS
    for suffix in ('-metadata', '-download')}
FAILURES = {'none', 'redirect', 'redirect_refused', 'rate_limited',
            'authorization_or_access_restriction', 'unavailable',
            'content_negotiation', 'service_failure', 'redirect_failure',
            'http_failure', 'network_interruption', 'response_validation_failed',
            'diagnostic_refused'}
RATE_HEADERS = {
    'X-RateLimit-Limit': 'limit', 'X-RateLimit-Remaining': 'remaining',
    'X-RateLimit-Used': 'used', 'X-RateLimit-Reset': 'reset',
    'Retry-After': 'retry_after',
}


def rates(headers):
    """Only bounded decimal values; never stringify headers or exceptions."""
    result = {}
    for header, key in RATE_HEADERS.items():
        value = headers.get(header) if headers is not None else None
        if isinstance(value, str) and re.fullmatch(r'[0-9]{1,20}', value):
            result[key] = int(value)
    return result


def category(status, rate):
    if status == 429 or (status == 403 and
                        (rate.get('remaining') == 0 or 'retry_after' in rate)):
        return 'rate_limited'
    if status in (401, 403):
        return 'authorization_or_access_restriction'
    if status in (404, 410):
        return 'unavailable'
    if status == 415:
        return 'content_negotiation'
    if 500 <= status <= 599:
        return 'service_failure'
    if 300 <= status <= 399:
        return 'redirect_failure'
    return 'http_failure'


def emit(operation, status, failure, succeeded, rate=None, redirect=False):
    # All call sites supply fixed identifiers and categories, never remote text.
    if (operation not in OPERATIONS or failure not in FAILURES
            or (status is not None and (type(status) is not int or not 100 <= status <= 599))):
        raise ValueError('unknown diagnostic operation')
    record = {'operation': operation + ('-redirect' if redirect else ''),
              'http_status': status, 'failure_category': failure,
              'succeeded': succeeded, 'rate_limit': rate or {}}
    print(json.dumps(record, sort_keys=True), flush=True)


class Redirects(production.Redirects):
    def __init__(self, diagnostic):
        super().__init__()
        self.diagnostic = diagnostic

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        rate = rates(headers)
        try:
            request = super().redirect_request(req, fp, code, msg, headers, newurl)
        except Exception:
            self.diagnostic.failure = 'redirect_refused'
            emit(self.diagnostic.operation, code, 'redirect_refused', False, rate, True)
            raise
        emit(self.diagnostic.operation, code, 'redirect', True, rate, True)
        return request


class Observer:
    def __init__(self, diagnostic):
        self.diagnostic = diagnostic
        self.opener = urllib.request.build_opener(Redirects(diagnostic))

    def open(self, request, timeout):
        d = self.diagnostic
        try:
            response = self.opener.open(request, timeout=timeout)
        except urllib.error.HTTPError as error:
            d.status, d.rate = error.code, rates(error.headers)
            d.failure = d.failure or category(d.status, d.rate)
            raise
        except (urllib.error.URLError, TimeoutError):
            d.failure = d.failure or 'network_interruption'
            raise
        d.status, d.rate = response.status, rates(response.headers)
        return ObservedResponse(d, response)


class ObservedResponse:
    def __init__(self, diagnostic, response):
        self.diagnostic, self.response = diagnostic, response

    def __enter__(self):
        self.response.__enter__()
        return self

    def __exit__(self, *args):
        return self.response.__exit__(*args)

    def geturl(self):
        return self.response.geturl()

    def read(self, limit):
        try:
            return self.response.read(limit)
        except (OSError, urllib.error.URLError, http.client.HTTPException):
            self.diagnostic.failure = 'network_interruption'
            raise


class Diagnostic(production.API):
    def __init__(self):
        self.operation = 'context'
        self.status = None
        self.rate = {}
        self.failure = None
        self.failed = False
        self.opener = Observer(self)

    def probe(self, operation, endpoint, *, binary=False, limit=4_000_000,
              validate=None):
        self.operation, self.status, self.rate, self.failure = operation, None, {}, None
        try:
            data = self.get(endpoint, binary=binary, limit=limit)
            if validate is not None:
                validate(data)
        except (OSError, http.client.HTTPException):
            self.failed = True
            emit(operation, self.status, self.failure or 'network_interruption', False, self.rate)
            return None
        except Exception:
            # Includes malformed JSON, interrupted reads and bounds/checksum failures.
            self.failed = True
            emit(operation, self.status, self.failure or 'response_validation_failed', False, self.rate)
            return None
        emit(operation, self.status, 'none', True, self.rate)
        return data


def context():
    production.require(os.environ.get('GITHUB_REPOSITORY') == 'phreddrick/GB'
                       and os.environ.get('GITHUB_REPOSITORY_ID') == '4607577'
                       and os.environ.get('GITHUB_REPOSITORY_OWNER_ID') == '1794145'
                       and os.environ.get('GITHUB_EVENT_NAME') == 'pull_request'
                       and bool(os.environ.get('GH_TOKEN')), 'wrong diagnostic context')
    event = production.decode(Path(os.environ['GITHUB_EVENT_PATH']).read_bytes())
    pr = event['pull_request']
    for repo in (pr['head']['repo'], pr['base']['repo']):
        production.require(repo['full_name'] == 'phreddrick/GB' and repo['id'] == 4607577,
                           'same-repository PR required')
    production.require(production.PROFILE['repository'] == 'phreddrick/GB'
                       and production.PROFILE['repository_id'] == 4607577
                       and production.PROFILE['attempt'] == 1
                       and production.PROFILE['source'] == '7a17a1bc7edf75091386f30944a529a0ccf48d8d'
                       and production.PROFILE['run_id'] == 43451208400
                       and {n: a['id'] for n, a in production.PROFILE['artifacts'].items()} == ARTIFACTS,
                       'fixed PR78 identities required')


def run():
    context()
    diagnostic = Diagnostic()
    profile = production.PROFILE
    run_data = diagnostic.probe('ci-run', 'actions/runs/43451208400')
    diagnostic.probe('ci-jobs', 'actions/runs/43451208400/attempts/1/jobs?per_page=100',
                     validate=lambda data: production.provenance(run_data or {}, data))
    for name, expected in profile['artifacts'].items():
        diagnostic.probe(name + '-metadata', 'actions/artifacts/' + str(expected['id']),
                         validate=lambda data, n=name: production.artifact_metadata(data, n))
        def check_zip(data, e=expected):
            production.require(len(data) == e['bytes'] and production.sha(data) == e['sha256'],
                               'wrong fixed artifact bytes')
        diagnostic.probe(name + '-download', 'actions/artifacts/' + str(expected['id']) + '/zip',
                         binary=True, limit=expected['bytes'], validate=check_zip)
    return int(diagnostic.failed)


def main():
    try:
        return run()
    except Exception:
        emit('context', None, 'diagnostic_refused', False)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())

#!/usr/bin/env python3
"""Exercise real appliance HTTP endpoints; secrets stay in memory/private files."""
import http.client
import json
import os
import secrets
import subprocess
import sys
import time

container, address, artifact_dir = sys.argv[1:]
host, port = address.rsplit(':', 1)
token = None

def request(method, path, body=None, expected=200):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    conn = http.client.HTTPConnection(host, int(port), timeout=5)
    conn.request(method, path, None if body is None else json.dumps(body), headers)
    response = conn.getresponse()
    raw = response.read()
    conn.close()
    print(f'{method} {path}: HTTP {response.status}', flush=True)
    if response.status != expected:
        # Error responses are private as they may contain sensitive context.
        with open(os.path.join(artifact_dir, 'api-error.txt'), 'wb') as f:
            f.write(raw)
        raise RuntimeError(f'{method} {path}: expected HTTP {expected}, got {response.status}; see api-error.txt')
    return json.loads(raw) if raw else {}

try:
    for _ in range(45):
        try:
            request('GET', '/api/v1/identity')
            break
        except (OSError, http.client.HTTPException):
            time.sleep(1)
    else:
        raise RuntimeError('GET /api/v1/identity did not become ready')
    claim_file = subprocess.run(['docker', 'exec', container, 'cat', '/run/homestead/claim-token'], check=True, capture_output=True, timeout=10).stdout.decode().splitlines()
    if len(claim_file) != 2 or not claim_file[0]:
        raise RuntimeError('claim token file is malformed')
    claim = claim_file[0]
    request('POST', '/api/v1/claim', {'token': claim})
    password = secrets.token_urlsafe(32)
    owner = request('POST', '/api/v1/admins/setup', {'username': 'owner', 'password': password}, 201)
    token = owner['token']
    request('GET', '/api/v1/me')
    request('POST', '/api/v1/storage/registrations',
            {'mountpoint': '/fixture/storage', 'label': 'disposable'})
    request('POST', '/api/v1/storage/assignments',
            {'class': 'application', 'mountpoint': '/fixture/storage'})
    # Review the actual curated plan, then approve exactly that digest.
    install = {'domain': 'lan', 'storage': {'application': '/fixture/storage'},
               'container_ports': {'server': 9000}}
    plan = request('POST', '/api/v1/catalog/authentik/plan', install)
    install['digest'] = plan['digest']
    operation = request('POST', '/api/v1/catalog/authentik/install', install)
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        op = request('GET', '/api/v1/operations/' + str(operation['id']))
        if op['status'] in ('succeeded', 'failed', 'cancelled'):
            with open(os.path.join(artifact_dir, 'authentik-operation.json'), 'w') as f:
                json.dump(op, f)
            print('Authentik install operation: ' + op['status'], flush=True)
            if op['status'] != 'succeeded':
                raise RuntimeError('Authentik install operation failed; see authentik-operation.json')
            break
        time.sleep(3)
    else:
        raise RuntimeError('Authentik install operation exceeded 180 seconds')
    raise RuntimeError('Authentik readiness, TLS edge, owner link, managed roster, and Jellyfin install are not yet implemented in this fixture')
except Exception as exc:
    print('BLOCKED: ' + str(exc), file=sys.stderr, flush=True)
    sys.exit(1)

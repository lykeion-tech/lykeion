#!/usr/bin/env python3
"""Validate design traceability; --write regenerates the catalogue, not OpenAPI."""
import argparse
import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
API = ROOT / 'docs/specs/backend-api.md'
PLAN = ROOT / 'docs/plans/backend-implementation.md'
CAT = ROOT / 'docs/contracts/api-catalog.json'

def catalogue():
    source = API.read_text()
    commands, handlers, endpoints, groups, frames = [], [], [], [], []
    section, features = 0, []
    for line in source.splitlines():
        m = re.match(r'## (\d+)\.', line)
        if m:
            section = int(m[1])
        m = re.match(r'### F(\d+)(?:–F(\d+))? (.+)', line)
        if m:
            first, last = int(m[1]), int(m[2] or m[1])
            features = [f'F{i:02}' for i in range(first, last + 1)]
            groups.append({'feature_ids': features, 'title': m[3]})
        cells = [x.strip() for x in line.strip('|').split('|')]
        if not line.startswith('| '):
            continue
        if section == 3 and len(cells) == 3 and cells[1] in ('client → server', 'server → client'):
            frames.append({'type': cells[0], 'direction': cells[1], 'payload_design': cells[2]})
        if re.fullmatch(r'[a-z]+\.[a-z-]+', cells[0]):
            if section == 5:
                assert len(cells) == 3, line
                commands.append({'name': cells[0], 'feature_group': features,
                                 'payload_design': cells[1], 'authority_and_effect': cells[2]})
            elif section == 7:
                handlers.append({'kind': cells[0], 'parameters_and_output': cells[1]})
        if section in (6, 7) and re.match(r'(GET|POST) `/', cells[0]):
            method, path = cells[0].split(' ', 1)
            endpoints.append({'service': 'worker' if section == 6 else 'science',
                              'method': method, 'path': path.strip('`'),
                              'request_response_design': cells[1],
                              'semantics': cells[2] if len(cells) > 2 else None})
    rows = []
    for line in PLAN.read_text().splitlines():
        m = re.match(r'\| (F\d{2}) \| (.*?) \| (.*?) \| (.*?) \|$', line)
        if m:
            rows.append({'feature_id': m[1], 'ownership': m[2], 'api_seam': m[3],
                         'acceptance': m[4], 'slices': re.findall(r'S\d{2}', m[4])})
    return {'schema_version': '1', 'status': 'design-not-implemented',
            'format': 'design-catalogue-not-openapi',
            'source': 'docs/specs/backend-api.md',
            'command_transport': 'POST /v1/workspaces/{workspace_id}/commands/{command_name}',
            'feature_groups': groups, 'commands': commands,
            'secure_credential_operations': [
                {'name': 'credential.issue', 'payload_design': 'approved_decision_ref, scopes, expiry',
                 'response': 'One-time secure delivery; secret excluded from reusable operation receipts/events'},
                {'name': 'credential.revoke', 'payload_design': 'credential_id',
                 'response': 'Durable revocation receipt; invalidate further use'}],
            'service_endpoints': endpoints, 'science_handlers': handlers,
            'realtime': {
                'workspace_upgrade': 'GET /v1/workspaces/{w}/realtime',
                'workspace_subprotocol': 'lykeion.workspace.v1',
                'replay': 'GET /v1/workspaces/{w}/events?after={cursor}',
                'replay_format': 'paginated-json',
                'workspace_frames': frames,
                'worker_subprotocol': 'lykeion.worker.v1',
                'worker_frames': ['worker.hello', 'worker.wake', 'worker.directive', 'worker.ack', 'worker.resync_required', 'error'],
                'mutation_transport': 'HTTP commands; WebSocket document synchronization carries committed edits only'},
            'account_endpoints': [{'method': 'POST', 'path': '/v1/invitations/{id}/accept',
                                   'authority': 'Authenticated recipient plus one-time bound proof and revision'},
                                  {'method': 'GET', 'path': '/v1/me/operations/{id}',
                                   'authority': 'Authenticated operation recipient; no pre-admission workspace disclosure'}],
            'collaboration_acceptance': [{'id': m[1], 'owners': m[2], 'acceptance': m[3]}
                for m in re.finditer(r'^\| (C\d{2}) \| (.*?) \| (.*?) \|$', PLAN.read_text(), re.M)],
            'feature_acceptance': rows,
            'product_acceptance_scenarios': list(range(1, 41)),
            'not_yet_wire_schemas': ['Referenced typed records', 'Auth/device/OAuth provider adapters',
                                    'Native updater transport', 'Numerical programme policy values']}

def check(data):
    names = [x['name'] for x in data['commands']]
    assert len(names) == len(set(names)), 'Duplicate command'
    assert len(data['science_handlers']) == 16
    assert len(data['service_endpoints']) == 16
    assert len(data['realtime']['workspace_frames']) == 13
    assert [x['id'] for x in data['collaboration_acceptance']] == [f'C{i:02}' for i in range(1, 13)]
    assert [x['feature_id'] for x in data['feature_acceptance']] == [f'F{i:02}' for i in range(1, 28)]
    assert all(x['slices'] for x in data['feature_acceptance'])
    text = (ROOT / 'docs/specs/scientific-workflow.md').read_text()
    assert set(map(int, re.findall(r'^(\d+)\. ', text, re.M))) >= set(range(1, 41))
    audit = ROOT / 'research/multica-reference/API-GAP-AUDIT.md'
    inventory = (ROOT / 'research/multica-reference/INVENTORY.md').read_text().split('## Capability inventory')[1].split('## Reliability mechanisms')[0]
    inv_names = [r.split('|')[1].strip() for r in inventory.splitlines() if r.startswith('| ')][1:]
    audit_table = audit.read_text().split('Additional reliability item')[0]
    audit_names = [r.split('|')[1].strip() for r in audit_table.splitlines() if r.startswith('| ')][2:]
    assert set(inv_names) == set(audit_names), (set(inv_names) - set(audit_names), set(audit_names) - set(inv_names))
    scenario_table = PLAN.read_text().split('## Acceptance scenario ownership')[1].split('## Collaboration acceptance supplement')[0]
    scenario_ids = []
    for row in scenario_table.splitlines():
        if re.match(r'\| \d', row):
            scenario_ids.extend(map(int, re.findall(r'\d+', row.split('|')[1])))
    assert sorted(scenario_ids) == list(range(1, 41)), 'Scenario ownership drift'
    assert 'supplies SSE' not in API.read_text()
    assert 'Use bounded long polling initially' not in API.read_text()
    for doc in [API, PLAN, audit, ROOT / 'research/visual-baseline/api-design-reference/README.md']:
        for link in re.findall(r'\]\(([^)]+)\)', doc.read_text()):
            if '://' not in link and not link.startswith('#'):
                assert (doc.parent / link.split('#')[0]).exists(), f'Broken link: {doc}: {link}'
    evidence = ROOT / 'research/visual-baseline/api-design-reference'
    manifest = json.loads((evidence / 'manifest.json').read_text())
    assert len(manifest['files']) == 14
    for entry in manifest['files']:
        assert hashlib.sha256((evidence / entry['file']).read_bytes()).hexdigest() == entry['sha256']

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--write', action='store_true')
    args = parser.parse_args()
    data = catalogue()
    if args.write:
        CAT.write_text(json.dumps(data, indent=2, ensure_ascii=False) + '\n')
    else:
        assert json.loads(CAT.read_text()) == data, 'Catalogue drift: run --write after reviewing changes'
    check(data)
    print(f"Design checks passed: {len(data['commands'])} named commands, 16 worker/science routes, 13 workspace frame types, 16 handlers, 27 features, 40 scientific + 12 collaboration cases, complete inventory mapping, 14 evidence hashes. Not an implementation test.")

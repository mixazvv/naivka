#!/usr/bin/env python3
"""Add only signatures to immutable, pinned historical release assets.

Never execute/rebuild old binaries, replace archives/sums, change latest, or sign
arbitrary URLs. Each old release is pinned by asset ID, byte count and GitHub's
SHA-256 digest in a reviewed repository manifest. Re-download and hash all three
assets, check both archive hashes against the original sums, then sign those
exact bytes with the current release key. Existing signatures must verify.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.request

REPOSITORY = 'mixazvv/naivka'
ROOT = Path(__file__).resolve().parent.parent


def validate_manifest(data):
    if data.get('repository') != REPOSITORY:
        raise ValueError('Unexpected repository')
    releases = data['releases']
    if not isinstance(releases, list) or len(releases) > 100:
        raise ValueError('Unexpected release count')
    seen = set()
    for release in releases:
        tag = release['tag']
        if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', tag) or tag in seen:
            raise ValueError('Invalid/duplicate tag')
        seen.add(tag)
        expected = {f'Naivka-{tag}-windows.zip', f'Naivka-{tag}-windows7.zip', 'SHA256SUMS.txt'}
        if len(release['assets']) != 3 or {a['name'] for a in release['assets']} != expected:
            raise ValueError('Unexpected asset names')
        for asset in release['assets']:
            if not re.fullmatch('[a-f0-9]{64}', asset['sha256']):
                raise ValueError('Missing pinned digest')
            if not isinstance(asset['id'], int) or asset['id'] <= 0:
                raise ValueError('Missing pinned asset identity')
            limit = 1024 if asset['name'] == 'SHA256SUMS.txt' else 300 * 1024 * 1024
            if not isinstance(asset['size'], int) or not 0 < asset['size'] <= limit:
                raise ValueError('Invalid asset size')
    return releases


def verify_metadata(release, actual):
    if actual['tag_name'] != release['tag'] or actual['draft'] or actual['prerelease']:
        raise ValueError('Release identity/status changed')
    assets = {a['name']: a for a in actual['assets']}
    for pinned in release['assets']:
        current = assets.get(pinned['name'], {})
        if any(current.get(k) != pinned[k] for k in ('id', 'size')) or current.get('digest') != 'sha256:' + pinned['sha256']:
            raise ValueError('Historical asset metadata changed: ' + pinned['name'])
    return assets


def download_verified(url, destination, expected, opener=urllib.request.urlopen):
    digest = hashlib.sha256()
    count = 0
    req = urllib.request.Request(url, headers={'User-Agent': 'Naivka-historical-signatures'})
    with opener(req, timeout=60) as response, destination.open('wb') as output:
        while True:
            chunk = response.read(1024 * 1024)
            if not chunk:
                break
            count += len(chunk)
            if count > expected['size']:
                raise ValueError('Asset exceeds pinned size')
            digest.update(chunk)
            output.write(chunk)
    if count != expected['size'] or digest.hexdigest() != expected['sha256']:
        raise ValueError('Historical asset hash/size mismatch')


def verify_sums(release, contents):
    sums = {}
    for line in contents.decode('ascii').splitlines():
        match = re.fullmatch(r'([a-f0-9]{64}) [ *]([^/\\\s]+)', line)
        if not match or match[2] in sums:
            raise ValueError('Invalid original sums')
        sums[match[2]] = match[1]
    expected = {a['name']: a['sha256'] for a in release['assets'] if a['name'].endswith('.zip')}
    if sums != expected:
        raise ValueError('Original sums disagree with pinned archive digests')


def check_signature(public, sums, signature, work):
    raw = base64.b64decode(signature.strip(), validate=True)
    if len(raw) != 64:
        raise ValueError('Invalid signature size')
    path = work / 'verify.sig'
    path.write_bytes(raw)
    subprocess.run(['openssl', 'pkeyutl', '-verify', '-pubin', '-keyform', 'DER', '-inkey', str(public), '-rawin', '-in', str(sums), '-sigfile', str(path)], check=True, stdout=subprocess.DEVNULL)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--check-only', action='store_true')
    parser.add_argument('--shard', type=int, default=0)
    parser.add_argument('--shards', type=int, default=1)
    args = parser.parse_args()
    releases = validate_manifest(json.loads((ROOT / 'docs/historical-release-hashes.json').read_text()))
    if not 1 <= args.shards <= 10 or not 0 <= args.shard < args.shards:
        raise ValueError('Invalid shard')
    if args.check_only:
        print(f'Validated pinned identities/digests for {len(releases)} historical releases')
        return
    secret = os.environ.pop('SIGN_KEY', '')
    if not secret:
        raise ValueError('NAIVKA_SIGN_KEY is required')
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix='naivka-historical-') as temp:
        work = Path(temp)
        key = work / 'key.pem'
        key.write_text(secret)
        secret = ''
        public = work / 'public.der'
        match = re.search(r'^var updatePubKey = "([^"]+)"$', (ROOT / 'signing.go').read_text(), re.M)
        public.write_bytes(bytes.fromhex('302a300506032b6570032100') + base64.b64decode(match[1], validate=True))
        # Validate the secret against the embedded public key before any upload.
        probe = work / 'probe'
        probe.write_bytes(b'Naivka historical signature key validation')
        sig = subprocess.check_output(['openssl', 'pkeyutl', '-sign', '-rawin', '-inkey', str(key), '-in', str(probe)])
        check_signature(public, probe, base64.b64encode(sig), work)
        for i, release in enumerate(releases):
            if i % args.shards != args.shard:
                continue
            tag = release['tag']
            req = urllib.request.Request(f'https://api.github.com/repos/{REPOSITORY}/releases/tags/{tag}', headers={'Authorization': 'Bearer ' + os.environ['GH_TOKEN'], 'Accept': 'application/vnd.github+json', 'User-Agent': 'Naivka-historical-signatures'})
            with urllib.request.urlopen(req, timeout=30) as response:
                raw = response.read(1024 * 1024 + 1)
            if len(raw) > 1024 * 1024:
                raise ValueError('Oversized release metadata')
            actual = verify_metadata(release, json.loads(raw))
            print(f'{tag}: verifying original archives and sums', flush=True)
            for asset in release['assets']:
                url = f'https://github.com/{REPOSITORY}/releases/download/{tag}/{asset["name"]}'
                download_verified(url, work / asset['name'], asset)
            sums = work / 'SHA256SUMS.txt'
            verify_sums(release, sums.read_bytes())
            signature = work / 'SHA256SUMS.txt.sig'
            if signature.name in actual:
                # Concurrent/repeated runs may have completed this tag. Verify
                # instead of overwriting an existing signature.
                existing = actual[signature.name]
                if not 0 < existing['size'] <= 4096 or not re.fullmatch(r'sha256:[a-f0-9]{64}', existing.get('digest', '')):
                    raise ValueError('Invalid existing signature metadata')
                download_verified(f'https://github.com/{REPOSITORY}/releases/download/{tag}/{signature.name}', signature, {'size': existing['size'], 'sha256': existing['digest'][7:]})
                check_signature(public, sums, signature.read_bytes(), work)
                print(f'{tag}: existing signature verified', flush=True)
            else:
                raw_signature = subprocess.check_output(['openssl', 'pkeyutl', '-sign', '-rawin', '-inkey', str(key), '-in', str(sums)])
                signature.write_bytes(base64.b64encode(raw_signature))
                check_signature(public, sums, signature.read_bytes(), work)
                # No --clobber, no changes to archive/sums/tag/latest metadata.
                subprocess.run(['gh', 'release', 'upload', tag, str(signature), '--repo', REPOSITORY], check=True)
                print(f'{tag}: signature uploaded; archives unchanged', flush=True)
            for asset in release['assets']:
                (work / asset['name']).unlink()
            signature.unlink()


if __name__ == '__main__':
    main()

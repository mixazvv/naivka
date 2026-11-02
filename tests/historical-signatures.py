import base64
import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('historical', ROOT / 'scripts/sign-historical-releases.py')
historical = importlib.util.module_from_spec(spec)
spec.loader.exec_module(historical)


class HistoricalSignatures(unittest.TestCase):
    def setUp(self):
        self.data = json.loads((ROOT / 'docs/historical-release-hashes.json').read_text())

    def test_manifest_and_original_metadata_are_pinned(self):
        releases = historical.validate_manifest(self.data)
        self.assertEqual(len(releases), 37)
        release = releases[0]
        actual = {'tag_name': release['tag'], 'draft': False, 'prerelease': False, 'assets': [dict(a, digest='sha256:' + a['sha256']) for a in release['assets']]}
        historical.verify_metadata(release, actual)
        for key, value in [('id', 1), ('size', 1), ('digest', 'sha256:' + '0' * 64)]:
            changed = copy.deepcopy(actual)
            changed['assets'][0][key] = value
            with self.assertRaises(ValueError):
                historical.verify_metadata(release, changed)
        self.data['releases'][0]['assets'][0]['name'] = '../archive.zip'
        with self.assertRaises(ValueError):
            historical.validate_manifest(self.data)

    def test_original_sums_must_match_both_pinned_archives(self):
        release = self.data['releases'][0]
        text = ''.join(a['sha256'] + '  ' + a['name'] + '\n' for a in release['assets'] if a['name'].endswith('.zip')).encode()
        historical.verify_sums(release, text)
        for altered in (text[:-5], text + text, text.replace(text[:64], b'0' * 64), text + b'00  arbitrary.exe\n'):
            with self.assertRaises(ValueError):
                historical.verify_sums(release, altered)

    def test_streaming_download_rejects_truncation_extension_and_hash_changes(self):
        original = b'pinned original release bytes'
        expected = {'size': len(original), 'sha256': hashlib.sha256(original).hexdigest()}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'asset'
            historical.download_verified('https://github.com/fixture', path, expected, lambda *a, **kw: io.BytesIO(original))
            self.assertEqual(path.read_bytes(), original)
            for altered in (original[:-1], original + b'x', b'x' * len(original)):
                with self.assertRaises(ValueError):
                    historical.download_verified('https://github.com/fixture', path, expected, lambda *a, **kw: io.BytesIO(altered))

    def test_signature_uses_the_embedded_public_key_verification_format(self):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp)
            key, public, sums = (work / n for n in ('key', 'public', 'sums'))
            subprocess.run(['openssl', 'genpkey', '-algorithm', 'ED25519', '-out', str(key)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(['openssl', 'pkey', '-in', str(key), '-pubout', '-outform', 'DER', '-out', str(public)], check=True)
            sums.write_bytes(b'original pinned SHA256SUMS contents\n')
            raw = subprocess.check_output(['openssl', 'pkeyutl', '-sign', '-rawin', '-inkey', str(key), '-in', str(sums)])
            historical.check_signature(public, sums, base64.b64encode(raw), work)
            sums.write_bytes(b'changed')
            with self.assertRaises(subprocess.CalledProcessError):
                historical.check_signature(public, sums, base64.b64encode(raw), work)
            with self.assertRaises(ValueError):
                historical.check_signature(public, sums, base64.b64encode(b'short'), work)


if __name__ == '__main__':
    unittest.main()

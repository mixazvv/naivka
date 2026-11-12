#!/usr/bin/env python3
"""Installer filesystem/rollback fixture. All OS and network actions are mocked.
Set MITA_CLIENT to exercise validation RPC with the official binary as well.
Real upstream protocol compatibility is tested by TestMieruRealTransport.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

source = (Path(__file__).resolve().parents[1] / 'server/install-mieru.sh').read_text()
with tempfile.TemporaryDirectory(prefix='naivka-mieru-install-') as tmp:
    root = Path(tmp)
    bins = root / 'bin'
    bins.mkdir()
    base = root / 'installation'
    unit = root / 'service'
    archive = root / 'archive.tar.gz'
    fake_mita = b'''#!/usr/bin/env python3
import os,sys,time
from pathlib import Path
fixture=Path(os.environ['FIXTURE'])
if Path(sys.argv[0]).parent.name != 'current':
    assert os.environ.get('MITA_UDS_PATH','').endswith('/validate.sock'), 'validation used shared RPC socket'
    if os.environ.get('MITA_CLIENT'):
        os.execv(os.environ['MITA_CLIENT'], [os.environ['MITA_CLIENT']]+sys.argv[1:])
    if sys.argv[1]=='run':
        time.sleep(120)
    elif sys.argv[1]=='status':
        print('mita server status is "IDLE"')
    elif sys.argv[1:3]==['apply','config']:
        assert Path(sys.argv[3]).exists()
    else:
        sys.exit(1)
else:
    assert sys.argv[1]=='status'
    if (fixture/'fail').exists(): sys.exit(1)
    state='IDLE' if (fixture/'idle').exists() else 'RUNNING'
    print('mita server status is "'+state+'"')
'''
    with tarfile.open(archive, 'w:gz') as tar:
        entry = tarfile.TarInfo('mita')
        entry.size = len(fake_mita)
        entry.mode = 0o755
        tar.addfile(entry, io.BytesIO(fake_mita))
    digest = 'sha256:' + hashlib.sha256(archive.read_bytes()).hexdigest()
    (root / 'release.json').write_text(json.dumps({'assets': [{'name': 'mita_3.39.0_linux_amd64.tar.gz', 'digest': digest}]}))
    stubs = {
        'curl': '''#!/usr/bin/env python3
import os,sys,shutil
url=next(a for a in sys.argv if a.startswith('https://'))
dst=sys.argv[sys.argv.index('-o')+1]
shutil.copyfile(os.environ['FIXTURE']+('/release.json' if 'api.github.com' in url else '/archive.tar.gz'),dst)
''',
        'systemctl': '''#!/bin/sh
case "$1" in
 is-active|is-enabled) test -f "$FIXTURE/service";;
 *) exit 0;;
esac
''',
        'install': '''#!/usr/bin/env python3
import shutil,sys,os
shutil.copyfile(sys.argv[-2],sys.argv[-1]);os.chmod(sys.argv[-1],0o755 if '-m755' in sys.argv else 0o600)
''',
        'getent': '#!/bin/sh\nexit 0\n',
        'id': '#!/bin/sh\nexit 0\n',
        'uname': '#!/bin/sh\necho x86_64\n',
        'ufw': '#!/bin/sh\necho "Status: inactive"\n',
    }
    for name, body in stubs.items():
        p = bins / name
        p.write_text(body)
        p.chmod(0o755)
    script = source.replace('[[ $EUID == 0 ]]', '[[ 1 == 1 ]]')
    script = script.replace('/opt/naivka-mieru', str(base)).replace('/etc/systemd/system/naivka-mita.service', str(unit))
    script = script.replace('/run/naivka-mieru-install.lock', str(root / 'lock'))
    script = script.replace('{1..30}', '{1..2}').replace('sleep 1', 'sleep 0.01')
    path = root / 'install.sh'
    path.write_text(script)
    env = dict(os.environ, PATH=str(bins) + os.pathsep + os.environ['PATH'], FIXTURE=tmp,
               SERVER_ADDR='203.0.113.10', NAME='fixture')
    def run(ok):
        result = subprocess.run(['bash', str(path)], env=env, capture_output=True, text=True, timeout=45)
        assert (result.returncode == 0) == ok, result.stderr
        return result
    run(True)
    before = (base / 'current/config.json').read_bytes()
    assert json.loads(before)['users'][0]['password']
    run(True)
    assert (base / 'current/config.json').read_bytes() == before, 'reinstall rotated credentials'
    old_unit = unit.read_bytes()
    (root / 'fail').touch()
    run(False)
    assert (base / 'current/config.json').read_bytes() == before, 'rollback lost config'
    assert unit.read_bytes() == old_unit, 'rollback lost service'
    (root / 'fail').unlink()
    (root / 'idle').touch()
    result = run(False)
    assert 'NAIVKA-LINK' not in result.stdout, 'idle proxy reported installation success'
    assert (base / 'current/config.json').read_bytes() == before, 'idle rollback lost config'
    (root / 'idle').unlink()
    if env.get('MITA_CLIENT'):
        # A real RPC validation error must leave the installed files untouched.
        bad = json.loads(before)
        bad['mtu'] = 1
        (base / 'current/config.json').write_text(json.dumps(bad))
        bad_before = (base / 'current/config.json').read_bytes()
        result = run(False)
        assert 'Конфигурация Mieru не прошла проверку' in result.stderr
        assert (base / 'current/config.json').read_bytes() == bad_before
        assert unit.read_bytes() == old_unit
        (base / 'current/config.json').write_bytes(before)
    (root / 'release.json').write_text(json.dumps({'assets': []}))
    run(False)
    assert (base / 'current/config.json').read_bytes() == before, 'unverified asset changed installation'
    assert not list(base.glob('stage.*')), 'temporary secret files survived'
print('Mieru installer: isolated validation, reinstall, rollback, IDLE rejection, digest rejection and cleanup passed')

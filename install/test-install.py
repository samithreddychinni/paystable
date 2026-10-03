#!/usr/bin/env python3
"""Test the installer with a local release binary and no network access."""

import hashlib
import os
from pathlib import Path
import platform
import subprocess
import sys
import tempfile


binary = Path(sys.argv[1]).resolve()
installer = Path(__file__).parent / "public" / "install.sh"
arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine()]
asset = f"paystable-{platform.system().lower()}-{arch}"

with tempfile.TemporaryDirectory(prefix="paystable-install-") as temporary:
    root = Path(temporary)
    shim = root / "shim"
    shim.mkdir()
    # Accept only the intended repository URLs. Never contact a gateway.
    (shim / "curl").write_text('''#!/usr/bin/env python3
import os
from pathlib import Path
import shutil
import sys
args = sys.argv[1:]
url = args[1]
base = "https://github.com/samithreddychinni/paystable/releases/download/v0.3.0/"
if url == "https://api.github.com/repos/samithreddychinni/paystable/releases/latest":
    print('{"tag_name": "v0.3.0"}')
elif url == base + os.environ["INSTALL_TEST_ASSET"]:
    shutil.copyfile(os.environ["INSTALL_TEST_BINARY"], args[3])
elif url == base + "checksums.txt":
    Path(args[3]).write_text(os.environ["INSTALL_TEST_CHECKSUM"])
else:
    sys.exit("unexpected installer URL")
''')
    (shim / "curl").chmod(0o755)
    checksum = hashlib.sha256(binary.read_bytes()).hexdigest()
    env = dict(os.environ, PATH=f"{shim}:{os.environ['PATH']}",
               INSTALL_TEST_BINARY=str(binary), INSTALL_TEST_ASSET=asset)

    def run(name, checksum_text):
        directory = root / name
        directory.mkdir()
        result = subprocess.run(["sh", str(installer.resolve())], cwd=directory,
                                env=dict(env, INSTALL_TEST_CHECKSUM=checksum_text),
                                capture_output=True, text=True)
        return directory, result

    directory, result = run("clean", f"{checksum}  {asset}\n")
    assert result.returncode == 0, result.stdout + result.stderr
    local_env = directory / "paystable" / ".env"
    assert local_env.stat().st_mode & 0o777 == 0o600
    assert local_env.stat().st_size > 0
    assert (directory / "paystable" / "instructions.md").is_file()
    before = local_env.stat()
    result = subprocess.run(["sh", str(installer.resolve())], cwd=directory,
                            env=dict(env, INSTALL_TEST_CHECKSUM=f"{checksum}  {asset}\n"),
                            capture_output=True, text=True)
    assert result.returncode != 0, "installer must refuse an existing .env"
    assert local_env.stat() == before, "installer changed the existing .env"
    for name, checksum_text in [("tampered", f"{'0' * 64}  {asset}\n"),
                                ("missing", f"{checksum}  another-asset\n")]:
        directory, result = run(name, checksum_text)
        assert result.returncode != 0, f"installer accepted {name} checksum"
        assert not (directory / "paystable" / ".env").exists()
    print("PASS: clean install, .env mode 0600, repeat refusal, tampered and missing checksums")

#!/usr/bin/env python3
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location('repair', Path(__file__).with_name('repair-v023-sboms.py'))
REPAIR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REPAIR)


class RepairTests(unittest.TestCase):
    def test_only_blocked_unpublished_v023_draft(self):
        valid = dict(tag_name='v0.2.3', draft=True, immutable=False, assets=[])
        REPAIR.require_draft(valid)
        for update in (dict(tag_name='v0.2.2'), dict(draft=False), dict(immutable=True),
                       dict(assets=[dict(name='checksums.txt.sig')])):
            with self.subTest(update=update), self.assertRaises(SystemExit):
                REPAIR.require_draft(dict(valid, **update))

    def test_original_signed_manifest_rejects_changes_and_paths(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            names = [f'archive-{i}.tar.gz' for i in range(13)] + [f'sbom-{i}.json' for i in range(22)]
            digest = hashlib.sha256(b'fixture').hexdigest()
            for name in names:
                (root / name).write_bytes(b'fixture')
            lines = [f'{digest}  {name}\n' for name in names]
            manifest = ''.join(lines)
            REPAIR.verify_files(root, manifest)
            for bad in (''.join(lines[:-1]), manifest + lines[0], manifest.replace(names[0], '../outside')):
                with self.assertRaises(SystemExit):
                    REPAIR.verify_files(root, bad)
            (root / names[0]).write_bytes(b'changed')
            with self.assertRaises(SystemExit):
                REPAIR.verify_files(root, manifest)

    def test_mislabeled_and_missing_sboms_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for i in range(13):
                (root / f'{i}.tar.gz.spdx.json').write_text('{"spdxVersion":"SPDX-2.3"}')
                (root / f'{i}.tar.gz.cyclonedx.json').write_text('{"bomFormat":"CycloneDX"}')
            REPAIR.validate_sboms(root)
            file = root / '0.tar.gz.cyclonedx.json'
            file.write_text(json.dumps(dict(spdxVersion='SPDX-2.3')))
            with self.assertRaises(SystemExit):
                REPAIR.validate_sboms(root)
            file.unlink()
            with self.assertRaises(SystemExit):
                REPAIR.validate_sboms(root)


if __name__ == '__main__':
    unittest.main()

#!/usr/bin/env python3
"""Exercise the standalone installer's embedded archive extraction code."""

import io
import os
from pathlib import Path
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parent / "install.sh"
EXTRACTOR = INSTALLER.read_text().split(
    "python3 - \"$TEMP_SOURCE\" <<'PY'\n", 1
)[1].split("\nPY\n", 1)[0]

# Reproduce the tarfile API on Python versions without extraction filters.
LEGACY_API = """
import tarfile
original_extractall = tarfile.TarFile.extractall
def legacy_extractall(self, path='.', members=None, *, numeric_owner=False):
    return original_extractall(self, path, members, numeric_owner=numeric_owner)
tarfile.TarFile.extractall = legacy_extractall
if hasattr(tarfile, 'data_filter'):
    del tarfile.data_filter
"""


def entry(name, kind=tarfile.REGTYPE, mode=0o644):
    member = tarfile.TarInfo(name)
    member.type = kind
    member.mode = mode
    member.uid = member.gid = 12345
    if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE):
        member.linkname = "../outside"
    return member


class ArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.destination = self.root / "source"

    def extract(self, members, legacy=False):
        with tarfile.open(self.root / "source.tar.gz", "w:gz") as archive:
            for member in members:
                data = b"release content\n" if member.isfile() else b""
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        return subprocess.run(
            [sys.executable, "-", str(self.root)],
            input=(LEGACY_API if legacy else "") + EXTRACTOR,
            text=True, capture_output=True, umask=0o077,
        )

    def check_valid_archive(self, legacy=False):
        result = self.extract([
            entry("scripts/", tarfile.DIRTYPE, 0o7777),
            entry("scripts/run.sh", mode=0o6777),
            entry("nested/config.txt", mode=0o6666),
        ], legacy=legacy)
        self.assertEqual(result.returncode, 0, result.stderr)
        for name, mode in (("scripts/run.sh", 0o700), ("nested/config.txt", 0o600)):
            path = self.destination / name
            self.assertEqual(path.read_bytes(), b"release content\n")
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), mode)
            self.assertEqual(path.stat().st_uid, os.getuid())
        self.assertEqual(stat.S_IMODE((self.destination / "scripts").stat().st_mode), 0o700)

    def test_valid_archive(self):
        self.check_valid_archive()

    def test_python_without_extraction_filters(self):
        self.check_valid_archive(legacy=True)

    def test_unsafe_paths_rejected_before_writing(self):
        for name in ("../outside", "nested/../../outside", str(self.root / "outside")):
            with self.subTest(name=name):
                result = self.extract([entry("valid.txt"), entry(name)])
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.destination.exists())
                self.assertFalse((self.root / "outside").exists())

    def test_links_and_special_files_rejected_before_writing(self):
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE, tarfile.FIFOTYPE, tarfile.CHRTYPE, tarfile.BLKTYPE):
            with self.subTest(kind=kind):
                result = self.extract([entry("valid.txt"), entry("special", kind)])
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.destination.exists())

    def test_existing_destination_is_not_used(self):
        outside = self.root / "outside"
        outside.mkdir()
        self.destination.symlink_to(outside, target_is_directory=True)
        result = self.extract([entry("valid.txt")])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(list(outside.iterdir()), [])

    def test_duplicate_files_rejected(self):
        result = self.extract([entry("duplicate.txt"), entry("duplicate.txt")])
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()

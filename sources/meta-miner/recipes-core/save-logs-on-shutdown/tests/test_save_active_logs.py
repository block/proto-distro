"""Run with Python 3 on Linux; all log paths use temporary directories."""
import gzip
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "files/save-active-logs.sh"


class SaveLogsTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.source = self.root / "source"
        self.destination = self.root / "destination"
        self.source.mkdir()
        self.destination.mkdir()
        self.env = dict(os.environ, SOURCE_LOG_ROOT=str(self.source),
                        PERSISTENT_LOG_ROOT=str(self.destination))

    def write(self, path, content):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)

    def run_backup(self):
        result = subprocess.run(["bash", str(SCRIPT)], env=self.env,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result

    def test_discovers_all_services_and_preserves_contents(self):
        for name in ["mcdd/mcdd.log", "mdk-server/mdk-server.log",
                     "hashboard-service/hashboard-service@9.log",
                     "psu-service/psu-service@3.log", "new service/a b.log"]:
            self.write(self.source / name, name.encode())
        self.run_backup()
        for source in self.source.glob("*/*.log"):
            archives = list((self.destination / source.parent.name).glob(source.name + "-*.gz"))
            self.assertEqual(len(archives), 1)
            self.assertEqual(gzip.decompress(archives[0].read_bytes()), source.read_bytes())

    def test_skips_empty_archived_top_level_and_symlinked_logs(self):
        self.write(self.source / "service/empty.log", b"")
        self.write(self.source / "service/old.log-20260101.gz", b"old")
        self.write(self.source / "top-level.log", b"top")
        self.write(self.root / "external/secret.log", b"outside source")
        (self.source / "service/link.log").symlink_to(self.root / "external/secret.log")
        (self.source / "linked-directory").symlink_to(self.root / "external")
        self.run_backup()
        self.assertEqual(list(self.destination.rglob("*.gz")), [])

    def test_skips_symlinked_destination(self):
        self.write(self.source / "service/service.log", b"content")
        outside = self.root / "outside"
        outside.mkdir()
        (self.destination / "service").symlink_to(outside)
        self.run_backup()
        self.assertEqual(list(outside.iterdir()), [])

    def test_failed_compression_does_not_skip_later_services(self):
        commands = self.root / "bin"
        commands.mkdir()
        command = commands / "gzip"
        command.write_text('#!/bin/sh\ncase "$*" in *nginx*) echo partial; exit 1;; esac\nexec /usr/bin/gzip "$@"\n')
        command.chmod(0o755)
        self.env["PATH"] = str(commands) + ":" + os.environ["PATH"]
        self.write(self.source / "nginx/access.log", b"nginx")
        self.write(self.source / "psu-service/psu-service@1.log", b"psu")
        result = self.run_backup()
        self.assertIn("Could not save", result.stderr)
        self.assertEqual(list((self.destination / "nginx").iterdir()), [])
        self.assertEqual(len(list((self.destination / "psu-service").glob("*.gz"))), 1)

    def test_retention_uses_mtime_not_filename_and_preserves_unrelated_files(self):
        directory = self.destination / "service"
        for index in range(35):
            path = directory / f"service.log-{2100-index}0101-000000.gz"
            self.write(path, b"archive")
            os.utime(path, (1000 + index, 1000 + index))
        self.write(directory / "keep.txt", b"unrelated")
        self.write(self.source / "service/service.log", b"new log")
        self.run_backup()
        archives = list(directory.glob("*.gz"))
        self.assertEqual(len(archives), 30)
        self.assertTrue((directory / "keep.txt").exists())
        for index in range(6):
            self.assertFalse((directory / f"service.log-{2100-index}0101-000000.gz").exists())

    def test_low_or_unknown_space_skips_copy(self):
        commands = self.root / "bin"
        commands.mkdir()
        command = commands / "df"
        self.env["PATH"] = str(commands) + ":" + os.environ["PATH"]
        self.write(self.source / "service/service.log", b"content")
        for available in ("50000", "unknown"):
            command.write_text(f'#!/bin/sh\necho "Filesystem 1024-blocks Used Available Capacity Mounted"\necho "disk 100000 0 {available} 0% /data"\n')
            command.chmod(0o755)
            self.run_backup()
            self.assertEqual(list(self.destination.rglob("*.gz")), [])


if __name__ == "__main__":
    unittest.main()

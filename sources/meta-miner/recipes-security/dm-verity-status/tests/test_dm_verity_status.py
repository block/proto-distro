#!/usr/bin/env python3
"""Run the status CLI against disposable sysfs and command fixtures."""
import os
from pathlib import Path
import subprocess
import tempfile


SOURCE = Path(__file__).resolve().parents[1] / "files" / "dm-verity-status"
COMMANDS = """#!/bin/sh
case "${0##*/}" in
uname) echo Linux ;;
id) echo 0 ;;
findmnt) echo '/dev/dm-0 253:0 ext4 ro' ;;
dmsetup)
    case "$1" in
    info) echo '253:0:L--r:1:0' ;;
    table) echo '0 8192 verity 1 179:7 179:7 4096 4096 1024 1024 sha256 digest salt' ;;
    status) echo "0 8192 verity $TEST_VERITY_STATUS" ;;
    *) exit 1 ;;
    esac ;;
*) exit 1 ;;
esac
"""


def check(backing, expected, code=0, status="V"):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        sysfs = root / "sysfs"
        device = sysfs / "253:0"
        (device / "dm").mkdir(parents=True)
        (device / "slaves").mkdir()
        for name, value in {"dm/name": "rootfs", "ro": "1", "dm/suspended": "0", "size": "8192"}.items():
            (device / name).write_text(value + "\n")
        for name, number in backing.items():
            slave = device / "slaves" / name
            slave.mkdir()
            if number is not None:
                (slave / "partition").write_text(number + "\n")
        commands = root / "bin"
        commands.mkdir()
        for name in ("uname", "id", "findmnt", "dmsetup"):
            command = commands / name
            command.write_text(COMMANDS)
            command.chmod(0o755)
        # Redirect sysfs and trusted commands only in this disposable script copy.
        script = root / "dm-verity-status"
        script.write_text(
            SOURCE.read_text()
            .replace("sysfs=/sys/dev/block", f"sysfs='{sysfs}'")
            .replace("PATH=/usr/sbin:/usr/bin:/sbin:/bin", f"PATH='{commands}:/usr/sbin:/usr/bin:/sbin:/bin'")
        )
        result = subprocess.run(
            ["/bin/sh", str(script)], text=True, capture_output=True,
            env={**os.environ, "TEST_VERITY_STATUS": status},
        )
        assert result.returncode == code, (result.returncode, result.stdout, result.stderr)
        assert expected in result.stdout, result.stdout
        return result.stdout


def check_untrusted_path():
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        marker = root / "executed"
        for name in ("uname", "id", "findmnt", "dmsetup", "cat"):
            command = root / name
            command.write_text(f"#!/bin/sh\nprintf hijacked > '{marker}'\nexit 1\n")
            command.chmod(0o755)
        for arguments in ([], ["--help"]):
            result = subprocess.run(
                ["/bin/sh", str(SOURCE), *arguments], text=True, capture_output=True,
                env={**os.environ, "PATH": str(root)},
            )
            assert not marker.exists(), (arguments, result.stdout, result.stderr)
            if arguments:
                assert result.returncode == 0, (result.stdout, result.stderr)


if __name__ == "__main__":
    check_untrusted_path()
    for number in ("7", "8", "9"):
        check({f"mmcblk0p{number}": number}, f"Boot partition: /dev/mmcblk0p{number} (partition {number})\n")
    for backing in ({}, {"mmcblk0p7": "7", "mmcblk0p8": "8"}, {"dm-1": None}):
        check(backing, "Boot partition: unknown\n")
    for number in ("0", "invalid"):
        check({"mmcblk0p7": number}, "UNKNOWN: invalid backing partition number\n", code=2)
    output = check({"mmcblk0p8": "8"}, "Boot partition: /dev/mmcblk0p8 (partition 8)\n", code=3, status="C")
    assert "corruption has been detected" in output, output
    print("PASS trusted PATH, boot partitions 7/8/9, ambiguous/nested mappings, invalid partition numbers, and corruption status")

#!/usr/bin/env python3
"""Run with: python3 test_bootargs.py /path/to/sandbox/u-boot.

Uses native U-Boot Hush/setexpr and persisted env export/import. AVB, MMC,
FIT boot, reset, and recovery are hardware substitutes. No board is accessed.
"""
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile


SCRIPT = Path(__file__).resolve().parents[1] / "files" / "boot-release.scr.cmd"
GUID = "ce1b3eab-4d12-4a60-b4c5-877c2cf21102"
ROOT_HASH = "2ed3d8f204c6b2d7c4c49e21523577aa3d2bb19faab989e373f89969a3b26706"
SALT = "a26ba8c996d77ed2d03d5c055cff84bc3a9281b0aca546332295013a1e0758f4"
VERITY = (
    f'dm-mod.create="vroot,,0,ro,0 464744 verity 1 PARTUUID={GUID} '
    f'PARTUUID={GUID} 1024 1024 232372 232372 sha256 {ROOT_HASH} {SALT} '
    '1 ignore_zero_blocks" root=/dev/dm-0'
)
ANDROID = (
    f"androidboot.vbmeta.device=PARTUUID={GUID} androidboot.vbmeta.avb_version=1.1 "
    "androidboot.vbmeta.device_state=unlocked androidboot.vbmeta.hash_alg=sha256 "
    "androidboot.vbmeta.size=2304 "
    "androidboot.vbmeta.digest=a261018e81fa9379ec177903c55f349f8f807ca537573abb4d1fb841da212110 "
    "androidboot.vbmeta.invalidate_on_error=yes androidboot.veritymode=enforcing "
    "androidboot.verifiedbootstate=green"
)
TRAILER = "console=ttynull ro rootwait quiet"


def run(binary, arguments, *, verify="y", slot="7", init="true", verification="true",
        filter_failure=False, statefile=None, restore=False,
        load="false", boot="false", script=SCRIPT, **environment):
    script = script.read_text()
    script = script.replace("avb init ${devnum}", "run proof_avb_init")
    script = script.replace("avb verify ${avb_slot}", "run proof_avb_verify")
    script = script.replace("part uuid mmc ${devnum}:${rootpart} rootpart_partuuid", "run proof_part_uuid")
    script = script.replace("if load mmc ${devnum}:${rootpart} ${kernel_addr_r} /boot/fitImage; then",
                            f"if echo PROOF_FIT_LOAD; {load}; then")
    script = script.replace("bootm ${kernel_addr_r}${kernel_boot_conf}", f"echo PROOF_BOOTM; {boot}")
    script = script.replace("load mmc ${devnum}:${rootpart} ${kernel_addr_r} /boot/fitImage",
                            f"echo PROOF_FIT_LOAD; {load}")
    script = script.replace("saveenv", "run proof_saveenv")
    script = script.replace("bootcount reset", "setenv bootcount 0")
    script = script.replace("stm32prog usb 0", "echo PROOF_ST_DFU")
    script = script.replace("fastboot udp", "echo PROOF_UDP; poweroff")
    state = "env print rootpart ustate bootcount"
    lines = []
    for line in script.splitlines():
        if line.strip() == "reset":
            line = f"{state}; echo PROOF_RESET; poweroff"
        if filter_failure and line.strip().startswith("if setexpr bootargs gsub "):
            line = "    if false; then"
        lines.append(line)
    values = {
        "verify": verify, "rootpart": slot, "a_partnum": "7", "b_partnum": "8",
        "devnum": "1", "console": "ttySTM0", "baudrate": "115200",
        "bootcount": "0", "ustate": "0", "reboot_fastboot": "0",
        "factory_reset": "n", "ipaddr": "", "bootargs": "STALE", "avb_bootargs": VERITY,
        "proof_arguments": arguments,
        "proof_avb_init": f"echo PROOF_AVB_INIT; {init}",
        "proof_avb_verify": (
            'echo PROOF_AVB_VERIFY_${avb_slot}; '
            'setenv avb_bootargs "${proof_arguments}"; true'
            if verification == "true" else "echo PROOF_AVB_FAILED; false"
        ),
        "proof_part_uuid": f"setenv rootpart_partuuid {GUID}",
        "altbootcmd": f"{state}; echo PROOF_UDP; poweroff",
    }
    values.update(environment)
    with tempfile.TemporaryDirectory() as directory:
        statefile = statefile or Path(directory) / "saved.env"
        commands = [
            f"setenv {name}" + (f" {shlex.quote(value)}" if value else "")
            for name, value in values.items()
            if not (restore and name in ("rootpart", "ustate"))
        ]
        if restore:
            commands.extend([
                f"host load hostfs - 1000000 {shlex.quote(str(statefile))}",
                "env import -t 1000000 ${filesize} rootpart ustate",
            ])
        save_command = (
            "env export -t 1000000 rootpart ustate && "
            f"host save hostfs - 1000000 {shlex.quote(str(statefile))} ${{filesize}}"
        )
        commands.append(f"setenv proof_saveenv {shlex.quote(save_command)}")
        commands.extend(lines)
        result = subprocess.run(
            [str(binary), "-d", str(binary.parent / "u-boot.dtb"), "-c", "\n".join(commands)],
            cwd=directory, capture_output=True, text=True, timeout=30,
            env={**os.environ, "SDL_VIDEODRIVER": "dummy", "SDL_AUDIODRIVER": "dummy"},
        )
    output = result.stdout + result.stderr
    assert result.returncode >= 0, output
    return output


if __name__ == "__main__":
    binary = Path(sys.argv[1]).resolve()
    full = f"{VERITY} {ANDROID}"
    expected = f"{VERITY} {TRAILER}"
    for slot, verify in (("7", "y"), ("8", "y"), ("7", ""), ("7", "unexpected")):
        output = run(binary, full, slot=slot, verify=verify)
        assert f"Bootargs: {expected}\n" in output, output
        assert f"PROOF_AVB_VERIFY_{'_a' if slot == '7' else '_b'}" in output, output
        assert "PROOF_FIT_LOAD" in output, output
    output = run(binary, f"{full} other=keep androidboot.extra=drop final=keep")
    assert f"Bootargs: {VERITY} other=keep final=keep {TRAILER}\n" in output, output
    for options in (
        {"init": "false"},
        {"verification": "false"},
        {"filter_failure": True},
        {"slot": "9"},
    ):
        output = run(binary, **{"arguments": full, **options})
        assert "PROOF_RESET" in output, output
        assert "Bootargs:" not in output and "PROOF_FIT_LOAD" not in output, output
    for arguments in ("", f"root=PARTUUID={GUID}", f"{VERITY}x"):
        output = run(binary, arguments, verity_root="y")
        assert "PROOF_AVB_VERIFY__a" in output and "Bootargs:" not in output, output
        assert "PROOF_RESET" in output and "PROOF_FIT_LOAD" not in output, output
    output = run(binary, full, verify="n")
    assert f"Bootargs: root=PARTUUID={GUID} rw rootwait quiet\n" in output, output
    assert "PROOF_AVB" not in output and "PROOF_FIT_LOAD" in output, output
    print(f"PASS native Hush/setexpr: {len(full + ' ' + TRAILER)} to {len(expected)} bytes; exact quoted mapping preserved")
    print("PASS A/B, default verification, failure/stale-state guards, and explicit verify=n")

    for slot, previous, suffix in (("7", "8", "_b"), ("8", "7", "_a")):
        with tempfile.TemporaryDirectory() as directory:
            statefile = Path(directory) / "saved.env"
            output = run(binary, full, slot=slot, verification="false",
                         ustate="1", bootcount="2", statefile=statefile)
            assert not statefile.exists(), output
            assert all(value in output for value in (f"rootpart={slot}\n", "ustate=1\n", "PROOF_RESET")), output
            assert "PROOF_UDP" not in output and "PROOF_FIT_LOAD" not in output, output
            output = run(binary, full, slot=slot, ustate="1", bootcount="3", statefile=statefile)
            saved = statefile.read_text()
            assert f"rootpart={slot}\n" in saved and "ustate=3\n" in saved, output
            assert "PROOF_RESET" in output and "PROOF_FIT_LOAD" not in output, output
            output = run(binary, full, restore=True, statefile=statefile, load="true", boot="poweroff")
            assert f"Booting from mmc 1:{previous}" in output, output
            assert f"PROOF_AVB_VERIFY_{suffix}" in output and f"Bootargs: {expected}" in output, output
            assert "PROOF_RESET" not in output and "PROOF_UDP" not in output, output
            fallback = statefile.read_text()
            assert f"rootpart={previous}\n" in fallback and "ustate=3\n" in fallback, output
            statefile.write_text(saved)
            output = run(binary, full, restore=True, statefile=statefile, verification="false")
            assert all(value in output for value in (f"rootpart={previous}\n", "ustate=3\n", "PROOF_RESET")), output
            assert "PROOF_UDP" not in output and "PROOF_FIT_LOAD" not in output, output
            assert statefile.read_text() == fallback

    for script in (SCRIPT, SCRIPT.with_name("boot-default.scr.cmd")):
        for slot, previous in (("7", "8"), ("8", "7")):
            with tempfile.TemporaryDirectory() as directory:
                statefile = Path(directory) / "saved.env"
                output = run(binary, full, script=script, slot=slot, ustate="1",
                             bootcount="3", factory_reset="y", statefile=statefile)
                saved = statefile.read_text()
                assert f"rootpart={slot}\n" in saved and "ustate=3\n" in saved, output
                assert "bootcount=3\n" in output and "PROOF_RESET" in output, output
                assert "PROOF_AVB" not in output and "PROOF_FIT_LOAD" not in output, output
                output = run(binary, full, script=script, restore=True, statefile=statefile,
                             load="true", boot="poweroff")
                assert f"Booting from mmc 1:{previous}" in output and "PROOF_FIT_LOAD" in output, output
                assert "PROOF_RESET" not in output, output
                fallback = statefile.read_text()
                assert f"rootpart={previous}\n" in fallback and "ustate=3\n" in fallback, output
        output = run(binary, full, script=script, reboot_dfu="2", verification="false")
        assert "PROOF_ST_DFU" in output and "PROOF_RESET" in output, output
        assert "PROOF_AVB" not in output and "PROOF_UDP" not in output, output

    for load in ("true", "false"):
        output = run(binary, full, load=load, boot="false")
        assert all(value in output for value in ("rootpart=7\n", "ustate=0\n", "PROOF_RESET")), output
        assert "PROOF_UDP" not in output, output
        assert ("PROOF_BOOTM" in output) == (load == "true"), output
    print("PASS existing OTA timeout/A/B rollback, kernel/load failure, ST DFU")

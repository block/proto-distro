"""Exercise the installed distribution, never imports from the source checkout.

Run with standalone uv/uvx and OpenSSL 3.x on PATH:
    uv run --managed-python --python 3.12 --no-project python python/tests/smoke_wheel.py --wheel /absolute/path/to/proto_distro_security_tools-0.0.0-py3-none-any.whl --sdist /absolute/path/to/proto_distro_security_tools-0.0.0.tar.gz
"""

import argparse
import configparser
import email
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile

COMMANDS = {
    "fsbl_signer": "fsbl.fsbl_signer:cli",
    "create_avb_image": "create_avb_image.create_avb_image:main",
    "verify_avb_image": "create_avb_image.create_avb_image:verify_main",
    "avbtool": "create_avb_image.create_avb_image:avbtool_main",
}
EXCLUDED_COMMANDS = ("cot_signer", "optee_keys", "replace_uboot_avbkey")
EXCLUDED_MODULES = (
    *EXCLUDED_COMMANDS,
    "hvac",
    "requests",
    "asn1crypto",
    "pyasn1",
    "pyasn1_modules",
    "elftools",
    "cryptography",
    "cffi",
    "pycparser",
    "certifi",
    "charset_normalizer",
    "idna",
    "urllib3",
)
EXCLUDED_DEPENDENCIES = (
    "hvac",
    "requests",
    "asn1crypto",
    "pyasn1",
    "pyasn1-modules",
    "pyelftools",
    "cryptography",
    "cffi",
    "pycparser",
    "certifi",
    "charset-normalizer",
    "idna",
    "urllib3",
)
RUNTIME_DEPENDENCIES = {"click", "pycryptodome"}

LICENSE_FILES = ("LICENSE", "create_avb_image/_vendor/LICENSE")
APACHE_SHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"


def inspect_license_metadata(metadata):
    assert tuple(map(int, metadata["Metadata-Version"].split("."))) >= (2, 4), metadata
    assert metadata["License-Expression"] == "Apache-2.0 AND MIT", metadata
    assert sorted(metadata.get_all("License-File", [])) == sorted(LICENSE_FILES), metadata
    runtime = {item for item in metadata.get_all("Requires-Dist", []) if "extra ==" not in item}
    assert runtime == RUNTIME_DEPENDENCIES, runtime


def inspect_license_contents(apache, mit, vendor):
    assert hashlib.sha256(apache).hexdigest() == APACHE_SHA256, "Apache license text differs"
    header = vendor.decode().splitlines()[2:23]
    notice = "\n".join(line.removeprefix("# ").removeprefix("#") for line in header) + "\n"
    assert mit == notice.encode(), "Vendored AVB license differs from its source notice"
    assert mit.startswith(b"Copyright 2016, The Android Open Source Project\n"), mit
    assert b"Permission is hereby granted, free of charge" in mit, mit
    assert mit.endswith(b"SOFTWARE.\n"), mit


def inspect_wheel(wheel: Path) -> None:
    with zipfile.ZipFile(wheel) as archive:
        names = archive.namelist()
        (metadata_path,) = [name for name in names if name.endswith(".dist-info/METADATA")]
        metadata = email.message_from_bytes(archive.read(metadata_path))
        assert metadata["Name"] == "proto-distro-security-tools", metadata
        assert metadata["Requires-Python"] == ">=3.12", metadata
        inspect_license_metadata(metadata)
        assert wheel.name == f"proto_distro_security_tools-{metadata['Version']}-py3-none-any.whl", wheel
        prefix = metadata_path.removesuffix("METADATA")
        wheel_metadata = email.message_from_bytes(archive.read(prefix + "WHEEL"))
        assert wheel_metadata["Root-Is-Purelib"] == "true", wheel_metadata
        assert wheel_metadata.get_all("Tag") == ["py3-none-any"], wheel_metadata
        entries = configparser.ConfigParser()
        entries.read_string(archive.read(prefix + "entry_points.txt").decode())
        assert dict(entries["console_scripts"]) == COMMANDS, dict(entries["console_scripts"])
        allowed = {"fsbl", "create_avb_image", prefix[:-1]}
        for name in names:
            parts = Path(name).parts
            assert parts[0] in allowed, name
            assert not any(part in {"tests", "test_artifacts", "sources", ".venv", "build"} for part in parts), name
            assert name.endswith((".py", "/UPSTREAM")) or name.startswith(prefix), name
            data = archive.read(name)
            for marker in (
                b"BEGIN PRIVATE KEY",
                b"BEGIN RSA PRIVATE KEY",
                b"BEGIN EC PRIVATE KEY",
                b"BEGIN ENCRYPTED PRIVATE KEY",
            ):
                assert marker not in data, f"Private key content in {name}"
        vendor = archive.read("create_avb_image/_vendor/avbtool.py")
        inspect_license_contents(
            archive.read(prefix + "licenses/LICENSE"),
            archive.read(prefix + "licenses/create_avb_image/_vendor/LICENSE"),
            vendor,
        )
        provenance = archive.read("create_avb_image/_vendor/UPSTREAM").decode()
        assert f"Patched SHA-256: {hashlib.sha256(vendor).hexdigest()}" in provenance, provenance


def inspect_sdist(sdist: Path, wheel: Path) -> None:
    with tarfile.open(sdist, "r:gz") as archive, zipfile.ZipFile(wheel) as built:
        names = archive.getnames()
        (prefix,) = {name.split("/")[0] for name in names}

        def content(name):
            stream = archive.extractfile(prefix + "/" + name)
            assert stream is not None, name
            return stream.read()

        metadata = email.message_from_bytes(content("PKG-INFO"))
        inspect_license_metadata(metadata)
        (wheel_metadata,) = [name for name in built.namelist() if name.endswith(".dist-info/METADATA")]
        assert metadata["Version"] == email.message_from_bytes(built.read(wheel_metadata))["Version"], metadata
        wheel_prefix = wheel_metadata.removesuffix("METADATA")
        for name in LICENSE_FILES:
            assert content(name) == built.read(wheel_prefix + "licenses/" + name), name
        vendor = content("create_avb_image/_vendor/avbtool.py")
        assert vendor == built.read("create_avb_image/_vendor/avbtool.py"), "AVB source differs"
        assert content("create_avb_image/_vendor/UPSTREAM") == built.read("create_avb_image/_vendor/UPSTREAM")
        inspect_license_contents(content("LICENSE"), content("create_avb_image/_vendor/LICENSE"), vendor)


def smoke(wheel: Path, root: Path) -> None:
    repository = Path(__file__).resolve().parents[2]
    environment = os.environ.copy()
    for name in ("PYTHONPATH", "PYTHONHOME", "VIRTUAL_ENV", "UV_PROJECT_ENVIRONMENT"):
        environment.pop(name, None)
    home = root / "home"
    home.mkdir()
    environment["HOME"] = str(home)
    temporary = root / "system-temporary"
    temporary.mkdir()
    environment["TMPDIR"] = str(temporary)
    environment["UV_DEFAULT_INDEX"] = "https://pypi.org/simple"
    uvx = shutil.which("uvx")
    assert uvx, "uvx not found on PATH"
    installed = [uvx, "--python", sys.executable, "--no-python-downloads", "--from", str(wheel)]

    def run(*args, ok=True, stdin=None, timeout=120) -> bytes:
        result = subprocess.run(
            list(map(str, args)),
            cwd=root,
            env=environment,
            input=stdin,
            capture_output=True,
            timeout=timeout,
        )
        assert (result.returncode == 0) == ok, (
            f"{args}: exit {result.returncode}\n"
            f"stdout:\n{result.stdout.decode(errors='replace')}\n"
            f"stderr:\n{result.stderr.decode(errors='replace')}"
        )
        if not ok and "verify_avb_image" in args:
            assert b"Traceback (most recent call last)" not in result.stderr, result.stderr.decode(errors="replace")
        return result.stdout

    def cli(*args, **kwargs) -> bytes:
        return run(*installed, *args, **kwargs)

    version = run("openssl", "version").decode().strip()
    assert version.startswith("OpenSSL 3."), version
    print(f"{platform.system()} {platform.machine()}; Python {platform.python_version()}; {version}", flush=True)
    cli("python", "--version", timeout=600)
    installation = """
import importlib.metadata
import importlib.util
import json
from pathlib import Path
import sys
import sysconfig

commands, excluded_commands, excluded_modules, excluded_dependencies, runtime = json.loads(sys.argv[1])
distribution = importlib.metadata.distribution("proto-distro-security-tools")
entries = {entry.name: entry.value for entry in distribution.entry_points if entry.group == "console_scripts"}
assert entries == commands, entries
requirements = {item for item in distribution.requires if "extra ==" not in item}
assert requirements == set(runtime), requirements
installed = {item.metadata["Name"].lower().replace("_", "-") for item in importlib.metadata.distributions()}
assert not installed.intersection(excluded_dependencies), installed
scripts = Path(sysconfig.get_path("scripts"))
for name in excluded_commands:
    assert not (scripts / name).exists(), name
for name in excluded_modules:
    assert importlib.util.find_spec(name) is None, name
for name in ("fsbl", "create_avb_image"):
    origin = Path(importlib.util.find_spec(name).origin).resolve()
    assert origin.is_relative_to(Path(sys.prefix).resolve()), origin
"""
    cli(
        "python",
        "-I",
        "-c",
        installation,
        json.dumps([COMMANDS, EXCLUDED_COMMANDS, EXCLUDED_MODULES, EXCLUDED_DEPENDENCIES, sorted(RUNTIME_DEPENDENCIES)]),
    )
    for command in COMMANDS:
        cli(command, "--help")

    for key in ("a", "b"):
        run(
            "openssl",
            "genpkey",
            "-algorithm",
            "RSA",
            "-pkeyopt",
            "rsa_keygen_bits:2048",
            "-out",
            root / f"{key}.priv.pem",
        )
        run("openssl", "pkey", "-in", root / f"{key}.priv.pem", "-pubout", "-out", root / f"{key}.pub.pem")
    private = root / "a.priv.pem"
    public = root / "a.pub.pem"
    avbkey = root / "a.avbpubkey"
    cli("avbtool", "extract_public_key", "--key", private, "--output", avbkey)
    image = root / "image with spaces.ext4"
    payload = bytes(range(256)) * 256
    image.write_bytes(payload)
    neighbor = root / "rootfs.avb"
    sentinel = b"previous user-owned content"
    neighbor.write_bytes(sentinel)
    legacy_outputs = [image.with_name(image.name + suffix) for suffix in (".avb", ".avb.footer")]
    scratch = root / "reconstruction"
    scratch.mkdir()
    output = scratch / "rootfs.avb"
    body = image.with_name(image.name + ".avb.body")
    tail = image.with_name(image.name + ".avb.tail")
    partition_size = 1073741824

    def options(source=image, signing=private, verifying=public, metadata=avbkey):
        return ["--rootfs-image", source, "--privkey", signing, "--pubkey", verifying, "--avbpubkey", metadata]

    def digest(path):
        with path.open("rb") as stream:
            return hashlib.file_digest(stream, "sha256").digest()

    protected = {path: digest(path) for path in (image, private, public, avbkey, neighbor)}

    def intact():
        assert {path: digest(path) for path in protected} == protected, "Generation modified an input"
        assert not list(root.rglob(".create-avb-image-*")), "Temporary image directory leaked"
        assert not list(root.rglob(".verify-avb-image-*")), "Temporary verification directory leaked"

    def verify(source=image, body_image=body, tail_image=tail, key=public, ok=True):
        inputs = {path: digest(path) for path in (source, body_image, tail_image, key, private, avbkey, neighbor)}
        result = cli(
            "verify_avb_image",
            "--rootfs-image",
            source,
            "--body",
            body_image,
            "--tail",
            tail_image,
            "--pubkey",
            key,
            ok=ok,
            timeout=30,
        )
        assert {path: digest(path) for path in inputs} == inputs, "Verification modified an input"
        intact()
        return result

    # Decode only through the installed wheel, never the source checkout.
    decode_footer = """
import json, sys
from create_avb_image._vendor import avbtool
with open(sys.argv[1], 'rb') as stream:
    stream.seek(-avbtool.AvbFooter.SIZE, 2)
    footer = avbtool.AvbFooter(stream.read())
print(json.dumps([footer.original_image_size, footer.vbmeta_offset, footer.vbmeta_size]))
"""

    def compare_range(left, right, count):
        while count:
            size = min(count, 1024 * 1024)
            expected = left.read(size)
            actual = right.read(size)
            assert len(expected) == size and actual == expected, "AVB byte range differs"
            count -= size

    def round_trip(source, signed, body_image, tail_image):
        original, vbmeta_offset, vbmeta_size = json.loads(cli("python", "-c", decode_footer, tail_image))
        assert original == source.stat().st_size
        body_end = (vbmeta_offset + vbmeta_size + 511) // 512 * 512
        body_size = body_end - original
        assert body_image.stat().st_size == body_size
        terminal = tail_image.read_bytes()
        assert len(terminal) == 512 and terminal[:448] == bytes(448) and terminal[448:452] == b"AVBf"
        with signed.open("wb") as stream:
            for path in (source, body_image):
                with path.open("rb") as part:
                    shutil.copyfileobj(part, stream, 1024 * 1024)
            stream.seek(partition_size - 512)
            stream.write(terminal)
        assert signed.stat().st_size == partition_size
        with signed.open("rb") as full, source.open("rb") as plain, body_image.open("rb") as metadata:
            compare_range(full, plain, original)
            compare_range(full, metadata, body_size)
            full.seek(vbmeta_offset + vbmeta_size)
            assert full.read(body_end - vbmeta_offset - vbmeta_size) == bytes(body_end - vbmeta_offset - vbmeta_size)
            full.seek(-512, 2)
            assert full.read() == terminal
        cli("avbtool", "verify_image", "--image", signed, "--key", public)
        verify(source, body_image, tail_image)
        return original, vbmeta_offset, vbmeta_size

    # Fresh generation publishes only body/tail, printing their paths in order.
    generated = cli("create_avb_image", *options())
    assert generated.decode().splitlines()[-2:] == [str(body), str(tail)]
    assert not any(path.exists() for path in legacy_outputs)
    intact()
    # Replacement preserves unrelated old full/compact artifacts and read-only raw input.
    for path in (*legacy_outputs, body, tail):
        path.write_bytes(sentinel)
    image.chmod(0o444)
    try:
        generated = cli("create_avb_image", *options())
    finally:
        image.chmod(0o644)
    assert generated.decode().splitlines()[-2:] == [str(body), str(tail)]
    assert all(path.read_bytes() == sentinel for path in legacy_outputs)
    original, vbmeta_offset, vbmeta_size = round_trip(image, output, body, tail)
    info = cli("avbtool", "info_image", "--image", output).decode()
    fields = dict(line.strip().split(":", 1) for line in info.splitlines() if ":" in line)
    fields = {key: value.strip() for key, value in fields.items()}
    expected = {
        "Algorithm": "SHA256_RSA2048",
        "Partition Name": "rootfs",
        "Hash Algorithm": "sha256",
        "Data Block Size": "1024 bytes",
        "Hash Block Size": "1024 bytes",
        "FEC num roots": "0",
        "FEC offset": "0",
        "FEC size": "0 bytes",
    }
    for key, value in expected.items():
        assert fields.get(key) == value, info
    cmdline = cli("avbtool", "calculate_kernel_cmdline", "--image", output).decode()
    for argument in ("dm-mod.create=", "PARTUUID=$(ANDROID_SYSTEM_PARTUUID)", "root=/dev/dm-0"):
        assert argument in cmdline, cmdline

    # No full AVB, writable sibling directory, or neighboring rootfs.avb is required.
    saved_output = root / "saved-full.avb"
    output.rename(saved_output)
    verify()
    assert not output.exists()
    readonly = root / "read only inputs"
    readonly.mkdir()
    readonly_inputs = [readonly / name for name in ("plain image.ext4", "body.bin", "tail.bin", "trusted public.pem")]
    for source, destination in zip((image, body, tail, public), readonly_inputs):
        shutil.copyfile(source, destination)
        destination.chmod(0o444)
    readonly.chmod(0o555)
    try:
        verify(*readonly_inputs)
        cli("create_avb_image", *options(source=readonly_inputs[0]), ok=False)
        assert set(readonly.iterdir()) == set(readonly_inputs)
    finally:
        readonly.chmod(0o755)
        for path in readonly_inputs:
            path.chmod(0o644)
    saved_output.rename(output)

    verify(key=root / "b.pub.pem", ok=False)
    bad_source = root / "bad-source.ext4"
    for contents in (b"x" * len(payload), bytes([payload[0] ^ 1]) + payload[1:], payload[:-1]):
        bad_source.write_bytes(contents)
        verify(source=bad_source, ok=False)
    bad_source.write_bytes(bytes.fromhex("3aff26ed") + payload[4:])
    verify(source=bad_source, ok=False)

    body_bytes, tail_bytes = body.read_bytes(), tail.read_bytes()
    bad_body, bad_tail = root / "bad-body.bin", root / "bad-tail.bin"
    for offset in (0, vbmeta_offset - original + 119, len(body_bytes) - 1):
        changed = bytearray(body_bytes)
        changed[offset] ^= 1  # Hashtree, signed VBMeta rollback index, alignment padding.
        bad_body.write_bytes(changed)
        verify(body_image=bad_body, ok=False)
    for contents in (b"", body_bytes[:-1], body_bytes + bytes(512)):
        bad_body.write_bytes(contents)
        verify(body_image=bad_body, ok=False)
    for offset in (0, 448, 455, 511):
        changed = bytearray(tail_bytes)
        changed[offset] ^= 1  # Zero padding, magic, version, reserved bytes.
        bad_tail.write_bytes(changed)
        verify(tail_image=bad_tail, ok=False)
    for contents in (b"", tail_bytes[448:], tail_bytes[:-1], tail_bytes + b"garbage"):
        bad_tail.write_bytes(contents)
        verify(tail_image=bad_tail, ok=False)
    # Old compact input has no fallback.
    bad_body.write_bytes(body_bytes[: vbmeta_offset + vbmeta_size - original] + tail_bytes[448:])
    verify(body_image=bad_body, ok=False)

    # Preserve well-framed descriptors when patching profile fields. Empty digest
    # bytes become salt so rejection cannot be attributed to malformed framing.
    patch_metadata = """
import sys
from pathlib import Path
from create_avb_image._vendor import avbtool
source_body, source_tail, destination_body, destination_tail, kind = map(str, sys.argv[1:])
data = bytearray(Path(source_body).read_bytes())
terminal = bytearray(Path(source_tail).read_bytes())
footer = avbtool.AvbFooter(terminal[-64:])
offset = footer.vbmeta_offset - footer.original_image_size
header = avbtool.AvbVBMetaHeader(data[offset:offset + 256])
if kind == 'footer-range':
    footer.vbmeta_offset = 1 << 63
    terminal[-64:] = footer.encode()
elif kind == 'header-range':
    header.descriptors_size = 1 << 63
    data[offset:offset + 256] = header.encode()
elif kind.startswith('vbmeta='):
    vbmeta = Path(kind.split('=', 1)[1]).read_bytes()
    footer.vbmeta_size = len(vbmeta)
    body_end = avbtool.round_to_multiple(footer.vbmeta_offset + len(vbmeta), 512)
    data = data[:offset] + vbmeta + bytes(body_end - footer.vbmeta_offset - len(vbmeta))
    terminal[-64:] = footer.encode()
else:
    start = offset + 256 + header.authentication_data_block_size + header.descriptors_offset
    end = start + header.descriptors_size
    descriptors = avbtool.parse_descriptors(data[start:end])
    tree = next(item for item in descriptors if isinstance(item, avbtool.AvbHashtreeDescriptor))
    if kind == 'empty-digest':
        tree.salt += tree.root_digest
        tree.root_digest = b''
    elif kind == 'partition':
        tree.partition_name = 'system'
    elif kind == 'block-size':
        tree.data_block_size = 4096
    elif kind == 'hash-algorithm':
        tree.hash_algorithm = 'shake_128'
    elif kind == 'fec':
        tree.fec_num_roots = 2
        tree.fec_offset = tree.tree_offset + tree.tree_size
        tree.fec_size = 1024
    elif kind == 'oversized-image':
        tree.image_size = 1 << 63
    else:
        raise AssertionError(kind)
    encoded = b''.join(item.encode() for item in descriptors)
    assert len(encoded) == end - start
    data[start:end] = encoded
Path(destination_body).write_bytes(data)
Path(destination_tail).write_bytes(terminal)
"""
    for kind in (
        "footer-range",
        "header-range",
        "oversized-image",
        "empty-digest",
        "partition",
        "block-size",
        "hash-algorithm",
        "fec",
    ):
        cli("python", "-c", patch_metadata, body, tail, bad_body, bad_tail, kind)
        verify(body_image=bad_body, tail_image=bad_tail, ok=False)

    # Trusted signatures must also carry exactly our dm-init boot profile.
    with tempfile.TemporaryDirectory(dir=root) as directory:
        signed = Path(directory) / "rootfs.avb"
        dm_init = ["--setup_as_rootfs_for_dm_init", "--dm_init_device", "PARTUUID=$(ANDROID_SYSTEM_PARTUUID)"]
        for extra in (
            [],  # Missing dm-init descriptors.
            ["--setup_as_rootfs_for_dm_init", "--dm_init_device", "/dev/wrong-root"],
            [*dm_init, "--kernel_cmdline", "init=/unexpected-init"],
            [*dm_init, "--prop", "unexpected:value"],
        ):
            signed.write_bytes(payload)
            cli(
                "avbtool",
                "add_hashtree_footer",
                "--image",
                signed,
                "--partition_name",
                "rootfs",
                "--partition_size",
                str(partition_size),
                "--block_size",
                "1024",
                "--hash_algorithm",
                "sha256",
                "--algorithm",
                "SHA256_RSA2048",
                "--key",
                private,
                "--do_not_generate_fec",
                *extra,
            )
            cli("avbtool", "verify_image", "--image", signed, "--key", public)
            raw_size, offset, size = json.loads(cli("python", "-c", decode_footer, signed))
            end = (offset + size + 511) // 512 * 512
            with signed.open("rb") as stream:
                stream.seek(raw_size)
                bad_body.write_bytes(stream.read(end - raw_size))
                stream.seek(-512, 2)
                bad_tail.write_bytes(stream.read())
            verify(body_image=bad_body, tail_image=bad_tail, ok=False)

    # Real signatures do not suffice without a matching, enabled rootfs hashtree.
    with tempfile.TemporaryDirectory(dir=root) as directory:
        fixtures = Path(directory)
        (fixtures / "rootfs.avb").symlink_to(output)
        for name, extra in (
            ("no-hashtree", []),
            ("disabled-hashtree", ["--include_descriptors_from_image", output, "--flags", "1"]),
            ("disabled-verification", ["--include_descriptors_from_image", output, "--flags", "2"]),
        ):
            vbmeta = fixtures / (name + ".avb")
            cli(
                "avbtool",
                "make_vbmeta_image",
                "--algorithm",
                "SHA256_RSA2048",
                "--key",
                private,
                "--output",
                vbmeta,
                *extra,
            )
            cli("avbtool", "verify_image", "--image", vbmeta, "--key", public)
            cli("python", "-c", patch_metadata, body, tail, bad_body, bad_tail, "vbmeta=" + str(vbmeta))
            verify(body_image=bad_body, tail_image=bad_tail, ok=False)

        # A signed image is not a plain input: avbtool must not silently strip it.
        signed_input_outputs = [output.with_name(output.name + suffix) for suffix in (".avb.body", ".avb.tail")]
        for destination in signed_input_outputs:
            destination.write_bytes(sentinel)
        before = digest(output)
        cli("create_avb_image", *options(source=output), ok=False)
        assert digest(output) == before
        assert all(destination.read_bytes() == sentinel for destination in signed_input_outputs)
        intact()

        cli("avbtool", "verify_image", "--image", fixtures / "rootfs.avb", "--key", public)
        cli("avbtool", "verify_image", "--image", fixtures / "rootfs.avb", "--key", root / "b.pub.pem", ok=False)
        with output.open("r+b") as stream:
            stream.write(bytes([payload[0] ^ 1]))
        cli("avbtool", "verify_image", "--image", fixtures / "rootfs.avb", "--key", public, ok=False)
    intact()

    def old_outputs():
        assert all(path.read_bytes() == sentinel for path in (*legacy_outputs, body, tail))
        intact()

    for path in (body, tail):
        path.write_bytes(sentinel)
    cli("create_avb_image", *options(verifying=root / "b.pub.pem"), ok=False)
    old_outputs()
    for argument in ("source", "signing", "verifying", "metadata"):
        cli("create_avb_image", *options(**{argument: root / "missing"}), ok=False)
        old_outputs()
    no_openssl = 'import os, runpy; os.environ["PATH"] = ""; runpy.run_module("create_avb_image.create_avb_image", run_name="__main__")'
    cli("python", "-c", no_openssl, *options(), ok=False)
    old_outputs()
    cli("python", "-c", no_openssl, "--help")
    verify_no_openssl = (
        'import os; os.environ["PATH"] = ""; from create_avb_image.create_avb_image import verify_main; verify_main()'
    )
    cli("python", "-c", verify_no_openssl, "--help")
    cli(
        "python",
        "-c",
        verify_no_openssl,
        "--rootfs-image",
        image,
        "--body",
        body,
        "--tail",
        tail,
        "--pubkey",
        public,
        ok=False,
    )
    old_outputs()

    # Reject Android sparse containers and images exceeding the fixed partition.
    malformed = root / "malformed.ext4"
    malformed.write_bytes(bytes.fromhex("3aff26ed") + bytes(24))
    oversized = root / "oversized.ext4"
    with oversized.open("wb") as stream:
        stream.truncate(partition_size + 1024)
    for invalid in (malformed, oversized):
        before = digest(invalid)
        previous = [invalid.with_name(invalid.name + suffix) for suffix in (".avb.body", ".avb.tail")]
        for destination in previous:
            destination.write_bytes(sentinel)
        cli("create_avb_image", *options(source=invalid), ok=False)
        assert all(destination.read_bytes() == sentinel for destination in previous)
        assert digest(invalid) == before
        intact()
    oversized.unlink()

    # Neither output may alias an input or the other output.
    for destination, other in ((body, tail), (tail, body)):
        for source in (image, private, public, avbkey):
            before = digest(source)
            destination.unlink()
            os.link(source, destination)
            cli("create_avb_image", *options(), ok=False)
            assert digest(source) == before and digest(destination) == before
            assert other.read_bytes() == sentinel
            intact()
        destination.unlink()
        for target in (neighbor, root / "absent-target", other):
            destination.symlink_to(target)
            cli("create_avb_image", *options(), ok=False)
            assert destination.is_symlink() and other.read_bytes() == sentinel
            assert not (root / "absent-target").exists()
            intact()
            destination.unlink()
        destination.mkdir()
        cli("create_avb_image", *options(), ok=False)
        assert destination.is_dir() and other.read_bytes() == sentinel
        destination.rmdir()
        destination.write_bytes(sentinel)
        other.unlink()
        os.link(destination, other)
        cli("create_avb_image", *options(), ok=False)
        old_outputs()
        other.unlink()
        other.write_bytes(sentinel)
    # Old artifacts are not outputs; replacing their hardlinks leaves them untouched.
    for old, destination in zip(legacy_outputs, (body, tail)):
        destination.unlink()
        os.link(old, destination)
    cli("create_avb_image", *options())
    assert all(path.read_bytes() == sentinel for path in legacy_outputs)
    verify()
    body.unlink()
    tail.unlink()
    output.unlink()

    # Read symlink inputs, but publish only beside the caller path.
    linked_dir = root / "linked"
    linked_dir.mkdir()
    linked = linked_dir / "linked.ext4"
    linked.symlink_to(image)
    cli("create_avb_image", *options(source=linked))
    linked_body = linked.with_name(linked.name + ".avb.body")
    linked_tail = linked.with_name(linked.name + ".avb.tail")
    assert not linked.with_name(linked.name + ".avb").exists()
    assert not linked.with_name(linked.name + ".avb.footer").exists()
    round_trip(linked, output, linked_body, linked_tail)
    assert not body.exists() and not tail.exists()
    assert all(path.read_bytes() == sentinel for path in legacy_outputs)
    intact()

    # An unaligned raw input retains its pre-tree alignment byte.
    unaligned = root / "unaligned.ext4"
    unaligned.write_bytes(payload[:-1])
    cli("create_avb_image", *options(source=unaligned))
    unaligned_body = unaligned.with_name(unaligned.name + ".avb.body")
    unaligned_tail = unaligned.with_name(unaligned.name + ".avb.tail")
    assert not unaligned.with_name(unaligned.name + ".avb").exists()
    assert not unaligned.with_name(unaligned.name + ".avb.footer").exists()
    assert round_trip(unaligned, output, unaligned_body, unaligned_tail)[0] == 65535
    assert unaligned_body.read_bytes()[0] == 0
    assert unaligned.read_bytes() == payload[:-1]
    intact()

    keys = repository / "sources/meta-miner/recipes-dev/miner-signing-keys/files/stm32mp13_dev_keys"
    public_keys = sorted(keys.glob("publicKey*.pem"))
    assert [key.name for key in public_keys] == ["publicKey00.pem", "publicKey01.pem"], public_keys
    public_options = [item for key in public_keys for item in ("--pk", key)]
    signed = root / "signed.stm32"
    # C3 policy requires key index 1; preserve the signer's anti-rollback checks.
    cli(
        "fsbl_signer",
        "sign",
        "--product",
        "C3",
        "-i",
        repository / "python/fsbl/test_artifacts/tf-a-stm32mp131f-c3p1-emmc.stm32",
        "-o",
        signed,
        "--sk",
        keys / "privateKey01.pem",
        "--pwd",
        "qwert2",
        *public_options,
    )
    cli("fsbl_signer", "verify", "--product", "C3", "-i", signed, *public_options)

    print(
        "PASS: four installed commands, excluded packages/dependencies absent, AVB body/tail layout/reconstruction/trust boundaries, paired publication/protection, FSBL"
    )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--wheel", required=True, type=Path)
    parser.add_argument("--sdist", required=True, type=Path)
    args = parser.parse_args()
    wheel = args.wheel.resolve(strict=True)
    if not wheel.is_file() or wheel.suffix != ".whl":
        parser.error("--wheel must be an existing wheel file")
    inspect_wheel(wheel)
    sdist = args.sdist.resolve(strict=True)
    if not sdist.is_file() or not sdist.name.endswith(".tar.gz"):
        parser.error("--sdist must be an existing .tar.gz source distribution")
    inspect_sdist(sdist, wheel)
    with tempfile.TemporaryDirectory(prefix="proto-wheel-smoke-") as directory:
        smoke(wheel, Path(directory).resolve())


if __name__ == "__main__":
    main()

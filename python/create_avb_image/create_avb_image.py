"""Create and verify a signed rootfs image using the bundled AVB implementation."""

import os
import runpy
import shutil
import struct
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import BinaryIO

import click

from create_avb_image._vendor import avbtool

PARTITION_SIZE = 1073741824
COPY_CHUNK_SIZE = 1024 * 1024


def _read_footer(stream: BinaryIO) -> avbtool.AvbFooter:
    if stream.seek(0, os.SEEK_END) < avbtool.AvbFooter.SIZE:
        raise ValueError("Image is shorter than an AVB footer")
    stream.seek(-avbtool.AvbFooter.SIZE, os.SEEK_END)
    data = stream.read(avbtool.AvbFooter.SIZE)
    footer = avbtool.AvbFooter(data)
    if (footer.version_major, footer.version_minor) != (1, 0) or footer.encode() != data:
        raise ValueError("Unsupported or noncanonical AVB footer")
    return footer


def _copy_range(source: BinaryIO, destination: BinaryIO, length: int) -> None:
    while length > 0:
        data = source.read(min(length, COPY_CHUNK_SIZE))
        if not data:
            raise ValueError("Unexpected EOF copying AVB image")
        destination.write(data)
        length -= len(data)


def _validate_image(image: Path, rootfs_size: int) -> avbtool.AvbFooter:
    with image.open("rb") as stream:
        footer = _read_footer(stream)
        original, offset, size = footer.original_image_size, footer.vbmeta_offset, footer.vbmeta_size
        if not (
            0 < original == rootfs_size < PARTITION_SIZE
            and avbtool.AvbVBMetaHeader.SIZE <= size <= avbtool.Avb.MAX_VBMETA_SIZE
            and offset >= original
            and offset % 4096 == 0
            and offset + size <= PARTITION_SIZE - 4096
            and stream.tell() == PARTITION_SIZE
        ):
            raise ValueError("Invalid AVB image ranges or rootfs size")
        stream.seek(offset)
        vbmeta = stream.read(size)
        if len(vbmeta) != size:
            raise ValueError("Truncated VBMeta")
        header = avbtool.AvbVBMetaHeader(vbmeta[: avbtool.AvbVBMetaHeader.SIZE])
        algorithm = avbtool.ALGORITHMS["SHA256_RSA2048"]
        authentication = header.authentication_data_block_size
        auxiliary = header.auxiliary_data_block_size
        if not (
            header.required_libavb_version_major == 1
            and header.required_libavb_version_minor <= avbtool.AVB_VERSION_MINOR
            and header.algorithm_type == algorithm.algorithm_type
            and header.flags == 0
            and size == avbtool.AvbVBMetaHeader.SIZE + authentication + auxiliary
            and authentication % 64 == 0
            and auxiliary % 64 == 0
            and header.hash_size == algorithm.hash_num_bytes
            and header.signature_size == algorithm.signature_num_bytes
            and header.public_key_size == algorithm.public_key_num_bytes
        ):
            raise ValueError("Unsupported VBMeta profile or block sizes")
        for block_size, ranges in (
            (
                authentication,
                [(header.hash_offset, header.hash_size), (header.signature_offset, header.signature_size)],
            ),
            (
                auxiliary,
                [
                    (header.public_key_offset, header.public_key_size),
                    (header.public_key_metadata_offset, header.public_key_metadata_size),
                    (header.descriptors_offset, header.descriptors_size),
                ],
            ),
        ):
            end = 0
            for start, length in sorted(ranges):
                if start + length > block_size or (length and start < end):
                    raise ValueError("Invalid or overlapping VBMeta block ranges")
                if length:
                    end = start + length
        start = avbtool.AvbVBMetaHeader.SIZE + authentication + header.descriptors_offset
        descriptors = vbmeta[start : start + header.descriptors_size]
        position = 0
        while position < len(descriptors):
            if len(descriptors) - position < avbtool.AvbDescriptor.SIZE:
                raise ValueError("Truncated AVB descriptor header")
            _, length = struct.unpack_from(avbtool.AvbDescriptor.FORMAT_STRING, descriptors, position)
            position += avbtool.AvbDescriptor.SIZE + length
            if length % 8 or position > len(descriptors):
                raise ValueError("Invalid AVB descriptor range")
        tree = None
        cmdlines = []
        try:
            parsed = avbtool.parse_descriptors(descriptors)
        except TypeError as error:
            # Upstream hash decoding calls digest() without a length, even for SHAKE.
            raise ValueError(f"Invalid AVB descriptor: {error}") from error
        for descriptor in parsed:
            if isinstance(descriptor, avbtool.AvbHashtreeDescriptor) and tree is None:
                tree = descriptor
            elif isinstance(descriptor, avbtool.AvbKernelCmdlineDescriptor):
                cmdlines.append((descriptor.flags, descriptor.kernel_cmdline))
            else:
                raise ValueError("Unsupported or duplicate AVB descriptor")
        data_size = avbtool.round_to_multiple(original, 4096)
        _, tree_size = avbtool.calc_hash_level_offsets(data_size, 1024, 32)
        if not (
            tree is not None
            and tree.partition_name == "rootfs"
            and tree.dm_verity_version == 1
            and tree.hash_algorithm == "sha256"
            and tree.data_block_size == tree.hash_block_size == 1024
            and tree.flags == 0
            and len(tree.root_digest) == 32
            and tree.fec_num_roots == tree.fec_offset == tree.fec_size == 0
            and tree.image_size == tree.tree_offset == data_size
            and tree.tree_size == tree_size
            and offset == data_size + avbtool.round_to_multiple(tree_size, 4096)
        ):
            raise ValueError("Missing or unsupported rootfs hashtree profile")
        expected_cmdlines = avbtool.Avb()._get_cmdline_descriptors_for_dm_init(
            tree, "PARTUUID=$(ANDROID_SYSTEM_PARTUUID)"
        )
        if sorted(cmdlines) != sorted((item.flags, item.kernel_cmdline) for item in expected_cmdlines):
            raise ValueError("Missing or unsupported rootfs dm-init profile")
        for start, end in (
            (original, data_size),
            (data_size + tree_size, offset),
            (offset + size, avbtool.round_to_multiple(offset + size, 512)),
            (PARTITION_SIZE - 512, PARTITION_SIZE - avbtool.AvbFooter.SIZE),
        ):
            stream.seek(start)
            if stream.read(end - start) != bytes(end - start):
                raise ValueError("Nonzero AVB alignment padding")
        return footer


def _check_inputs(*paths: Path) -> None:
    if shutil.which("openssl") is None:
        raise click.ClickException("OpenSSL executable not found on PATH")
    for path in paths:
        if not path.is_file() or not os.access(path, os.R_OK):
            raise ValueError(f"Input must be a readable regular file: {path}")
    with paths[0].open("rb") as stream:
        if stream.read(4) == struct.pack("<I", 0xED26FF3A):
            raise ValueError(f"Android sparse-container images are not supported: {paths[0]}")


def _verify_image(rootfs_image: Path, body: Path, tail: Path, pubkey: Path) -> None:
    rootfs_image, body, tail, pubkey = (path.resolve() for path in (rootfs_image, body, tail, pubkey))
    _check_inputs(rootfs_image, body, tail, pubkey)
    original = rootfs_image.stat().st_size
    body_size = body.stat().st_size
    if not (
        0 < original < PARTITION_SIZE
        and body_size > 0
        and (original + body_size) % 512 == 0
        and original + body_size <= PARTITION_SIZE - 512
        and tail.stat().st_size == 512
    ):
        raise ValueError("Invalid AVB body/tail sizes")
    with tail.open("rb") as stream:
        footer = _read_footer(stream)
    if footer.original_image_size != original or original + body_size != avbtool.round_to_multiple(
        footer.vbmeta_offset + footer.vbmeta_size, 512
    ):
        raise ValueError("AVB body/tail ranges do not match the raw rootfs")
    with tempfile.TemporaryDirectory(prefix=".verify-avb-image-") as directory:
        image = Path(directory) / "rootfs.avb"
        with image.open("wb") as destination:
            for path in (rootfs_image, body):
                with path.open("rb") as source:
                    _copy_range(source, destination, path.stat().st_size)
            destination.seek(PARTITION_SIZE - 512)
            with tail.open("rb") as source:
                _copy_range(source, destination, 512)
        _validate_image(image, original)
        subprocess.run(
            [
                sys.executable,
                "-m",
                "create_avb_image._vendor.avbtool",
                "verify_image",
                "--image",
                str(image),
                "--key",
                str(pubkey),
            ],
            check=True,
        )


@click.command(name="verify_avb_image")
@click.option(
    "--rootfs-image", required=True, type=click.Path(exists=True, dir_okay=False, readable=True, path_type=Path)
)
@click.option("--body", required=True, type=click.Path(exists=True, dir_okay=False, readable=True, path_type=Path))
@click.option("--tail", required=True, type=click.Path(exists=True, dir_okay=False, readable=True, path_type=Path))
@click.option("--pubkey", required=True, type=click.Path(exists=True, dir_okay=False, readable=True, path_type=Path))
def verify_main(rootfs_image: Path, body: Path, tail: Path, pubkey: Path) -> None:
    """Verify raw rootfs plus AVB body/tail against a trusted public PEM.

    Requires OpenSSL 3.x. Reconstructs a private sparse 1 GiB image, placing body
    after the raw rootfs and the 512-byte tail at partition_size - 512.
    The signature authenticates the padded rootfs and signed VBMeta, not the
    terminal footer or original unpadded length. No hardware boot or OTA guarantee.
    """
    try:
        _verify_image(rootfs_image, body, tail, pubkey)
        click.echo(f"Verified AVB image matches rootfs: {rootfs_image.resolve()}")
    except (OSError, subprocess.CalledProcessError, struct.error, LookupError, ValueError, avbtool.AvbError) as error:
        raise click.ClickException(str(error)) from error


@click.command()
@click.option(
    "--rootfs-image",
    required=True,
    type=click.Path(exists=True, file_okay=True, dir_okay=False, readable=True, path_type=Path),
)
@click.option(
    "--privkey",
    required=True,
    type=click.Path(exists=True, file_okay=True, dir_okay=False, readable=True, path_type=Path),
)
@click.option(
    "--pubkey",
    required=True,
    type=click.Path(exists=True, file_okay=True, dir_okay=False, readable=True, path_type=Path),
)
@click.option(
    "--avbpubkey",
    required=True,
    type=click.Path(exists=True, file_okay=True, dir_okay=False, readable=True, path_type=Path),
)
def main(rootfs_image: Path, privkey: Path, pubkey: Path, avbpubkey: Path) -> None:
    """Write only ROOTFS_IMAGE.avb.body and ROOTFS_IMAGE.avb.tail.

    Requires OpenSSL 3.x and a trusted public PEM. Body contains the hashtree,
    VBMeta and alignment padding; tail is the final 512-byte sector. Place body
    after the unchanged raw rootfs and tail at 1073741312 in a 1 GiB partition.
    The signature authenticates the padded rootfs and signed VBMeta, not the
    terminal footer or original unpadded length. No hardware boot or OTA guarantee.

    Generation and verification failures preserve both previous outputs.
    Publication validates both before replacing either; two renames are not
    atomic as a pair. Existing .avb and .avb.footer files remain untouched.
    """
    try:
        # Preserve the caller's filename and directory, even for symlink inputs.
        outputs = [
            rootfs_image.absolute().with_name(rootfs_image.name + suffix) for suffix in (".avb.body", ".avb.tail")
        ]
        inputs = [path.resolve() for path in (rootfs_image, privkey, pubkey, avbpubkey)]
        rootfs_image, privkey, pubkey, avbpubkey = inputs
        _check_inputs(*inputs)
        rootfs_size = rootfs_image.stat().st_size
        for output in outputs:
            if output.is_symlink() or (output.exists() and not output.is_file()):
                raise click.ClickException(f"Output must be a regular file, not a symlink: {output}")
            if any(output.resolve() == path or (output.exists() and os.path.samefile(output, path)) for path in inputs):
                raise click.ClickException(f"Output aliases an input: {output}")
        if all(output.exists() for output in outputs) and os.path.samefile(*outputs):
            raise click.ClickException("AVB body and tail outputs alias each other")

        with tempfile.TemporaryDirectory(prefix=".create-avb-image-", dir=outputs[0].parent) as directory:
            image = Path(directory) / "rootfs.avb"
            shutil.copyfile(rootfs_image, image)
            command = [sys.executable, "-m", "create_avb_image._vendor.avbtool"]
            subprocess.run(
                command
                + [
                    "add_hashtree_footer",
                    "--image",
                    str(image),
                    "--partition_name",
                    "rootfs",
                    "--partition_size",
                    str(PARTITION_SIZE),
                    "--block_size",
                    "1024",
                    "--hash_algorithm",
                    "sha256",
                    "--key",
                    str(privkey),
                    "--algorithm",
                    "SHA256_RSA2048",
                    "--public_key_metadata",
                    str(avbpubkey),
                    "--setup_as_rootfs_for_dm_init",
                    "--dm_init_device",
                    "PARTUUID=$(ANDROID_SYSTEM_PARTUUID)",
                    "--do_not_generate_fec",
                ],
                check=True,
            )
            subprocess.run(command + ["verify_image", "--image", str(image), "--key", str(pubkey)], check=True)
            footer = _validate_image(image, rootfs_size)
            body_end = avbtool.round_to_multiple(footer.vbmeta_offset + footer.vbmeta_size, 512)
            staged = [Path(directory) / name for name in ("rootfs.avb.body", "rootfs.avb.tail")]
            with image.open("rb") as source:
                for path, start, length in (
                    (staged[0], rootfs_size, body_end - rootfs_size),
                    (staged[1], PARTITION_SIZE - 512, 512),
                ):
                    source.seek(start)
                    with path.open("wb") as destination:
                        _copy_range(source, destination, length)
            for source, destination in zip(staged, outputs):
                os.replace(source, destination)
        for output in outputs:
            click.echo(output)
    except (OSError, subprocess.CalledProcessError, struct.error, LookupError, ValueError, avbtool.AvbError) as error:
        raise click.ClickException(str(error)) from error


def avbtool_main() -> None:
    """Expose the upstream CLI, including its exit status and invocation log."""
    runpy.run_path(avbtool.__file__, run_name="__main__")


if __name__ == "__main__":
    main()

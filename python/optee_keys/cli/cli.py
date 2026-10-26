import sys
from pathlib import Path

import click

from ..src.core import (
    compare_key_do,
    get_ta_info,
    inject_key_do,
    parse_ta_header,
    print_key_do,
    verify_ta_signature,
    verify_ta_with_pem,
)
from ..src.defaults import verify_ta_with_default_key
from ..src.tee_pager import (
    compare_pager_with_pem,
    export_key_as_pem,
    print_key_from_pager,
    replace_modulus_in_pager,
)


@click.group()
def cli() -> None:
    """Utility for managing OP-TEE public keys in ELF files.

    This module provides functionality to work with public keys embedded in OP-TEE ELF files,
    including reading, comparing, and injecting RSA public keys. It supports both reading keys
    from ELF files and PEM-formatted public key files.
    """
    pass


@cli.command()
@click.argument("elf_path", type=click.Path(exists=True))
def print_key(elf_path: str) -> None:
    """Print the public key components inside tee.elf.

    Args:
        elf_path: Path to the ELF file.

    Returns:
        None
    """
    exponent, modulus = print_key_do(Path(elf_path))
    click.echo(f"Exponent: {exponent}")
    click.echo("Modulus:")
    click.echo(modulus)


@cli.command()
@click.argument("elf_path", type=click.Path(exists=True))
@click.argument("pem_path", type=click.Path(exists=True))
def compare_key(elf_path: str, pem_path: str) -> None:
    """Compare embedded ELF key with a PEM public key.

    Args:
        elf_path: Path to the ELF file.
        pem_path: Path to the PEM public key file.

    Returns:
        None

    Raises:
        SystemExit: Exit code 0 if keys match, 1 if they don't.
    """
    if compare_key_do(Path(elf_path), Path(pem_path)):
        click.secho("Public key in ELF matches the provided PEM key.", fg="green")
        sys.exit(0)
    else:
        click.secho("Public key in ELF does NOT match the provided PEM key.", fg="red")
        sys.exit(1)


@cli.command()
@click.argument("elf_path", type=click.Path(exists=True))
@click.argument("pem_path", type=click.Path(exists=True))
@click.argument("output_path", type=click.Path())
def inject_key(elf_path: str, pem_path: str, output_path: str) -> None:
    """Inject new public key from PEM into tee.elf and write to output ELF.

    Creates a new ELF file with the public key from the PEM file replacing
    the existing key in the input ELF file.

    Args:
        elf_path: Path to the input ELF file.
        pem_path: Path to the PEM file containing the new public key.
        output_path: Path where the modified ELF file will be written.

    Returns:
        None
    """
    inject_key_do(Path(elf_path), Path(pem_path), Path(output_path))
    click.secho(f"[+] New public key injected into: {output_path}", fg="green")


@cli.command()
@click.argument("ta_path", type=click.Path(exists=True))
@click.argument("optee_elf_path", type=click.Path(exists=True))
def verify_ta(ta_path: str, optee_elf_path: str) -> None:
    """Verify a TA's signature using the public key from OP-TEE ELF.

    Args:
        ta_path: Path to the .ta file to verify.
        optee_elf_path: Path to the OP-TEE ELF containing the public key.

    Returns:
        None

    Raises:
        SystemExit: Exit code 0 if signature is valid, 1 if invalid.
    """
    if verify_ta_signature(Path(ta_path), Path(optee_elf_path)):
        click.secho("TA signature is VALID", fg="green")
        sys.exit(0)
    else:
        click.secho("TA signature is INVALID", fg="red")
        sys.exit(1)


@cli.command()
@click.argument("ta_path", type=click.Path(exists=True))
@click.argument("pem_path", type=click.Path(exists=True))
def verify_ta_with_key(ta_path: str, pem_path: str) -> None:
    """Verify a TA's signature using a PEM key file.

    Args:
        ta_path: Path to the .ta file to verify.
        pem_path: Path to the PEM key file.

    Returns:
        None

    Raises:
        SystemExit: Exit code 0 if signature is valid, 1 if invalid.
    """
    if verify_ta_with_pem(Path(ta_path), Path(pem_path)):
        click.secho("TA signature is VALID", fg="green")
        sys.exit(0)
    else:
        click.secho("TA signature is INVALID", fg="red")
        sys.exit(1)


@cli.command()
@click.argument("ta_path", type=click.Path(exists=True))
def ta_info(ta_path: str) -> None:
    """Display information about a TA file.

    Args:
        ta_path: Path to the .ta file.

    Returns:
        None

    Raises:
        SystemExit: Exit code 1 if error reading TA.
    """
    info = get_ta_info(Path(ta_path))
    if info.valid:
        click.echo(f"Image Type: {info.img_type}")
        click.echo(f"Image Size: {info.img_size} bytes")
        click.echo(f"Algorithm: {info.algorithm}")
        click.echo(f"UUID: {info.uuid}")
        click.echo(f"Hash Size: {info.hash_size} bytes")
        click.echo(f"Signature Size: {info.sig_size} bytes")
    else:
        click.secho(f"Error reading TA: {info.error}", fg="red")
        sys.exit(1)


@cli.command()
@click.argument("ta_path", type=click.Path(exists=True))
@click.argument("output_path", type=click.Path())
def strip_ta(ta_path: str, output_path: str) -> None:
    """Extract raw payload from a signed TA file.

    Strips the OP-TEE signature envelope, outputting the original
    unsigned binary that can be re-signed with a different key.

    Args:
        ta_path: Path to the signed .ta file.
        output_path: Path to write the extracted raw binary.

    Returns:
        None

    Raises:
        SystemExit: Exit code 1 if error occurs.
    """
    try:
        ta_header = parse_ta_header(Path(ta_path))
        Path(output_path).write_bytes(ta_header.image)
        click.secho(f"Extracted {ta_header.img_size} bytes to {output_path}", fg="green")
    except ValueError as e:
        click.secho(f"Error: {e}", fg="red")
        sys.exit(1)


@cli.command()
@click.argument("ta_path", type=click.Path(exists=True))
def verify_ta_with_default(ta_path: str) -> None:
    """Verify if a TA is signed with the default OP-TEE key.

    Args:
        ta_path: Path to the .ta file to verify.

    Returns:
        None

    Raises:
        FileNotFoundError: If default key is not available.
        SystemExit: Exit code 0 if signed with default key, 1 if not, 2 if default key not found.
    """
    try:
        if verify_ta_with_default_key(Path(ta_path)):
            click.secho("TA is signed with the DEFAULT OP-TEE key", fg="yellow")
            sys.exit(0)
        else:
            click.secho("TA is NOT signed with the default key", fg="green")
            sys.exit(1)
    except FileNotFoundError:
        click.secho("Default key not available", fg="red")
        sys.exit(2)


@cli.command()
@click.argument("pager_path", type=click.Path(exists=True))
@click.option("--pem", is_flag=True, help="Output as PEM format")
def print_key_pager(pager_path: str, pem: bool) -> None:
    """Print the public key from a tee-pager_v2.bin file.

    Args:
        pager_path: Path to the pager binary file.
        pem: If True, output in PEM format. Otherwise, output hex values.

    Returns:
        None
    """
    if pem:
        pem_str = export_key_as_pem(Path(pager_path))
        click.echo(pem_str)
    else:
        exponent, modulus = print_key_from_pager(Path(pager_path))
        click.echo(f"Exponent: {exponent}")
        click.echo("Modulus:")
        click.echo(modulus)


@cli.command()
@click.argument("pager_path", type=click.Path(exists=True))
@click.argument("pem_path", type=click.Path(exists=True))
def compare_key_pager(pager_path: str, pem_path: str) -> None:
    """Compare pager binary key with a PEM key.

    Args:
        pager_path: Path to the pager binary file.
        pem_path: Path to the PEM key file.

    Returns:
        None

    Raises:
        SystemExit: Exit code 0 if keys match, 1 if they don't.
    """
    if compare_pager_with_pem(Path(pager_path), Path(pem_path)):
        click.secho("Keys match!", fg="green")
        sys.exit(0)
    else:
        click.secho("Keys do NOT match", fg="red")
        sys.exit(1)


@cli.command()
@click.argument("pager_path", type=click.Path(exists=True))
@click.argument("reference_pem", type=click.Path(exists=True))
@click.argument("new_pem", type=click.Path(exists=True))
@click.option("--output", "-o", type=click.Path(), help="Output file (modifies in-place if not specified)")
def replace_modulus_pager(pager_path: str, reference_pem: str, new_pem: str, output: str | None) -> None:
    """Replace modulus in a tee-pager_v2.bin file.

    Args:
        pager_path: Path to the pager binary file.
        reference_pem: PEM file with the current key (used to find modulus).
        new_pem: PEM file with the new key (source of new modulus).
        output: Optional output file path. If not specified, modifies in-place.

    Returns:
        None
    """
    output_path = Path(output) if output else None
    replace_modulus_in_pager(Path(pager_path), Path(reference_pem), Path(new_pem), output_path)
    if output:
        click.secho(f"Modulus replaced, output: {output}", fg="green")
    else:
        click.secho("Modulus replaced in-place", fg="green")


if __name__ == "__main__":
    cli()

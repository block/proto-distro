import logging
from pathlib import Path

import click

from cot_signer.constants import (
    EMPTY_FILE_SHA256,
    FW_CONFIG_HASH_OID,
    HW_CONFIG_HASH_OID,
    NON_TRUSTED_FW_CONTENT_CERT_PK_OID,
    NON_TRUSTED_NVCOUNTER_OID,
    NON_TRUSTED_NV_COUNTER,
    NON_TRUSTED_WORLD_BOOTLOADER_HASH_OID,
    NON_TRUSTED_WORLD_PK_OID,
    NT_FW_CONTENT_CERT_NAME,
    NT_FW_KEY_CERT_NAME,
    STM32MP_CFG_CERT_NAME,
    TB_FW_CERT_NAME,
    TOS_FW_CONTENT_CERT_NAME,
    TOS_FW_KEY_CERT_NAME,
    TRUSTED_BOOT_FW_CONFIG_HASH_OID,
    TRUSTED_BOOT_FW_HASH_OID,
    TRUSTED_KEY_CERT_NAME,
    TRUSTED_NVCOUNTER_OID,
    TRUSTED_NV_COUNTER,
    TRUSTED_OS_FW_CONTENT_CERT_PK_OID,
    TRUSTED_OS_FW_EXTRA1_HASH_OID,
    TRUSTED_OS_FW_EXTRA2_HASH_OID,
    TRUSTED_OS_FW_HASH_OID,
    TRUSTED_WORLD_PK_OID,
    Environment,
    Product,
    SecurityWorld,
    SigningProvider,
)
from cot_signer.utils.certificate_manager import ChainOfTrustCertManager

logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(levelname)s - %(message)s")
logger = logging.getLogger(__name__)

"""
Chain of Trust re-signer that fixes up hashes, public keys, and signatures
Warning: Only tested on the STM32MP131f and only supports ECDSA prime256v1 with SHA256

Resources:
https://wiki.st.com/stm32mpu/wiki/TF-A_BL2_Trusted_Board_Boot#CoT
https://github.com/ARM-software/arm-trusted-firmware/blob/master/include/tools_share/tbbr_oid.h
"""

# fmt: off
class OrderedGroup(click.Group):
    def __init__(self, name=None, commands=None, **attrs):
        super(OrderedGroup, self).__init__(name, commands, **attrs)
        self.cmd_order = ['print-cert', 'sign-root-certs', 'sign-intermediate-certs',
                          'sign-leaf-certs', 'sign-leaf-certs-external', 'verify-cot']

    def list_commands(self, ctx):
        return self.cmd_order

@click.group(cls=OrderedGroup)
@click.option("--verbose", "-v", is_flag=True, default=False, help="Enable verbose logs")
def cli(verbose):
    """Chain of Trust re-signer that fixes up hashes, public keys, and signatures. Intended for production signing.
Warning: Only tested on the STM32MP131f and only supports ECDSA prime256v1 with SHA256"""

    if verbose:
        logger.setLevel(logging.DEBUG)
    else:
        logger.setLevel(logging.INFO)

@cli.command()
@click.option("--rot-key-pk", type=click.Path(exists=True, readable=True, path_type=Path), required=True, help="Root of Trust public key")
@click.option("--rot-key-sk", type=click.Path(exists=True, readable=True, path_type=Path), required=True, help="Root of Trust secret key")
@click.option("-r", "--rot-key-pwd", type=str, help="Password for the root key")
@click.option("--fw-config", type=click.Path(exists=True, readable=True), help="Firmware Config file")
@click.option("--hw-config", type=click.Path(exists=True, readable=True), help="HW Config file")
@click.option("--trusted-key-cert", type=click.Path(exists=True, readable=True), help="Trusted Key Certificate")
@click.option("--stm32mp-cfg-cert", type=click.Path(exists=True, readable=True), help="STM32MP Config Certificate")
@click.option("--tb-fw-cert", type=click.Path(exists=True, readable=True), help="Trusted Boot Firmware Certificate")
@click.option("--trusted-world-key-pk", type=click.Path(exists=True, readable=True, path_type=Path), help="Trusted World public key")
@click.option("--non-trusted-world-key-pk", type=click.Path(exists=True, readable=True, path_type=Path), help="Non Trusted World public key")
@click.option("--product", type=click.Choice(Product.members(), case_sensitive=False), required=True,
              help="Product identifier", callback=lambda ctx, param, val: Product[val])
@click.option("--out-dir", type=click.Path(file_okay=False, dir_okay=True, path_type=Path),
              help="Output directory for signed certificates (defaults to a temporary directory)")
def sign_root_certs(rot_key_pk, rot_key_sk, rot_key_pwd, fw_config, hw_config, trusted_key_cert, stm32mp_cfg_cert,
                    tb_fw_cert, trusted_world_key_pk, non_trusted_world_key_pk, product, out_dir):
    """Sign root level certificates"""
    required_params_stm32mp_crt = [fw_config, hw_config, stm32mp_cfg_cert]
    required_params_tb_fw_crt = [fw_config, hw_config, tb_fw_cert]
    required_params_trusted_key_crt = [trusted_world_key_pk, non_trusted_world_key_pk, trusted_key_cert]

    # Only prompt for password if we have any required arguments and password wasn't provided
    if any(required_params_stm32mp_crt + required_params_trusted_key_crt) and not rot_key_pwd:
        rot_key_pwd = click.prompt("Root key password", hide_input=True)

    # Check if we have STM32MP CFG cert arguments
    has_cfg = all(required_params_stm32mp_crt)
    if has_cfg:
        content = {FW_CONFIG_HASH_OID: fw_config, HW_CONFIG_HASH_OID: hw_config}
        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, stm32mp_cfg_cert,
                                              rot_key_sk, rot_key_pwd, rot_key_pk, content, out_dir)
        cot_manager.cert_signing_workflow()
    elif any(required_params_stm32mp_crt):
        # if we don't have all required args, let the user know
        raise ValueError("Missing a required argument for STM32MP CFG Cert.")

    # Check if we have TB FW cert arguments
    has_tb_fw = all(required_params_tb_fw_crt)
    if has_tb_fw:
        content = {FW_CONFIG_HASH_OID: fw_config, HW_CONFIG_HASH_OID: hw_config}
        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, tb_fw_cert,
                                              rot_key_sk, rot_key_pwd, rot_key_pk, content, out_dir)
        cot_manager.cert_signing_workflow()
    elif tb_fw_cert:
        # if we have a tb_fw_cert but not all required args, let the user know
        raise ValueError("Missing a required argument for TB FW Cert.")

    # Check if we have trusted key cert arguments
    has_trusted_key = all(required_params_trusted_key_crt)
    if has_trusted_key:
        content = {TRUSTED_WORLD_PK_OID: trusted_world_key_pk, NON_TRUSTED_WORLD_PK_OID: non_trusted_world_key_pk}
        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, trusted_key_cert,
                                              rot_key_sk, rot_key_pwd, rot_key_pk, content, out_dir)
        cot_manager.cert_signing_workflow()
    elif any(required_params_trusted_key_crt):
        # if we don't have all required args, let the user know
        raise ValueError("Missing a required argument for Trusted Key Cert.")

@cli.command()
@click.option("--trusted-world-key-pk", type=click.Path(exists=True, readable=True, path_type=Path), help="Trusted World public key")
@click.option("--trusted-world-key-sk", type=click.Path(exists=True, readable=True, path_type=Path), help="Trusted World secret key")
@click.option("--trusted-world-key-pwd", type=str, help="Password for the Trusted World key")
@click.option("--non-trusted-world-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Non Trusted World public key")
@click.option("--non-trusted-world-key-sk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Non Trusted World secret key")
@click.option("--non-trusted-world-key-pwd", type=str, help="Password for the Non Trusted World key")
@click.option("--tos-fw-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate public key")
@click.option("--nt-fw-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate public key")
@click.option("--tos-fw-key-cert", type=click.Path(exists=True, readable=True),
              help="Trusted OS Firmware Key Certificate")
@click.option("--nt-fw-key-cert", type=click.Path(exists=True, readable=True),
              help="Non Trusted OS Firmware Key Certificate")
@click.option("--product", type=click.Choice(Product.members(), case_sensitive=False), required=True,
              help="Product identifier", callback=lambda ctx, param, val: Product[val])
@click.option("--out-dir", type=click.Path(file_okay=False, dir_okay=True, path_type=Path),
              help="Output directory for signed certificates (defaults to a temporary directory)")
def sign_intermediate_certs(trusted_world_key_pk, trusted_world_key_sk, trusted_world_key_pwd,
                            non_trusted_world_key_pk, non_trusted_world_key_sk, non_trusted_world_key_pwd,
                            tos_fw_key_pk, nt_fw_key_pk, tos_fw_key_cert, nt_fw_key_cert, product, out_dir):
    """Sign intermediate level certificates"""

    required_params_trusted = [trusted_world_key_pk, trusted_world_key_sk, tos_fw_key_pk, tos_fw_key_cert]
    required_params_non_trusted = [non_trusted_world_key_pk, non_trusted_world_key_sk, nt_fw_key_pk, nt_fw_key_cert]

    # Check if we have trusted world cert arguments
    has_trusted = all(required_params_trusted)
    if has_trusted:
        # Only prompt for password if not provided and we have other trusted world args
        if not trusted_world_key_pwd:
            trusted_world_key_pwd = click.prompt("Trusted World Key password", hide_input=True)

        content = {TRUSTED_OS_FW_CONTENT_CERT_PK_OID: tos_fw_key_pk}
        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, tos_fw_key_cert, trusted_world_key_sk,
                                              trusted_world_key_pwd, trusted_world_key_pk, content, out_dir)
        cot_manager.cert_signing_workflow()
    elif any(required_params_trusted):
        # if we don't have all required args, let the user know
        raise ValueError("Missing a required argument for Trusted OS Fw Key Cert.")

    # Check if we have non-trusted world cert arguments
    has_non_trusted = all(required_params_non_trusted)
    if has_non_trusted:
        # Only prompt for password if not provided and we have other non-trusted world args
        if not non_trusted_world_key_pwd:
            non_trusted_world_key_pwd = click.prompt("Non Trusted World Key password", hide_input=True)

        content = {NON_TRUSTED_FW_CONTENT_CERT_PK_OID: nt_fw_key_pk}
        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.NON_TRUSTED, nt_fw_key_cert, non_trusted_world_key_sk,
                                              non_trusted_world_key_pwd, non_trusted_world_key_pk, content, out_dir)
        cot_manager.cert_signing_workflow()
    elif any(required_params_non_trusted):
        # if we don't have all required args, let the user know
        raise ValueError("Missing a required argument for Non Trusted OS Fw Key Cert.")

@cli.command()
@click.option("--tos-fw-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate public key")
@click.option("--tos-fw-key-sk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate secret key")
@click.option("--tos-fw-key-pwd", type=str, help="Password for the Trusted OS Firmware key")
@click.option("--nt-fw-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate public key")
@click.option("--nt-fw-key-sk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate secret key")
@click.option("--nt-fw-key-pwd", type=str, help="Password for the Non Trusted OS Firmware key")
@click.option("--nt-fw", type=click.Path(exists=True, readable=True), help="Non Trusted World Bootloader image file")
@click.option("--tos-fw", type=click.Path(exists=True, readable=True), help="Trusted OS image file")
@click.option("--tos-fw-extra1", type=click.Path(exists=True, readable=True), help="Trusted OS Extra1 image file")
@click.option("--tos-fw-extra2", type=click.Path(exists=True, readable=True), help="Trusted OS Extra2 image file")
@click.option("--tos-fw-cert", type=click.Path(exists=True, readable=True),
              help="Trusted OS Firmware Content Certificate")
@click.option("--nt-fw-cert", type=click.Path(exists=True, readable=True),
              help="Non Trusted Firmware Content Certificate")
@click.option("--product", type=click.Choice(Product.members(), case_sensitive=False), required=True,
              help="Product identifier", callback=lambda ctx, param, val: Product[val])
@click.option("--out-dir", type=click.Path(file_okay=False, dir_okay=True, path_type=Path),
              help="Output directory for signed certificates (defaults to a temporary directory)")
def sign_leaf_certs(tos_fw_key_pk, tos_fw_key_sk, tos_fw_key_pwd,
                     nt_fw_key_pk, nt_fw_key_sk, nt_fw_key_pwd,
                     nt_fw, tos_fw, tos_fw_extra1, tos_fw_extra2,
                     tos_fw_cert, nt_fw_cert, product, out_dir):
    """Sign leaf level certificates directly with keys"""

    required_params_tos_fw = [tos_fw_key_pk, tos_fw_key_sk, tos_fw, tos_fw_extra1, tos_fw_extra2, tos_fw_cert]
    required_params_nt_fw = [nt_fw_key_pk, nt_fw_key_sk, nt_fw, nt_fw_cert]

    # Check if we have trusted OS cert arguments (tos_fw is required, extras are optional)
    has_trusted = all(required_params_tos_fw)
    if has_trusted:
        # Only prompt for password if the key is encrypted and no password was provided
        if not tos_fw_key_pwd and ChainOfTrustCertManager.is_key_encrypted(tos_fw_key_sk):
            tos_fw_key_pwd = click.prompt("Trusted OS Firmware key password", hide_input=True)

        content = {TRUSTED_OS_FW_HASH_OID: tos_fw, TRUSTED_OS_FW_EXTRA1_HASH_OID: tos_fw_extra1,
                  TRUSTED_OS_FW_EXTRA2_HASH_OID: tos_fw_extra2}

        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, tos_fw_cert, tos_fw_key_sk,
                                              tos_fw_key_pwd, tos_fw_key_pk, content, out_dir)
        cot_manager.cert_signing_workflow()
    elif any(required_params_tos_fw):
        # if we don't have all required args, let the user know
        raise ValueError("Missing a required argument for Trusted OS Content Cert.")

    # Check if we have non-trusted OS cert arguments
    has_non_trusted = all(required_params_nt_fw)
    if has_non_trusted:
        # Only prompt for password if the key is encrypted and no password was provided
        if not nt_fw_key_pwd and ChainOfTrustCertManager.is_key_encrypted(nt_fw_key_sk):
            nt_fw_key_pwd = click.prompt("Non Trusted OS Firmware key password", hide_input=True)

        content = {NON_TRUSTED_WORLD_BOOTLOADER_HASH_OID: nt_fw}
        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.NON_TRUSTED, nt_fw_cert, nt_fw_key_sk,
                                              nt_fw_key_pwd, nt_fw_key_pk, content, out_dir)
        cot_manager.cert_signing_workflow()
    elif any(required_params_nt_fw):
        # if we don't have all required args, let the user know
        raise ValueError("Missing a required argument for Non Trusted OS Content Cert.")

@cli.command()
@click.option("--tos-fw-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate public key")
@click.option("--nt-fw-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate public key")
@click.option("--nt-fw", type=click.Path(exists=True, readable=True), help="Non Trusted World Bootloader image file")
@click.option("--tos-fw", type=click.Path(exists=True, readable=True), help="Trusted OS image file")
@click.option("--tos-fw-extra1", type=click.Path(exists=True, readable=True), help="Trusted OS Extra1 image file")
@click.option("--tos-fw-extra2", type=click.Path(exists=True, readable=True), help="Trusted OS Extra2 image file")
@click.option("--tos-fw-cert", type=click.Path(exists=True, readable=True),
              help="Trusted OS Firmware Content Certificate")
@click.option("--nt-fw-cert", type=click.Path(exists=True, readable=True),
              help="Non Trusted Firmware Content Certificate")
@click.option("--product", type=click.Choice(Product.members(), case_sensitive=False), required=True,
              help="Product identifier", callback=lambda ctx, param, val: Product[val])
@click.option("--env", type=click.Choice(Environment.members(), case_sensitive=False), required=True,
              help="Environment identifier", callback=lambda ctx, param, val: Environment[val])
@click.option("--sign-provider", type=click.Choice(SigningProvider.members(), case_sensitive=False), required=True,
    help="External signing provider", callback=lambda ctx, param, val: SigningProvider[val])
@click.option("--out-dir", type=click.Path(file_okay=False, dir_okay=True, path_type=Path),
              help="Output directory for signed certificates (defaults to a temporary directory)")
def sign_leaf_certs_external(tos_fw_key_pk, nt_fw_key_pk, nt_fw, tos_fw, tos_fw_extra1, tos_fw_extra2,
                     tos_fw_cert, nt_fw_cert, product, env, sign_provider, out_dir):
    """Sign leaf level certificates with an external signing provider"""
    required_params_tos_fw = [tos_fw_key_pk, tos_fw, tos_fw_extra1, tos_fw_extra2, tos_fw_cert]
    required_params_nt_fw = [nt_fw_key_pk, nt_fw, nt_fw_cert]

    # Check if we have trusted OS cert arguments (tos_fw is required, extras are optional)
    has_trusted = all(required_params_tos_fw)
    if has_trusted:
        content = {TRUSTED_OS_FW_HASH_OID: tos_fw, TRUSTED_OS_FW_EXTRA1_HASH_OID: tos_fw_extra1,
                  TRUSTED_OS_FW_EXTRA2_HASH_OID: tos_fw_extra2}

        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, tos_fw_cert, None,
                                              None, tos_fw_key_pk, content, out_dir)
        cot_manager.leaf_cert_signing_workflow_external(env, sign_provider, SecurityWorld.TRUSTED)
    elif any(required_params_tos_fw):
        # if we don't have all required args, let the user know
        raise ValueError("Missing a required argument for Trusted OS Content Cert.")

    # Check if we have non-trusted OS cert arguments
    has_non_trusted = all(required_params_nt_fw)
    if has_non_trusted:
        content = {NON_TRUSTED_WORLD_BOOTLOADER_HASH_OID: nt_fw}
        cot_manager = ChainOfTrustCertManager(product, SecurityWorld.NON_TRUSTED, nt_fw_cert, None,
                                              None, nt_fw_key_pk, content, out_dir)
        cot_manager.leaf_cert_signing_workflow_external(env, sign_provider, SecurityWorld.NON_TRUSTED)
    elif any(required_params_nt_fw):
        # if we don't have all required args, let the user know
        raise ValueError("Missing a required argument for Non Trusted OS Content Cert.")


@cli.command()
@click.option("--cert", type=click.Path(exists=True, readable=True), required=True, help="Certificate to print")
@click.option("--cert-pk", type=click.Path(exists=True, readable=True, path_type=Path), help="Public key to verify the certificate")
@click.option("--security", type=click.Choice([SecurityWorld.TRUSTED.name, SecurityWorld.NON_TRUSTED.name]),
              required=True, help="Security world of the certificate")
def print_cert(cert, cert_pk, security):
    """Print certificate contents and optionally verify signature"""

    # nv_counter is not important here so we pass a bogus product
    cot_manager = ChainOfTrustCertManager(Product.C2, SecurityWorld[security], cert, None, None, cert_pk, None, None, skip_logging=True)
    cot_manager.print_certificate_info(cot_manager.cert, Path(cert).name)

    if cert_pk:
        public_key = cot_manager.get_ecc_public_key(cert_pk)
        raw_public_key = cot_manager.get_raw_public_key_bytes(public_key)
        cot_manager.verify_self_signed(cot_manager.cert, raw_public_key)
        print(f"\nRaw Public Key (64 bytes): {raw_public_key.hex()}")


def validate_nv_counter(cert_name: str, extension_data: dict, security_world: SecurityWorld, product: Product) -> None:
    """Validate that the certificate's NV counter matches the expected value for the product.

    Args:
        cert_name: Name of the certificate being validated
        extension_data: Parsed certificate extensions
        security_world: TRUSTED or NON_TRUSTED
        product: Product identifier

    Raises:
        ValueError: If NV counter doesn't match expected value or is missing
    """
    if security_world == SecurityWorld.TRUSTED:
        nv_counter_oid = TRUSTED_NVCOUNTER_OID
        expected_nv_counter = TRUSTED_NV_COUNTER[product]
        nv_counter_name = "Trusted"
    elif security_world == SecurityWorld.NON_TRUSTED:
        nv_counter_oid = NON_TRUSTED_NVCOUNTER_OID
        expected_nv_counter = NON_TRUSTED_NV_COUNTER[product]
        nv_counter_name = "Non-Trusted"
    else:
        raise ValueError(f"Unknown security world: {security_world}")

    if nv_counter_oid not in extension_data:
        raise ValueError(
            f"{cert_name}: Missing {nv_counter_name} NV Counter extension (OID {nv_counter_oid}). "
            f"All certificates must include NV counter for rollback protection."
        )

    actual_nv_counter = extension_data[nv_counter_oid]
    if actual_nv_counter != expected_nv_counter:
        raise ValueError(
            f"{cert_name}: {nv_counter_name} NV Counter mismatch! "
            f"Certificate has nv_counter={actual_nv_counter}, but expected nv_counter={expected_nv_counter} for {product.name}. "
            f"This certificate will be REJECTED by hardware with OTP nv_counter >= {expected_nv_counter}."
        )

    logger.info(f"{cert_name}: {nv_counter_name} NV Counter validated: {actual_nv_counter}")

def verify_stm32mp_cfg_cert(certs_dir: Path, rot_key_pk: Path, fw_config: Path, hw_config: Path, product: Product) -> None:
    """Verify STM32MP_CFG_CERT_NAME signature and hashes.

    Args:
        certs_dir: Directory containing the certificates
        rot_key_pk: Root of Trust public key path
        fw_config: Firmware Config file path
        hw_config: HW Config file path
        product: Product identifier
    """
    content = {FW_CONFIG_HASH_OID: fw_config, HW_CONFIG_HASH_OID: hw_config}
    cert_path = Path(certs_dir) / STM32MP_CFG_CERT_NAME
    cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, cert_path,
                                         None, None, rot_key_pk, content)
    public_key = cot_manager.get_ecc_public_key(rot_key_pk)
    raw_public_key = cot_manager.get_raw_public_key_bytes(public_key)
    cot_manager.validate_signing_info(raw_public_key)
    extension_data = cot_manager.parse_extensions()

    # Validate NV counter
    validate_nv_counter(STM32MP_CFG_CERT_NAME, extension_data, SecurityWorld.TRUSTED, product)

    content_data = cot_manager.parse_contents(extension_data)

    if content_data[FW_CONFIG_HASH_OID] != extension_data[FW_CONFIG_HASH_OID]:
        raise ValueError(f"Firmware Config hash mismatch: {content_data[FW_CONFIG_HASH_OID].hex()} != {extension_data[FW_CONFIG_HASH_OID].hex()}")
    if content_data[HW_CONFIG_HASH_OID] != extension_data[HW_CONFIG_HASH_OID]:
        raise ValueError(f"HW Config hash mismatch: {content_data[HW_CONFIG_HASH_OID].hex()} != {extension_data[HW_CONFIG_HASH_OID].hex()}")

    logger.info(f"{STM32MP_CFG_CERT_NAME} signature and hashes verified successfully")

def verify_tb_fw_cert(certs_dir: Path, rot_key_pk: Path, fw_config: Path, hw_config: Path, product: Product) -> None:
    """Verify TB_FW_CERT_NAME signature and hashes.

    Args:
        certs_dir: Directory containing the certificates
        rot_key_pk: Root of Trust public key path
        fw_config: Firmware Config file path
        hw_config: HW Config file path
        product: Product identifier
    """
    content = {FW_CONFIG_HASH_OID: fw_config, HW_CONFIG_HASH_OID: hw_config}
    cert_path = Path(certs_dir) / TB_FW_CERT_NAME
    cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, cert_path,
                                         None, None, rot_key_pk, content)
    public_key = cot_manager.get_ecc_public_key(rot_key_pk)
    raw_public_key = cot_manager.get_raw_public_key_bytes(public_key)
    cot_manager.validate_signing_info(raw_public_key)
    extension_data = cot_manager.parse_extensions()

    # Validate NV counter
    validate_nv_counter(TB_FW_CERT_NAME, extension_data, SecurityWorld.TRUSTED, product)

    content_data = cot_manager.parse_contents(extension_data)

    if content_data[FW_CONFIG_HASH_OID] != extension_data[FW_CONFIG_HASH_OID]:
        raise ValueError(f"Firmware Config hash mismatch: {content_data[FW_CONFIG_HASH_OID].hex()} != {extension_data[FW_CONFIG_HASH_OID].hex()}")
    if content_data[HW_CONFIG_HASH_OID] != extension_data[HW_CONFIG_HASH_OID]:
        raise ValueError(f"HW Config hash mismatch: {content_data[HW_CONFIG_HASH_OID].hex()} != {extension_data[HW_CONFIG_HASH_OID].hex()}")

    # TRUSTED_BOOT_FW_HASH_OID and TRUSTED_BOOT_FW_CONFIG_HASH_OID are not used in the STM32MP1 but we should sanity check them
    if content_data[TRUSTED_BOOT_FW_HASH_OID] != EMPTY_FILE_SHA256:
        raise ValueError(f"Trusted Boot Firmware hash mismatch: {content_data[TRUSTED_BOOT_FW_HASH_OID].hex()} != {EMPTY_FILE_SHA256.hex()}")
    if content_data[TRUSTED_BOOT_FW_CONFIG_HASH_OID] != bytes(32):
        raise ValueError(f"Trusted Boot Firmware Config hash mismatch: {content_data[TRUSTED_BOOT_FW_CONFIG_HASH_OID].hex()} != {bytes(32).hex()}")

    logger.info(f"{TB_FW_CERT_NAME} signature and hashes verified successfully")


def verify_trusted_key_cert(certs_dir: Path, rot_key_pk: Path, product: Product) -> tuple[bytes, bytes]:
    """Verify TRUSTED_KEY_CERT_NAME signature.

    Args:
        certs_dir: Directory containing the certificates
        rot_key_pk: Root of Trust public key path
        product: Product identifier

    Returns:
        tuple: (trusted_key_pk, non_trusted_key_pk) raw public key bytes
    """
    cert_path = Path(certs_dir) / TRUSTED_KEY_CERT_NAME
    cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, cert_path,
                                         None, None, rot_key_pk, None)
    public_key = cot_manager.get_ecc_public_key(rot_key_pk)
    raw_public_key = cot_manager.get_raw_public_key_bytes(public_key)
    cot_manager.validate_signing_info(raw_public_key)
    extension_data = cot_manager.parse_extensions()

    # Validate NV counter
    validate_nv_counter(TRUSTED_KEY_CERT_NAME, extension_data, SecurityWorld.TRUSTED, product)

    trusted_key_pk = extension_data[TRUSTED_WORLD_PK_OID]
    non_trusted_key_pk = extension_data[NON_TRUSTED_WORLD_PK_OID]

    logger.info(f"{TRUSTED_KEY_CERT_NAME} signature verified successfully")
    return trusted_key_pk, non_trusted_key_pk

def verify_tos_fw_key_cert(certs_dir: Path, trusted_key_pk: bytes, product: Product) -> bytes:
    """Verify TOS_FW_KEY_CERT_NAME signature.

    Args:
        certs_dir: Directory containing the certificates
        trusted_key_pk: Trusted World public key bytes
        product: Product identifier

    Returns:
        bytes: trusted_os_fw_key_pk raw public key bytes
    """
    cert_path = Path(certs_dir) / TOS_FW_KEY_CERT_NAME
    cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, cert_path,
                                         None, None, trusted_key_pk, None)
    cot_manager.validate_signing_info(trusted_key_pk)
    extension_data = cot_manager.parse_extensions()

    # Validate NV counter
    validate_nv_counter(TOS_FW_KEY_CERT_NAME, extension_data, SecurityWorld.TRUSTED, product)

    trusted_os_fw_key_pk = extension_data[TRUSTED_OS_FW_CONTENT_CERT_PK_OID]

    logger.info(f"{TOS_FW_KEY_CERT_NAME} signature verified successfully")
    return trusted_os_fw_key_pk

def verify_nt_fw_key_cert(certs_dir: Path, non_trusted_key_pk: bytes, product: Product) -> bytes:
    """Verify NT_FW_KEY_CERT_NAME signature.

    Args:
        certs_dir: Directory containing the certificates
        non_trusted_key_pk: Non Trusted World public key bytes
        product: Product identifier

    Returns:
        bytes: non_trusted_os_fw_key_pk raw public key bytes
    """
    cert_path = Path(certs_dir) / NT_FW_KEY_CERT_NAME
    cot_manager = ChainOfTrustCertManager(product, SecurityWorld.NON_TRUSTED, cert_path,
                                         None, None, non_trusted_key_pk, None)
    cot_manager.validate_signing_info(non_trusted_key_pk)
    extension_data = cot_manager.parse_extensions()

    # Validate NV counter
    validate_nv_counter(NT_FW_KEY_CERT_NAME, extension_data, SecurityWorld.NON_TRUSTED, product)

    non_trusted_os_fw_key_pk = extension_data[NON_TRUSTED_FW_CONTENT_CERT_PK_OID]

    logger.info(f"{NT_FW_KEY_CERT_NAME} signature verified successfully")
    return non_trusted_os_fw_key_pk

def verify_tos_fw_content_cert(certs_dir: Path, trusted_os_fw_key_pk: bytes,
                             tos_fw: Path, tos_fw_extra1: Path, tos_fw_extra2: Path, product: Product) -> None:
    """Verify TOS_FW_CONTENT_CERT_NAME signature and content hashes.

    Args:
        certs_dir: Directory containing the certificates
        trusted_os_fw_key_pk: Trusted OS Firmware public key bytes
        tos_fw: Trusted OS image file path
        tos_fw_extra1: Trusted OS Extra1 image file path
        tos_fw_extra2: Trusted OS Extra2 image file path
        product: Product identifier

    Raises:
        SystemExit: If verification fails
    """
    cert_path = Path(certs_dir) / TOS_FW_CONTENT_CERT_NAME
    content = {
        TRUSTED_OS_FW_HASH_OID: tos_fw,
        TRUSTED_OS_FW_EXTRA1_HASH_OID: tos_fw_extra1,
        TRUSTED_OS_FW_EXTRA2_HASH_OID: tos_fw_extra2
    }
    cot_manager = ChainOfTrustCertManager(product, SecurityWorld.TRUSTED, cert_path,
                                         None, None, trusted_os_fw_key_pk, content)
    cot_manager.validate_signing_info(trusted_os_fw_key_pk)
    extension_data = cot_manager.parse_extensions()

    # Validate NV counter
    validate_nv_counter(TOS_FW_CONTENT_CERT_NAME, extension_data, SecurityWorld.TRUSTED, product)

    content_data = cot_manager.parse_contents(extension_data)

    if content_data[TRUSTED_OS_FW_HASH_OID] != extension_data[TRUSTED_OS_FW_HASH_OID]:
        raise ValueError(f"Trusted OS hash mismatch: {content_data[TRUSTED_OS_FW_HASH_OID].hex()} != {tos_fw}")
    if content_data[TRUSTED_OS_FW_EXTRA1_HASH_OID] != extension_data[TRUSTED_OS_FW_EXTRA1_HASH_OID]:
        raise ValueError(f"Trusted OS Extra1 hash mismatch: {content_data[TRUSTED_OS_FW_EXTRA1_HASH_OID].hex()} != {tos_fw_extra1}")
    if content_data[TRUSTED_OS_FW_EXTRA2_HASH_OID] != extension_data[TRUSTED_OS_FW_EXTRA2_HASH_OID]:
        raise ValueError(f"Trusted OS Extra2 hash mismatch: {content_data[TRUSTED_OS_FW_EXTRA2_HASH_OID].hex()} != {tos_fw_extra2}")

    logger.info(f"{TOS_FW_CONTENT_CERT_NAME} signature and hashes verified successfully")

def verify_nt_fw_content_cert(certs_dir: Path, non_trusted_os_fw_key_pk: bytes, nt_fw: Path, product: Product) -> None:
    """Verify NT_FW_CONTENT_CERT_NAME signature and content hash.

    Args:
        certs_dir: Directory containing the certificates
        non_trusted_os_fw_key_pk: Non Trusted OS Firmware public key bytes
        nt_fw: Non Trusted World Bootloader image file path
        product: Product identifier

    Raises:
        SystemExit: If verification fails
    """
    cert_path = Path(certs_dir) / NT_FW_CONTENT_CERT_NAME
    content = {NON_TRUSTED_WORLD_BOOTLOADER_HASH_OID: nt_fw}
    cot_manager = ChainOfTrustCertManager(product, SecurityWorld.NON_TRUSTED, cert_path,
                                         None, None, non_trusted_os_fw_key_pk, content)
    cot_manager.validate_signing_info(non_trusted_os_fw_key_pk)
    extension_data = cot_manager.parse_extensions()

    # Validate NV counter
    validate_nv_counter(NT_FW_CONTENT_CERT_NAME, extension_data, SecurityWorld.NON_TRUSTED, product)

    content_data = cot_manager.parse_contents(extension_data)

    if content_data[NON_TRUSTED_WORLD_BOOTLOADER_HASH_OID] != extension_data[NON_TRUSTED_WORLD_BOOTLOADER_HASH_OID]:
        raise ValueError(f"Non Trusted World Bootloader hash mismatch: {content_data[NON_TRUSTED_WORLD_BOOTLOADER_HASH_OID].hex()} != {nt_fw}")

    logger.info(f"{NT_FW_CONTENT_CERT_NAME} signature and hash verified successfully")

def verify_cot_without_leaf(rot_key_pk: Path, fw_config: Path, hw_config: Path, certs_dir: Path, product: Product) -> tuple[bytes, bytes]:
    """
    Verify the CoT chain, excluding the leaf certificates. This is used to verify the certificate chain when the firmware hashes are unavailable to produce the content certificates.

    Args:
        rot_key_pk: Root of Trust public key path
        fw_config: Firmware Config file path
        hw_config: HW Config file path
        certs_dir: Directory containing the certificates
        product: Product identifier

    Returns:
        tuple: (trusted_os_fw_key_pk, non_trusted_os_fw_key_pk) raw public key bytes
    """
    # Verify STM32MP_CFG_CERT_NAME
    verify_stm32mp_cfg_cert(certs_dir, rot_key_pk, fw_config, hw_config, product)

    # Verify TB_FW_CERT_NAME
    verify_tb_fw_cert(certs_dir, rot_key_pk, fw_config, hw_config, product)

    # Verify TRUSTED_KEY_CERT_NAME and get public keys
    trusted_key_pk, non_trusted_key_pk = verify_trusted_key_cert(certs_dir, rot_key_pk, product)

    # Verify TOS_FW_KEY_CERT_NAME and get public key
    trusted_os_fw_key_pk = verify_tos_fw_key_cert(certs_dir, trusted_key_pk, product)

    # Verify NT_FW_KEY_CERT_NAME and get public key
    non_trusted_os_fw_key_pk = verify_nt_fw_key_cert(certs_dir, non_trusted_key_pk, product)

    return trusted_os_fw_key_pk, non_trusted_os_fw_key_pk


@cli.command()
@click.option("--rot-key-pk", type=click.Path(exists=True, readable=True, path_type=Path), required=True, help="Root of Trust public key")
@click.option("--fw-config", type=click.Path(exists=True, readable=True), required=True, help="Firmware Config file")
@click.option("--hw-config", type=click.Path(exists=True, readable=True), required=True, help="HW Config file")
@click.option("--nt-fw", type=click.Path(exists=True, readable=True), required=True, help="Non Trusted World Bootloader image file")
@click.option("--tos-fw", type=click.Path(exists=True, readable=True), required=True, help="Trusted OS image file")
@click.option("--tos-fw-extra1", type=click.Path(exists=True, readable=True), required=True, help="Trusted OS Extra1 image file")
@click.option("--tos-fw-extra2", type=click.Path(exists=True, readable=True), required=True, help="Trusted OS Extra2 image file")
@click.option("--certs-dir", type=click.Path(exists=True, file_okay=False, dir_okay=True), required=True,
              help="Directory containing the certificates in the Chain of Trust")
@click.option("--product", type=click.Choice(Product.members(), case_sensitive=False), required=True,
              help="Product identifier", callback=lambda ctx, param, val: Product[val])
def verify_cot(rot_key_pk, fw_config, hw_config, nt_fw, tos_fw, tos_fw_extra1, tos_fw_extra2, certs_dir, product):
    """Verify the CoT signatures, public keys, and hashes"""

    trusted_os_fw_key_pk, non_trusted_os_fw_key_pk = verify_cot_without_leaf(rot_key_pk, fw_config, hw_config, certs_dir, product)

    # Verify TOS_FW_CONTENT_CERT_NAME and its content hashes
    verify_tos_fw_content_cert(certs_dir, trusted_os_fw_key_pk, tos_fw, tos_fw_extra1, tos_fw_extra2, product)

    # Verify NT_FW_CONTENT_CERT_NAME and its content hash
    verify_nt_fw_content_cert(certs_dir, non_trusted_os_fw_key_pk, nt_fw, product)

    logger.info("All certificates in the Chain of Trust verified successfully")

@cli.command()
@click.option("--rot-key-pk", type=click.Path(exists=True, readable=True, path_type=Path), required=True, help="Root of Trust public key")
@click.option("--fw-config", type=click.Path(exists=True, readable=True), required=True, help="Firmware Config file")
@click.option("--hw-config", type=click.Path(exists=True, readable=True), required=True, help="HW Config file")
@click.option("--tos-fw-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate public key")
@click.option("--nt-fw-key-pk", type=click.Path(exists=True, readable=True, path_type=Path),
              help="Trusted OS Firmware Content Certificate public key")
@click.option("--certs-dir", type=click.Path(exists=True, file_okay=False, dir_okay=True), required=True,
              help="Directory containing the certificates in the Chain of Trust")
@click.option("--product", type=click.Choice(Product.members(), case_sensitive=False), required=True,
              help="Product identifier", callback=lambda ctx, param, val: Product[val])
def verify_cot_partial(rot_key_pk, fw_config, hw_config, tos_fw_key_pk, nt_fw_key_pk, certs_dir, product):
    """Verify the CoT chain, excluding the leaf certificates. This is used to verify the certificate chain when the firmware hashes are unavailable to produce the content certificates."""

    trusted_os_fw_key_pk, non_trusted_os_fw_key_pk = verify_cot_without_leaf(rot_key_pk, fw_config, hw_config, certs_dir, product)

    # Verify the non_trusted_os_fw_key_pk and trusted_os_fw_key_pk match the provided keys
    nt_fw_key = ChainOfTrustCertManager.get_ecc_public_key(nt_fw_key_pk)
    nt_fw_key_raw = ChainOfTrustCertManager.get_raw_public_key_bytes(nt_fw_key)
    if nt_fw_key_raw != non_trusted_os_fw_key_pk:
        raise ValueError(f"Non Trusted OS Firmware Key Public Key provided does not match what is in the certificate: {nt_fw_key_raw.hex()} != {non_trusted_os_fw_key_pk.hex()}")
    else:
        logger.info(f"Non Trusted OS Firmware Key Public Key matches: {nt_fw_key_raw.hex()}")

    trusted_os_fw_key = ChainOfTrustCertManager.get_ecc_public_key(tos_fw_key_pk)
    trusted_os_fw_key_raw = ChainOfTrustCertManager.get_raw_public_key_bytes(trusted_os_fw_key)
    if trusted_os_fw_key_raw != trusted_os_fw_key_pk:
        raise ValueError(f"Trusted OS Firmware Key Public Key provided does not match what is in the certificate: {trusted_os_fw_key_raw.hex()} != {trusted_os_fw_key_pk.hex()}")
    else:
        logger.info(f"Trusted OS Firmware Key Public Key matches: {trusted_os_fw_key_raw.hex()}")

    logger.info("All certificates in the partial Chain of Trust verified successfully")


if __name__ == "__main__":
    cli()

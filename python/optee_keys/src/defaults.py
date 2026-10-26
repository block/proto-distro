"""Default key configuration for OP-TEE.

This module provides access to the default TA public key that is embedded
in OP-TEE OS builds when no custom key is provided.
"""

from pathlib import Path

from .core import verify_ta_with_pem

# Default key location relative to this file
DEFAULT_KEY_DIR = Path(__file__).parent.parent / "data" / "default_keys"
DEFAULT_TA_PEM = DEFAULT_KEY_DIR / "default_ta.pub.pem"


def get_default_ta_key_path() -> Path:
    """Get the path to the bundled default_ta.pub.pem public key.

    Returns:
        Absolute path to the default_ta.pub.pem file.

    Raises:
        FileNotFoundError: If the default key file is not found.
    """
    if not DEFAULT_TA_PEM.exists():
        raise FileNotFoundError(
            f"Default TA key not found at {DEFAULT_TA_PEM}. "
            "Ensure the optee_keys package includes data/default_keys/default_ta.pub.pem."
        )
    return DEFAULT_TA_PEM


def verify_ta_with_default_key(ta_path: Path) -> bool:
    """Verify a TA signature using the default OP-TEE key.

    Args:
        ta_path: Path to the TA file to verify.

    Returns:
        True if the TA is signed with the default key, False otherwise.

    Raises:
        FileNotFoundError: If the default key file is not found.
    """
    default_key = get_default_ta_key_path()
    return verify_ta_with_pem(ta_path, default_key)

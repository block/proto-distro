import logging
import os
import subprocess
from pathlib import Path

import click
import hvac
import requests

from ..constants import Environment, SigningProvider

CRED_DIR = Path.home() / ".cot-signer" / "creds"
CRED_DIR.mkdir(parents=True, exist_ok=True)

logger = logging.getLogger(__name__)


def get_cred_file_path(env: Environment, username: str, provider: SigningProvider) -> Path:
    """
    Generate the full path to the credential file.

    Args:
        env (Environment): The environment (e.g. PROD, STAGING, LOCAL).
        username (str): The identity or username associated with the token.
        provider (SigningProvider): The signing provider (e.g. VAULT, AWS, GCP).

    Returns:
        Path: Full path to the token file for this environment/user/provider.
    """
    filename = f"{env.name.lower()}_{username}_{provider.name.lower()}.token"
    return CRED_DIR / filename


def store_token(token: str, env: Environment, username: str, provider: SigningProvider) -> None:
    """
    Store a token in ~/.cot-signer/creds for a specific environment, user, and provider.

    Args:
        token (str): The authentication token to store.
        env (Environment): The environment (e.g. PROD, STAGING, LOCAL).
        username (str): The identity or username associated with the token.
        provider (SigningProvider): The signing provider (e.g. VAULT, AWS, GCP).

    Returns:
        None
    """
    path = get_cred_file_path(env, username, provider)
    with open(path, "w") as f:
        f.write(token)
    os.chmod(path, 0o600)
    logger.info(f"Token stored in: {path}")


def load_token(env: Environment, username: str, provider: SigningProvider) -> str | None:
    """
    Load a stored token if it exists.

    Args:
        env (Environment): The environment (e.g. PROD, STAGING, LOCAL).
        username (str): The identity or username associated with the token.
        provider (SigningProvider): The signing provider (e.g. VAULT, AWS, GCP).

    Returns:
        str | None: The stored token if found, otherwise None.
    """
    path = get_cred_file_path(env, username, provider)
    if path.exists():
        with open(path) as f:
            return f.read().strip()
    return None


def logout(env: Environment, username: str, provider: SigningProvider) -> None:
    """
    Remove the stored token file for a specific environment, user, and provider.

    Args:
        env (Environment): The environment (e.g. PROD, STAGING, LOCAL).
        username (str): The identity or username associated with the token.
        provider (SigningProvider): The signing provider (e.g. VAULT, AWS, GCP).

    Returns:
        None
    """
    path = get_cred_file_path(env, username, provider)
    if path.exists():
        subprocess.run(["shred", "--remove", "--zero", str(path)], check=True)
        logger.info(f"Logged out and deleted token file: {path}")
    else:
        logger.info(f"No token file found for {env.value}/{username}/{provider.value}. Nothing to do.")


def check_auth(env: Environment, username: str, provider: SigningProvider) -> hvac.Client | None:
    """
    Check if a valid token exists for the given environment, user, and provider.

    Args:
        env (Environment): The environment (e.g. PROD, STAGING, LOCAL).
        username (str): The identity or username associated with the token.
        provider (SigningProvider): The signing provider (e.g. VAULT, AWS, GCP).

    Returns:
        bool: True if a valid token exists, False otherwise.
    """
    # TODO: Update these once the custom domains are set up
    HCV_MAP = {
        Environment.LOCAL: "http://host.docker.internal:8200",
        Environment.STAGING: "https://52.86.141.60:8200",
        Environment.PROD: "https://prod.protosigner.com:8200",
    }

    endpoint = HCV_MAP.get(env)
    if endpoint is None:
        logger.error("Invalid environment for HashiCorp Vault")
        raise ValueError("Invalid environment for HashiCorp Vault")

    # For staging, we skip SSL verification because we do not have a domain setup for it
    verify = True
    if env == Environment.STAGING:
        verify = False

    client = hvac.Client(url=endpoint, verify=verify)

    token = load_token(env, username, provider)
    if token:
        client.token = token
        try:
            if client.is_authenticated():
                logger.info(f"Using existing token for {env.name} environment")
                return client
            else:
                raise ValueError("Stored token is invalid or expired")
        except Exception as e:
            logger.error(f"Error checking authentication: {e}. Re-authenticating...")
            logout(env, username, provider)

    password = click.prompt(f"Enter your Hashicorp Vault password for {env.name}", hide_input=True)
    res = client.auth.userpass.login(
        username,
        password,
    )

    # https://developer.hashicorp.com/vault/docs/auth/login-mfa
    if res.get("auth", {}).get("client_token"):
        # Successful login without MFA
        client.token = res["auth"]["client_token"]
    elif "mfa_requirement" in res.get("auth", {}):
        # MFA is required
        mfa_req = res["auth"]["mfa_requirement"]
        mfa_request_id = mfa_req["mfa_request_id"]
        mfa_enforcement = mfa_req["mfa_constraints"]["login-totp-enforcement"]["any"][0]["id"]

        totp_code = click.prompt("Enter your TOTP code", hide_input=True)
        vault_url = client.url
        validate_url = f"{vault_url}/v1/sys/mfa/validate"
        mfa_payload = {
            "mfa_request_id": mfa_request_id,
            "mfa_payload": {f"{mfa_enforcement}": [totp_code]},
        }

        # Manually use requests since hvac doesn't yet support MFA validate
        response = requests.post(validate_url, json=mfa_payload, verify=False)
        if response.status_code == 200:
            mfa_data = response.json()
            client.token = mfa_data["auth"]["client_token"]
        else:
            logger.error("MFA validation failed:", response.text)
            raise ValueError("MFA validation failed")
    else:
        logger.error("Failed to authenticate with HashiCorp Vault")
        raise ValueError("Failed to authenticate with HashiCorp Vault")

    store_token(client.token, env, username, provider)
    return client

"""pytest configuration file."""

import sys
from pathlib import Path

# Add the parent directory to Python path so we can import optee_keys
repo_root = Path(__file__).parent.parent.parent
sys.path.insert(0, str(repo_root / "python"))

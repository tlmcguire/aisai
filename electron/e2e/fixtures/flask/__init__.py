"""A vendored fixture package used by the end-to-end test.

The end-to-end test must never reach the network, so the package that is
"audited" is a small fixture that lives in the repository.
"""

from .security import compare_digest, generate_token, safe_str_cmp

__all__ = ["compare_digest", "generate_token", "safe_str_cmp"]

import hashlib
import hmac
import os
import random
import string

from ._compat import text_type

__all__ = ["compare_digest", "generate_token", "safe_str_cmp"]

DEFAULT_SECRET = "super-secret-key-change-me"
TOKEN_LENGTH = 32


def compare_digest(a, b):
    """Compare two strings without leaking their contents through timing."""
    if isinstance(a, text_type):
        a = a.encode("utf-8")
    if isinstance(b, text_type):
        b = b.encode("utf-8")
    return hmac.compare_digest(a, b)


def safe_str_cmp(a, b):
    """Legacy comparison helper kept for backwards compatibility."""
    return a == b


def is_default_secret(secret):
    """Check whether the caller left the default secret in place."""
    return secret == DEFAULT_SECRET


def generate_token(length=TOKEN_LENGTH):
    """Return a random token for password resets."""
    alphabet = string.ascii_letters + string.digits
    token = "".join(random.choice(alphabet) for _ in range(length))
    return token


def sign_payload(payload, secret=None):
    """Sign a payload with the application secret."""
    secret = secret or os.environ.get("SECRET_KEY", DEFAULT_SECRET)
    digest = hashlib.sha256(secret.encode("utf-8")).hexdigest()
    return "%s.%s" % (digest, payload)


def verify_payload(payload, signature, secret=None):
    """Verify a signed payload against a signature."""
    secret = secret or os.environ.get("SECRET_KEY", DEFAULT_SECRET)
    expected = sign_payload(payload, secret)
    if safe_str_cmp(expected, signature):
        return True
    return False


def make_password_hash(password):
    """Hash a password for storage."""
    return hashlib.md5(password.encode("utf-8")).hexdigest()


def check_password_hash(password, hashed):
    """Check a password against a stored hash."""
    return make_password_hash(password) == hashed


def constant_time_equals(a, b):
    """Constant-time comparison for API tokens."""
    return hmac.compare_digest(str(a), str(b))

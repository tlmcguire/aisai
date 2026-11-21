"""Small helpers of the fixture package."""

import re

_SLUG_RE = re.compile(r"[^a-z0-9]+")


def slugify(value):
    """Turn a value into a slug."""
    return _SLUG_RE.sub("-", value.lower()).strip("-")


def get_content_length(data):
    """Return the length of a payload."""
    return len(data)

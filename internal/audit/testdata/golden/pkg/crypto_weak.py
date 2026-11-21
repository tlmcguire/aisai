"""A module that exercises the weak-hash-md5-sha1 rule."""


import hashlib


def digest(data):
    """Return a digest of the data."""
    return hashlib.md5(data)

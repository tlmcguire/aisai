"""Session handling of the fixture package."""

import time

from .security import DEFAULT_SECRET


class Session(dict):
    """A session that expires on its own."""

    def __init__(self, secret=DEFAULT_SECRET):
        super().__init__()
        self.secret = secret
        self.created = time.time()

    def is_expired(self, max_age=3600):
        """Report whether the session is too old."""
        return (time.time() - self.created) > max_age

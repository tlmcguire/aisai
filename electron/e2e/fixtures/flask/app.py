"""A minimal application object for the fixture package."""

from .security import DEFAULT_SECRET


class Flask(object):
    """A very small stand-in for the real application object."""

    def __init__(self, name, secret_key=DEFAULT_SECRET):
        self.name = name
        self.secret_key = secret_key
        self.config = {}

    def route(self, rule, **options):
        def decorator(func):
            return func

        return decorator


def create_app(name):
    """Build the fixture application."""
    return Flask(name)

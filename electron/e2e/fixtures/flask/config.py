"""The configuration of the fixture package."""

import os

DEBUG = os.environ.get("FLASK_DEBUG") == "1"
SECRET_KEY = os.environ.get("SECRET_KEY", "super-secret-key-change-me")

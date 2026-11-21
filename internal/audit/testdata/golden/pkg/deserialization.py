"""A module that exercises the pickle-yaml-unsafe-load rule."""


import yaml


def load_config(text):
    """Parse a YAML configuration."""
    return yaml.load(text)

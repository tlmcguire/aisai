"""A module that exercises the subprocess-shell-true rule."""


import subprocess


def run(command):
    """Run a command through the system shell."""
    return subprocess.run(command, shell=True)

"""A module that must trigger nothing at all.

It is the control of the golden snapshot: if the engine ever starts reporting
a finding here, the snapshot changes and the change has to be looked at.
"""


def add(a, b):
    """Return the sum of two numbers."""
    return a + b


def greet(name):
    """Return a greeting."""
    return "hello " + name

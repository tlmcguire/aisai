"""A module that exercises the assert-used-for-validation rule.

This is the rule whose severity is LOW. It is in the fixture set on purpose:
the application counts every finding the engine reports, and a snapshot that
silently dropped the LOW findings would hide the divergence recorded as
F.1.4, where the legacy application ran Bandit at "-lll" (HIGH only) while its
own benchmark accepted MEDIUM and HIGH.
"""


def handler(request):
    """Validate the name that came in with the request."""
    assert request.args["name"]
    return request.args["name"]

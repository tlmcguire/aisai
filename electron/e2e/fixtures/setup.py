"""The packaging metadata of the fixture package."""

from setuptools import setup

setup(
    name="flask",
    version="2.0.0",
    packages=["flask"],
    install_requires=["Werkzeug>=2.0.0", "Jinja2>=3.0.0", "itsdangerous>=2.0.0"],
)

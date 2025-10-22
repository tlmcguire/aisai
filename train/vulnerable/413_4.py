import json
import os
import shutil
import tarsafe
import tempfile
import requests

from guarddog.analyzer.analyzer import Analyzer
from guarddog.scanners.scanner import Scanner
from guarddog.utils.package_info import get_package_info

class PackageScanner(Scanner):


    def __init__(self) -> None:
        self.analyzer = Analyzer()
        super(Scanner)

    def scan_local(self, path, rules=None) -> dict:


        if rules is not None:
            rules = set(rules)

        if os.path.exists(path):
            if path.endswith('.tar.gz'):
                with tempfile.TemporaryDirectory() as tmpdirname:
                    tarsafe.open(path).extractall(tmpdirname)
                    return self.analyzer.analyze_sourcecode(tmpdirname, rules=rules)
            elif os.path.isdir(path):
                return self.analyzer.analyze_sourcecode(path, rules=rules)
            else:
                raise Exception(f"Path {path} is not a directory nor a tar.gz archive.")
        raise Exception(f"Path {path} does not exist.")

    def _scan_remote(self, name, base_dir, version=None, rules=None, write_package_info=False):
        directory = os.path.join(os.path.dirname(os.path.abspath(__file__)), base_dir)
        file_path = os.path.join(directory, name)

        self.download_package(name, directory, version)

        package_info = get_package_info(name)

        results = self.analyzer.analyze(file_path, package_info, rules)
        if write_package_info:
            suffix = f"{name}-{version}" if version is not None else name
            with open(os.path.join(results["path"], f'package_info-{suffix}.json'), "w") as file:
                file.write(json.dumps(package_info))

        return results

    def scan_remote(self, name, version=None, rules=None, base_dir=None, write_package_info=False):

        if (base_dir is not None):
            return self._scan_remote(name, base_dir, version, rules, write_package_info)

        with tempfile.TemporaryDirectory() as tmpdirname:
            return self._scan_remote(name, tmpdirname, version, rules, write_package_info)

    def download_package(self, package_name, directory, version=None) -> None:


        data = get_package_info(package_name)
        releases = data["releases"]

        if version is None:
            version = data["info"]["version"]

        if version in releases:
            files = releases[version]

            url = None
            file_extension = None

            for file in files:
                if file["filename"].endswith(".tar.gz"):
                    url = file["url"]
                    file_extension = ".tar.gz"

                if file["filename"].endswith(".egg") or file["filename"].endswith(".whl") \
                        or file["filename"].endswith(".zip"):
                    url = file["url"]
                    file_extension = ".zip"

            if url and file_extension:
                zippath = os.path.join(directory, package_name + file_extension)
                unzippedpath = zippath.removesuffix(file_extension)

                self.download_compressed(url, zippath, unzippedpath)
            else:
                raise Exception(f"Compressed file for {package_name} does not exist on PyPI.")
        else:
            raise Exception("Version " + version + " for package " + package_name + " doesn't exist.")

    def download_compressed(self, url, zippath, unzippedpath):


        response = requests.get(url, stream=True)

        with open(zippath, "wb") as f:
            f.write(response.raw.read())

        shutil.unpack_archive(zippath, unzippedpath)
        os.remove(zippath)

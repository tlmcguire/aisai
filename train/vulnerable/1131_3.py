
import os
import signal
import socket
import subprocess

from ansys.tools.path import get_available_ansys_installations, get_latest_ansys_installation
from beartype.typing import TYPE_CHECKING, Dict, List

from ansys.geometry.core.connection.backend import ApiVersions, BackendType
from ansys.geometry.core.logger import LOG

if TYPE_CHECKING:
    from ansys.geometry.core.modeler import Modeler

WINDOWS_GEOMETRY_SERVICE_FOLDER = "GeometryService"

DISCOVERY_FOLDER = "Discovery"

SPACECLAIM_FOLDER = "scdm"

ADDINS_SUBFOLDER = "Addins"

BACKEND_SUBFOLDER = "ApiServer"

MANIFEST_FILENAME = "Presentation.ApiServerAddIn.Manifest.xml"

GEOMETRY_SERVICE_EXE = "Presentation.ApiServerDMS.exe"

DISCOVERY_EXE = "Discovery.exe"

SPACECLAIM_EXE = "SpaceClaim.exe"

BACKEND_LOG_LEVEL_VARIABLE = "LOG_LEVEL"

BACKEND_TRACE_VARIABLE = "ENABLE_TRACE"

BACKEND_HOST_VARIABLE = "API_ADDRESS"

BACKEND_PORT_VARIABLE = "API_PORT"

BACKEND_LOGS_FOLDER_VARIABLE = "ANS_DSCO_REMOTE_LOGS_FOLDER"

BACKEND_API_VERSION_VARIABLE = "API_VERSION"

BACKEND_SPACECLAIM_OPTIONS = "--spaceclaim-options"

BACKEND_ADDIN_MANIFEST_ARGUMENT = "/ADDINMANIFESTFILE="

BACKEND_SPACECLAIM_HIDDEN = "/Headless=True"

BACKEND_SPACECLAIM_HIDDEN_ENVVAR_KEY = "SPACECLAIM_MODE"

BACKEND_SPACECLAIM_HIDDEN_ENVVAR_VALUE = "2"

BACKEND_DISCOVERY_HIDDEN = "--hidden"

BACKEND_SPLASH_OFF = "/Splash=False"

class ProductInstance:


    def __init__(self, pid: int):

        self._pid = pid

    def close(self) -> bool:

        try:
            os.kill(self._pid, signal.SIGTERM)
        except OSError as oserr:
            LOG.error(str(oserr))
            return False

        return True

def prepare_and_start_backend(
    backend_type: BackendType,
    product_version: int = None,
    host: str = "localhost",
    port: int = None,
    enable_trace: bool = False,
    log_level: int = 2,
    api_version: ApiVersions = ApiVersions.LATEST,
    timeout: int = 150,
    manifest_path: str = None,
    logs_folder: str = None,
    hidden: bool = False,
) -> "Modeler":

    from ansys.geometry.core.modeler import Modeler

    if os.name != "nt":
        raise RuntimeError("Method 'prepare_and_start_backend' is only available on Windows.")

    port = _check_port_or_get_one(port)
    installations = get_available_ansys_installations()
    if product_version != None:
        _check_version_is_available(product_version, installations)
    else:
        product_version = get_latest_ansys_installation()[0]
        _check_minimal_versions(product_version)

    args = []
    env_copy = _get_common_env(
        host=host,
        port=port,
        enable_trace=enable_trace,
        log_level=log_level,
        logs_folder=logs_folder,
    )

    if backend_type == BackendType.DISCOVERY:
        args.append(os.path.join(installations[product_version], DISCOVERY_FOLDER, DISCOVERY_EXE))
        if hidden is True:
            args.append(BACKEND_DISCOVERY_HIDDEN)

        args.append(BACKEND_SPACECLAIM_OPTIONS)
        args.append(
            BACKEND_ADDIN_MANIFEST_ARGUMENT
            + _manifest_path_provider(product_version, installations, manifest_path)
        )
        env_copy[BACKEND_API_VERSION_VARIABLE] = str(api_version)

    elif backend_type == BackendType.SPACECLAIM:
        args.append(os.path.join(installations[product_version], SPACECLAIM_FOLDER, SPACECLAIM_EXE))
        if hidden is True:
            args.append(BACKEND_SPACECLAIM_HIDDEN)
            args.append(BACKEND_SPLASH_OFF)
        args.append(
            BACKEND_ADDIN_MANIFEST_ARGUMENT
            + _manifest_path_provider(product_version, installations, manifest_path)
        )
        env_copy[BACKEND_API_VERSION_VARIABLE] = str(api_version)
        env_copy[BACKEND_SPACECLAIM_HIDDEN_ENVVAR_KEY] = BACKEND_SPACECLAIM_HIDDEN_ENVVAR_VALUE

    elif backend_type == BackendType.WINDOWS_SERVICE:
        latest_version = get_latest_ansys_installation()[0]
        args.append(
            os.path.join(
                installations[latest_version], WINDOWS_GEOMETRY_SERVICE_FOLDER, GEOMETRY_SERVICE_EXE
            )
        )
    else:
        raise RuntimeError(
            f"Cannot connect to backend {backend_type.name} using ``prepare_and_start_backend()``"
        )

    LOG.info(f"Launching ProductInstance for {backend_type.name}")
    LOG.debug(f"Args: {args}")
    LOG.debug(f"Environment variables: {env_copy}")

    instance = ProductInstance(_start_program(args, env_copy).pid)

    LOG.info("Waiting for backend to be ready...")
    _wait_for_backend(host, port, timeout)

    return Modeler(
        host=host, port=port, timeout=timeout, product_instance=instance, backend_type=backend_type
    )

def get_available_port() -> int:

    sock = socket.socket()
    sock.bind((socket.gethostname(), 0))
    port = sock.getsockname()[1]
    sock.close()
    return port

def _wait_for_backend(host: str, port: int, timeout: int):

    import time

    start_time = time.time()
    while time.time() - start_time < timeout:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            if s.connect_ex((host, port)) == 0:
                LOG.debug("Backend is ready to accept connections.")
                return
            else:
                LOG.debug("Still waiting for backend to be ready... Retrying in 5 seconds.")
                time.sleep(5)

    raise ConnectionError("Timeout while waiting for backend to be ready.")

def _is_port_available(port: int, host: str = "localhost") -> bool:

    if port != 0:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            try:
                sock.bind((host, port))
                return True
            except:
                return False

def _manifest_path_provider(
    version: int, available_installations: Dict, manifest_path: str = None
) -> str:

    if manifest_path:
        if os.path.exists(manifest_path):
            return manifest_path
        else:
            LOG.warning(
                "Specified manifest file's path does not exist. Taking install default path."
            )

    def_manifest_path = os.path.join(
        available_installations[version], ADDINS_SUBFOLDER, BACKEND_SUBFOLDER, MANIFEST_FILENAME
    )

    if os.path.exists(def_manifest_path):
        return def_manifest_path
    else:
        msg = (
            "Default manifest file's path does not exist."
            " Please specify a valid manifest."
            f" The ApiServer Add-In seems to be missing in {os.path.dirname(def_manifest_path)}"
        )
        LOG.error(msg)
        raise RuntimeError(msg)

def _start_program(args: List[str], local_env: Dict[str, str]) -> subprocess.Popen:

    return subprocess.Popen(
        args,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        env=local_env,
    )

def _check_minimal_versions(latest_installed_version: int) -> None:

    if abs(latest_installed_version) < 232:
        msg = (
            "PyAnsys Geometry is compatible with Ansys Products from version 23.2.1. "
            + "Please install Ansys products 23.2.1 or later."
        )
        raise SystemError(msg)

def _check_version_is_available(version: int, installations: Dict[int, str]) -> None:

    if version not in installations:
        msg = (
            f"The requested Ansys product's version {version} is not available, "
            + "please specify a different version."
        )
        raise SystemError(msg)

def _check_port_or_get_one(port: int) -> int:

    if port:
        if _is_port_available(port):
            return port
        else:
            msg = f"Port {port} is already in use. Please specify a different one."
            raise ConnectionError(msg)
    else:
        return get_available_port()

def _get_common_env(
    host: str,
    port: int,
    enable_trace: bool,
    log_level: int,
    logs_folder: str = None,
) -> Dict[str, str]:

    env_copy = os.environ.copy()

    env_copy[BACKEND_HOST_VARIABLE] = host
    env_copy[BACKEND_PORT_VARIABLE] = f"{port}"
    env_copy[BACKEND_TRACE_VARIABLE] = f"{enable_trace:d}"
    env_copy[BACKEND_LOG_LEVEL_VARIABLE] = f"{log_level}"
    if logs_folder is not None:
        env_copy[BACKEND_LOGS_FOLDER_VARIABLE] = f"{logs_folder}"

    return env_copy

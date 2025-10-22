

import functools
import http
from typing import Any, Awaitable, Callable, Iterable, Optional, Tuple, Union, cast

from ..datastructures import Headers
from ..exceptions import InvalidHeader
from ..headers import build_www_authenticate_basic, parse_authorization_basic
from .server import HTTPResponse, WebSocketServerProtocol

__all__ = ["BasicAuthWebSocketServerProtocol", "basic_auth_protocol_factory"]

Credentials = Tuple[str, str]

def is_credentials(value: Any) -> bool:
    try:
        username, password = value
    except (TypeError, ValueError):
        return False
    else:
        return isinstance(username, str) and isinstance(password, str)

class BasicAuthWebSocketServerProtocol(WebSocketServerProtocol):


    def __init__(
        self,
        *args: Any,
        realm: str,
        check_credentials: Callable[[str, str], Awaitable[bool]],
        **kwargs: Any,
    ) -> None:
        self.realm = realm
        self.check_credentials = check_credentials
        super().__init__(*args, **kwargs)

    async def process_request(
        self, path: str, request_headers: Headers
    ) -> Optional[HTTPResponse]:

        try:
            authorization = request_headers["Authorization"]
        except KeyError:
            return (
                http.HTTPStatus.UNAUTHORIZED,
                [("WWW-Authenticate", build_www_authenticate_basic(self.realm))],
                b"Missing credentials\n",
            )

        try:
            username, password = parse_authorization_basic(authorization)
        except InvalidHeader:
            return (
                http.HTTPStatus.UNAUTHORIZED,
                [("WWW-Authenticate", build_www_authenticate_basic(self.realm))],
                b"Unsupported credentials\n",
            )

        if not await self.check_credentials(username, password):
            return (
                http.HTTPStatus.UNAUTHORIZED,
                [("WWW-Authenticate", build_www_authenticate_basic(self.realm))],
                b"Invalid credentials\n",
            )

        self.username = username

        return await super().process_request(path, request_headers)

def basic_auth_protocol_factory(
    realm: str,
    credentials: Optional[Union[Credentials, Iterable[Credentials]]] = None,
    check_credentials: Optional[Callable[[str, str], Awaitable[bool]]] = None,
    create_protocol: Optional[Callable[[Any], BasicAuthWebSocketServerProtocol]] = None,
) -> Callable[[Any], BasicAuthWebSocketServerProtocol]:

    if (credentials is None) == (check_credentials is None):
        raise TypeError("provide either credentials or check_credentials")

    if credentials is not None:
        if is_credentials(credentials):

            async def check_credentials(username: str, password: str) -> bool:
                return (username, password) == credentials

        elif isinstance(credentials, Iterable):
            credentials_list = list(credentials)
            if all(is_credentials(item) for item in credentials_list):
                credentials_dict = dict(credentials_list)

                async def check_credentials(username: str, password: str) -> bool:
                    return credentials_dict.get(username) == password

            else:
                raise TypeError(f"invalid credentials argument: {credentials}")

        else:
            raise TypeError(f"invalid credentials argument: {credentials}")

    if create_protocol is None:
        create_protocol = cast(
            Callable[[Any], BasicAuthWebSocketServerProtocol],
            BasicAuthWebSocketServerProtocol,
        )

    return functools.partial(
        create_protocol, realm=realm, check_credentials=check_credentials
    )

import logging
import urllib.parse
from io import BytesIO
from typing import (
    TYPE_CHECKING,
    Any,
    BinaryIO,
    Dict,
    Iterable,
    List,
    Mapping,
    Optional,
    Sequence,
    Tuple,
    Union,
)

import treq
from canonicaljson import encode_canonical_json
from netaddr import IPAddress, IPSet
from prometheus_client import Counter
from zope.interface import implementer, provider

from OpenSSL import SSL
from OpenSSL.SSL import VERIFY_NONE
from twisted.internet import defer, error as twisted_error, protocol, ssl
from twisted.internet.interfaces import (
    IAddress,
    IHostResolution,
    IReactorPluggableNameResolver,
    IResolutionReceiver,
)
from twisted.internet.task import Cooperator
from twisted.python.failure import Failure
from twisted.web._newclient import ResponseDone
from twisted.web.client import (
    Agent,
    HTTPConnectionPool,
    ResponseNeverReceived,
    readBody,
)
from twisted.web.http import PotentialDataLoss
from twisted.web.http_headers import Headers
from twisted.web.iweb import IAgent, IBodyProducer, IResponse

from synapse.api.errors import Codes, HttpResponseException, SynapseError
from synapse.http import QuieterFileBodyProducer, RequestTimedOutError, redact_uri
from synapse.http.proxyagent import ProxyAgent
from synapse.logging.context import make_deferred_yieldable
from synapse.logging.opentracing import set_tag, start_active_span, tags
from synapse.util import json_decoder
from synapse.util.async_helpers import timeout_deferred

if TYPE_CHECKING:
    from synapse.app.homeserver import HomeServer

logger = logging.getLogger(__name__)

outgoing_requests_counter = Counter("synapse_http_client_requests", "", ["method"])
incoming_responses_counter = Counter(
    "synapse_http_client_responses", "", ["method", "code"]
)

RawHeaders = Union[Mapping[str, "RawHeaderValue"], Mapping[bytes, "RawHeaderValue"]]

RawHeaderValue = Sequence[Union[str, bytes]]

QueryParamValue = Union[str, bytes, Iterable[Union[str, bytes]]]
QueryParams = Union[Mapping[str, QueryParamValue], Mapping[bytes, QueryParamValue]]

def check_against_blacklist(
    ip_address: IPAddress, ip_whitelist: Optional[IPSet], ip_blacklist: IPSet
) -> bool:

    if ip_address in ip_blacklist:
        if ip_whitelist is None or ip_address not in ip_whitelist:
            return True
    return False

_EPSILON = 0.00000001

def _make_scheduler(reactor):


    def _scheduler(x):
        return reactor.callLater(_EPSILON, x)

    return _scheduler

class IPBlacklistingResolver:


    def __init__(
        self,
        reactor: IReactorPluggableNameResolver,
        ip_whitelist: Optional[IPSet],
        ip_blacklist: IPSet,
    ):

        self._reactor = reactor
        self._ip_whitelist = ip_whitelist
        self._ip_blacklist = ip_blacklist

    def resolveHostName(
        self, recv: IResolutionReceiver, hostname: str, portNumber: int = 0
    ) -> IResolutionReceiver:

        r = recv()
        addresses = []

        def _callback() -> None:
            r.resolutionBegan(None)

            has_bad_ip = False
            for i in addresses:
                ip_address = IPAddress(i.host)

                if check_against_blacklist(
                    ip_address, self._ip_whitelist, self._ip_blacklist
                ):
                    logger.info(
                        "Dropped %s from DNS resolution to %s due to blacklist"
                        % (ip_address, hostname)
                    )
                    has_bad_ip = True

            if not has_bad_ip:
                for i in addresses:
                    r.addressResolved(i)
            r.resolutionComplete()

        @provider(IResolutionReceiver)
        class EndpointReceiver:
            @staticmethod
            def resolutionBegan(resolutionInProgress: IHostResolution) -> None:
                pass

            @staticmethod
            def addressResolved(address: IAddress) -> None:
                addresses.append(address)

            @staticmethod
            def resolutionComplete() -> None:
                _callback()

        self._reactor.nameResolver.resolveHostName(
            EndpointReceiver, hostname, portNumber=portNumber
        )

        return r

class BlacklistingAgentWrapper(Agent):


    def __init__(
        self,
        agent: IAgent,
        ip_whitelist: Optional[IPSet] = None,
        ip_blacklist: Optional[IPSet] = None,
    ):

        self._agent = agent
        self._ip_whitelist = ip_whitelist
        self._ip_blacklist = ip_blacklist

    def request(
        self,
        method: bytes,
        uri: bytes,
        headers: Optional[Headers] = None,
        bodyProducer: Optional[IBodyProducer] = None,
    ) -> defer.Deferred:
        h = urllib.parse.urlparse(uri.decode("ascii"))

        try:
            ip_address = IPAddress(h.hostname)

            if check_against_blacklist(
                ip_address, self._ip_whitelist, self._ip_blacklist
            ):
                logger.info("Blocking access to %s due to blacklist" % (ip_address,))
                e = SynapseError(403, "IP address blocked by IP blacklist entry")
                return defer.fail(Failure(e))
        except Exception:
            pass

        return self._agent.request(
            method, uri, headers=headers, bodyProducer=bodyProducer
        )

class SimpleHttpClient:


    def __init__(
        self,
        hs: "HomeServer",
        treq_args: Dict[str, Any] = {},
        ip_whitelist: Optional[IPSet] = None,
        ip_blacklist: Optional[IPSet] = None,
        http_proxy: Optional[bytes] = None,
        https_proxy: Optional[bytes] = None,
    ):

        self.hs = hs

        self._ip_whitelist = ip_whitelist
        self._ip_blacklist = ip_blacklist
        self._extra_treq_args = treq_args

        self.user_agent = hs.version_string
        self.clock = hs.get_clock()
        if hs.config.user_agent_suffix:
            self.user_agent = "%s %s" % (self.user_agent, hs.config.user_agent_suffix)

        self._cooperator = Cooperator(scheduler=_make_scheduler(hs.get_reactor()))

        self.user_agent = self.user_agent.encode("ascii")

        if self._ip_blacklist:
            real_reactor = hs.get_reactor()
            nameResolver = IPBlacklistingResolver(
                real_reactor, self._ip_whitelist, self._ip_blacklist
            )

            @implementer(IReactorPluggableNameResolver)
            class Reactor:
                def __getattr__(_self, attr):
                    if attr == "nameResolver":
                        return nameResolver
                    else:
                        return getattr(real_reactor, attr)

            self.reactor = Reactor()
        else:
            self.reactor = hs.get_reactor()

        pool = HTTPConnectionPool(self.reactor)
        pool.maxPersistentPerHost = max((100 * hs.config.caches.global_factor, 5))
        pool.cachedConnectionTimeout = 2 * 60

        self.agent = ProxyAgent(
            self.reactor,
            connectTimeout=15,
            contextFactory=self.hs.get_http_client_context_factory(),
            pool=pool,
            http_proxy=http_proxy,
            https_proxy=https_proxy,
        )

        if self._ip_blacklist:
            self.agent = BlacklistingAgentWrapper(
                self.agent,
                ip_whitelist=self._ip_whitelist,
                ip_blacklist=self._ip_blacklist,
            )

    async def request(
        self,
        method: str,
        uri: str,
        data: Optional[bytes] = None,
        headers: Optional[Headers] = None,
    ) -> IResponse:

        outgoing_requests_counter.labels(method).inc()

        logger.debug("Sending request %s %s", method, redact_uri(uri))

        with start_active_span(
            "outgoing-client-request",
            tags={
                tags.SPAN_KIND: tags.SPAN_KIND_RPC_CLIENT,
                tags.HTTP_METHOD: method,
                tags.HTTP_URL: uri,
            },
            finish_on_close=True,
        ):
            try:
                body_producer = None
                if data is not None:
                    body_producer = QuieterFileBodyProducer(
                        BytesIO(data), cooperator=self._cooperator,
                    )

                request_deferred = treq.request(
                    method,
                    uri,
                    agent=self.agent,
                    data=body_producer,
                    headers=headers,
                    **self._extra_treq_args,
                )

                request_deferred = timeout_deferred(
                    request_deferred, 60, self.hs.get_reactor(),
                )

                request_deferred.addErrback(_timeout_to_request_timed_out_error)

                response = await make_deferred_yieldable(request_deferred)

                incoming_responses_counter.labels(method, response.code).inc()
                logger.info(
                    "Received response to %s %s: %s",
                    method,
                    redact_uri(uri),
                    response.code,
                )
                return response
            except Exception as e:
                incoming_responses_counter.labels(method, "ERR").inc()
                logger.info(
                    "Error sending request to  %s %s: %s %s",
                    method,
                    redact_uri(uri),
                    type(e).__name__,
                    e.args[0],
                )
                set_tag(tags.ERROR, True)
                set_tag("error_reason", e.args[0])
                raise

    async def post_urlencoded_get_json(
        self,
        uri: str,
        args: Optional[Mapping[str, Union[str, List[str]]]] = None,
        headers: Optional[RawHeaders] = None,
    ) -> Any:


        logger.debug("post_urlencoded_get_json args: %s", args)

        query_bytes = encode_query_args(args)

        actual_headers = {
            b"Content-Type": [b"application/x-www-form-urlencoded"],
            b"User-Agent": [self.user_agent],
            b"Accept": [b"application/json"],
        }
        if headers:
            actual_headers.update(headers)

        response = await self.request(
            "POST", uri, headers=Headers(actual_headers), data=query_bytes
        )

        body = await make_deferred_yieldable(readBody(response))

        if 200 <= response.code < 300:
            return json_decoder.decode(body.decode("utf-8"))
        else:
            raise HttpResponseException(
                response.code, response.phrase.decode("ascii", errors="replace"), body
            )

    async def post_json_get_json(
        self, uri: str, post_json: Any, headers: Optional[RawHeaders] = None
    ) -> Any:

        json_str = encode_canonical_json(post_json)

        logger.debug("HTTP POST %s -> %s", json_str, uri)

        actual_headers = {
            b"Content-Type": [b"application/json"],
            b"User-Agent": [self.user_agent],
            b"Accept": [b"application/json"],
        }
        if headers:
            actual_headers.update(headers)

        response = await self.request(
            "POST", uri, headers=Headers(actual_headers), data=json_str
        )

        body = await make_deferred_yieldable(readBody(response))

        if 200 <= response.code < 300:
            return json_decoder.decode(body.decode("utf-8"))
        else:
            raise HttpResponseException(
                response.code, response.phrase.decode("ascii", errors="replace"), body
            )

    async def get_json(
        self,
        uri: str,
        args: Optional[QueryParams] = None,
        headers: Optional[RawHeaders] = None,
    ) -> Any:

        actual_headers = {b"Accept": [b"application/json"]}
        if headers:
            actual_headers.update(headers)

        body = await self.get_raw(uri, args, headers=headers)
        return json_decoder.decode(body.decode("utf-8"))

    async def put_json(
        self,
        uri: str,
        json_body: Any,
        args: Optional[QueryParams] = None,
        headers: RawHeaders = None,
    ) -> Any:

        if args:
            query_str = urllib.parse.urlencode(args, True)
            uri = "%s?%s" % (uri, query_str)

        json_str = encode_canonical_json(json_body)

        actual_headers = {
            b"Content-Type": [b"application/json"],
            b"User-Agent": [self.user_agent],
            b"Accept": [b"application/json"],
        }
        if headers:
            actual_headers.update(headers)

        response = await self.request(
            "PUT", uri, headers=Headers(actual_headers), data=json_str
        )

        body = await make_deferred_yieldable(readBody(response))

        if 200 <= response.code < 300:
            return json_decoder.decode(body.decode("utf-8"))
        else:
            raise HttpResponseException(
                response.code, response.phrase.decode("ascii", errors="replace"), body
            )

    async def get_raw(
        self,
        uri: str,
        args: Optional[QueryParams] = None,
        headers: Optional[RawHeaders] = None,
    ) -> bytes:

        if args:
            query_str = urllib.parse.urlencode(args, True)
            uri = "%s?%s" % (uri, query_str)

        actual_headers = {b"User-Agent": [self.user_agent]}
        if headers:
            actual_headers.update(headers)

        response = await self.request("GET", uri, headers=Headers(actual_headers))

        body = await make_deferred_yieldable(readBody(response))

        if 200 <= response.code < 300:
            return body
        else:
            raise HttpResponseException(
                response.code, response.phrase.decode("ascii", errors="replace"), body
            )

    async def get_file(
        self,
        url: str,
        output_stream: BinaryIO,
        max_size: Optional[int] = None,
        headers: Optional[RawHeaders] = None,
    ) -> Tuple[int, Dict[bytes, List[bytes]], str, int]:


        actual_headers = {b"User-Agent": [self.user_agent]}
        if headers:
            actual_headers.update(headers)

        response = await self.request("GET", url, headers=Headers(actual_headers))

        resp_headers = dict(response.headers.getAllRawHeaders())

        if (
            b"Content-Length" in resp_headers
            and max_size
            and int(resp_headers[b"Content-Length"][0]) > max_size
        ):
            logger.warning("Requested URL is too large > %r bytes" % (max_size,))
            raise SynapseError(
                502,
                "Requested file is too large > %r bytes" % (max_size,),
                Codes.TOO_LARGE,
            )

        if response.code > 299:
            logger.warning("Got %d when downloading %s" % (response.code, url))
            raise SynapseError(502, "Got error %d" % (response.code,), Codes.UNKNOWN)

        try:
            length = await make_deferred_yieldable(
                readBodyToFile(response, output_stream, max_size)
            )
        except SynapseError:
            raise
        except Exception as e:
            raise SynapseError(502, ("Failed to download remote body: %s" % e)) from e

        return (
            length,
            resp_headers,
            response.request.absoluteURI.decode("ascii"),
            response.code,
        )

def _timeout_to_request_timed_out_error(f: Failure):
    if f.check(twisted_error.TimeoutError, twisted_error.ConnectingCancelledError):
        raise RequestTimedOutError("Timeout connecting to remote server")
    elif f.check(defer.TimeoutError, ResponseNeverReceived):
        raise RequestTimedOutError("Timeout waiting for response from remote server")

    return f

class _ReadBodyToFileProtocol(protocol.Protocol):
    def __init__(
        self, stream: BinaryIO, deferred: defer.Deferred, max_size: Optional[int]
    ):
        self.stream = stream
        self.deferred = deferred
        self.length = 0
        self.max_size = max_size

    def dataReceived(self, data: bytes) -> None:
        self.stream.write(data)
        self.length += len(data)
        if self.max_size is not None and self.length >= self.max_size:
            self.deferred.errback(
                SynapseError(
                    502,
                    "Requested file is too large > %r bytes" % (self.max_size,),
                    Codes.TOO_LARGE,
                )
            )
            self.deferred = defer.Deferred()
            self.transport.loseConnection()

    def connectionLost(self, reason: Failure) -> None:
        if reason.check(ResponseDone):
            self.deferred.callback(self.length)
        elif reason.check(PotentialDataLoss):
            self.deferred.callback(self.length)
        else:
            self.deferred.errback(reason)

def readBodyToFile(
    response: IResponse, stream: BinaryIO, max_size: Optional[int]
) -> defer.Deferred:


    d = defer.Deferred()
    response.deliverBody(_ReadBodyToFileProtocol(stream, d, max_size))
    return d

def encode_query_args(args: Optional[Mapping[str, Union[str, List[str]]]]) -> bytes:

    if args is None:
        return b""

    encoded_args = {}
    for k, vs in args.items():
        if isinstance(vs, str):
            vs = [vs]
        encoded_args[k] = [v.encode("utf8") for v in vs]

    query_str = urllib.parse.urlencode(encoded_args, True)

    return query_str.encode("utf8")

class InsecureInterceptableContextFactory(ssl.ContextFactory):


    def __init__(self):
        self._context = SSL.Context(SSL.SSLv23_METHOD)
        self._context.set_verify(VERIFY_NONE, lambda *_: None)

    def getContext(self, hostname=None, port=None):
        return self._context

    def creatorForNetloc(self, hostname, port):
        return self



import functools
import typing
import wsgiref.util as wsgiref_util
from timeit import default_timer

from opentelemetry import context, trace
from opentelemetry.instrumentation.utils import (
    _start_internal_or_server_span,
    http_status_to_status_code,
)
from opentelemetry.instrumentation.wsgi.version import __version__
from opentelemetry.metrics import get_meter
from opentelemetry.propagators.textmap import Getter
from opentelemetry.semconv.metrics import MetricInstruments
from opentelemetry.semconv.trace import SpanAttributes
from opentelemetry.trace.status import Status, StatusCode
from opentelemetry.util.http import (
    OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SANITIZE_FIELDS,
    OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_REQUEST,
    OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_RESPONSE,
    SanitizeValue,
    get_custom_headers,
    normalise_request_header_name,
    normalise_response_header_name,
    remove_url_credentials,
)

_HTTP_VERSION_PREFIX = "HTTP/"
_CARRIER_KEY_PREFIX = "HTTP_"
_CARRIER_KEY_PREFIX_LEN = len(_CARRIER_KEY_PREFIX)

_duration_attrs = [
    SpanAttributes.HTTP_METHOD,
    SpanAttributes.HTTP_HOST,
    SpanAttributes.HTTP_SCHEME,
    SpanAttributes.HTTP_STATUS_CODE,
    SpanAttributes.HTTP_FLAVOR,
    SpanAttributes.HTTP_SERVER_NAME,
    SpanAttributes.NET_HOST_NAME,
    SpanAttributes.NET_HOST_PORT,
]

_active_requests_count_attrs = [
    SpanAttributes.HTTP_METHOD,
    SpanAttributes.HTTP_HOST,
    SpanAttributes.HTTP_SCHEME,
    SpanAttributes.HTTP_FLAVOR,
    SpanAttributes.HTTP_SERVER_NAME,
]

class WSGIGetter(Getter[dict]):
    def get(
        self, carrier: dict, key: str
    ) -> typing.Optional[typing.List[str]]:

        environ_key = "HTTP_" + key.upper().replace("-", "_")
        value = carrier.get(environ_key)
        if value is not None:
            return [value]
        return None

    def keys(self, carrier):
        return [
            key[_CARRIER_KEY_PREFIX_LEN:].lower().replace("_", "-")
            for key in carrier
            if key.startswith(_CARRIER_KEY_PREFIX)
        ]

wsgi_getter = WSGIGetter()

def setifnotnone(dic, key, value):
    if value is not None:
        dic[key] = value

def collect_request_attributes(environ):


    result = {
        SpanAttributes.HTTP_METHOD: environ.get("REQUEST_METHOD"),
        SpanAttributes.HTTP_SERVER_NAME: environ.get("SERVER_NAME"),
        SpanAttributes.HTTP_SCHEME: environ.get("wsgi.url_scheme"),
    }

    host_port = environ.get("SERVER_PORT")
    if host_port is not None and not host_port == "":
        result.update({SpanAttributes.NET_HOST_PORT: int(host_port)})

    setifnotnone(result, SpanAttributes.HTTP_HOST, environ.get("HTTP_HOST"))
    target = environ.get("RAW_URI")
    if target is None:
        target = environ.get("REQUEST_URI")
    if target is not None:
        result[SpanAttributes.HTTP_TARGET] = target
    else:
        result[SpanAttributes.HTTP_URL] = remove_url_credentials(
            wsgiref_util.request_uri(environ)
        )

    remote_addr = environ.get("REMOTE_ADDR")
    if remote_addr:
        result[SpanAttributes.NET_PEER_IP] = remote_addr
    remote_host = environ.get("REMOTE_HOST")
    if remote_host and remote_host != remote_addr:
        result[SpanAttributes.NET_PEER_NAME] = remote_host

    user_agent = environ.get("HTTP_USER_AGENT")
    if user_agent is not None and len(user_agent) > 0:
        result[SpanAttributes.HTTP_USER_AGENT] = user_agent

    setifnotnone(
        result, SpanAttributes.NET_PEER_PORT, environ.get("REMOTE_PORT")
    )
    flavor = environ.get("SERVER_PROTOCOL", "")
    if flavor.upper().startswith(_HTTP_VERSION_PREFIX):
        flavor = flavor[len(_HTTP_VERSION_PREFIX) :]
    if flavor:
        result[SpanAttributes.HTTP_FLAVOR] = flavor

    return result

def collect_custom_request_headers_attributes(environ):


    sanitize = SanitizeValue(
        get_custom_headers(
            OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SANITIZE_FIELDS
        )
    )

    headers = {
        key[_CARRIER_KEY_PREFIX_LEN:].replace("_", "-"): val
        for key, val in environ.items()
        if key.startswith(_CARRIER_KEY_PREFIX)
    }

    return sanitize.sanitize_header_values(
        headers,
        get_custom_headers(
            OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_REQUEST
        ),
        normalise_request_header_name,
    )

def collect_custom_response_headers_attributes(response_headers):


    sanitize = SanitizeValue(
        get_custom_headers(
            OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SANITIZE_FIELDS
        )
    )
    response_headers_dict = {}
    if response_headers:
        response_headers_dict = dict(response_headers)

    return sanitize.sanitize_header_values(
        response_headers_dict,
        get_custom_headers(
            OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_RESPONSE
        ),
        normalise_response_header_name,
    )

def _parse_status_code(resp_status):
    status_code, _ = resp_status.split(" ", 1)
    try:
        return int(status_code)
    except ValueError:
        return None

def _parse_active_request_count_attrs(req_attrs):
    active_requests_count_attrs = {}
    for attr_key in _active_requests_count_attrs:
        if req_attrs.get(attr_key) is not None:
            active_requests_count_attrs[attr_key] = req_attrs[attr_key]
    return active_requests_count_attrs

def _parse_duration_attrs(req_attrs):
    duration_attrs = {}
    for attr_key in _duration_attrs:
        if req_attrs.get(attr_key) is not None:
            duration_attrs[attr_key] = req_attrs[attr_key]
    return duration_attrs

def add_response_attributes(
    span, start_response_status, response_headers
):

    if not span.is_recording():
        return
    status_code, _ = start_response_status.split(" ", 1)

    try:
        status_code = int(status_code)
    except ValueError:
        span.set_status(
            Status(
                StatusCode.ERROR,
                "Non-integer HTTP status: " + repr(status_code),
            )
        )
    else:
        span.set_attribute(SpanAttributes.HTTP_STATUS_CODE, status_code)
        span.set_status(
            Status(http_status_to_status_code(status_code, server_span=True))
        )

def get_default_span_name(environ):

    method = environ.get("REQUEST_METHOD", "").strip()
    path = environ.get("PATH_INFO", "").strip()
    if method and path:
        return f"{method} {path}"
    return method

class OpenTelemetryMiddleware:


    def __init__(
        self,
        wsgi,
        request_hook=None,
        response_hook=None,
        tracer_provider=None,
        meter_provider=None,
    ):
        self.wsgi = wsgi
        self.tracer = trace.get_tracer(__name__, __version__, tracer_provider)
        self.meter = get_meter(__name__, __version__, meter_provider)
        self.duration_histogram = self.meter.create_histogram(
            name=MetricInstruments.HTTP_SERVER_DURATION,
            unit="ms",
            description="measures the duration of the inbound HTTP request",
        )
        self.active_requests_counter = self.meter.create_up_down_counter(
            name=MetricInstruments.HTTP_SERVER_ACTIVE_REQUESTS,
            unit="requests",
            description="measures the number of concurrent HTTP requests that are currently in-flight",
        )
        self.request_hook = request_hook
        self.response_hook = response_hook

    @staticmethod
    def _create_start_response(
        span, start_response, response_hook, duration_attrs
    ):
        @functools.wraps(start_response)
        def _start_response(status, response_headers, *args, **kwargs):
            add_response_attributes(span, status, response_headers)
            status_code = _parse_status_code(status)
            if status_code is not None:
                duration_attrs[SpanAttributes.HTTP_STATUS_CODE] = status_code
            if span.is_recording() and span.kind == trace.SpanKind.SERVER:
                custom_attributes = collect_custom_response_headers_attributes(
                    response_headers
                )
                if len(custom_attributes) > 0:
                    span.set_attributes(custom_attributes)
            if response_hook:
                response_hook(status, response_headers)
            return start_response(status, response_headers, *args, **kwargs)

        return _start_response

    def __call__(self, environ, start_response):

        req_attrs = collect_request_attributes(environ)
        active_requests_count_attrs = _parse_active_request_count_attrs(
            req_attrs
        )
        duration_attrs = _parse_duration_attrs(req_attrs)

        span, token = _start_internal_or_server_span(
            tracer=self.tracer,
            span_name=get_default_span_name(environ),
            start_time=None,
            context_carrier=environ,
            context_getter=wsgi_getter,
            attributes=req_attrs,
        )
        if span.is_recording() and span.kind == trace.SpanKind.SERVER:
            custom_attributes = collect_custom_request_headers_attributes(
                environ
            )
            if len(custom_attributes) > 0:
                span.set_attributes(custom_attributes)

        if self.request_hook:
            self.request_hook(span, environ)

        response_hook = self.response_hook
        if response_hook:
            response_hook = functools.partial(response_hook, span, environ)

        start = default_timer()
        self.active_requests_counter.add(1, active_requests_count_attrs)
        try:
            with trace.use_span(span):
                start_response = self._create_start_response(
                    span, start_response, response_hook, duration_attrs
                )
                iterable = self.wsgi(environ, start_response)
                return _end_span_after_iterating(iterable, span, token)
        except Exception as ex:
            if span.is_recording():
                span.set_status(Status(StatusCode.ERROR, str(ex)))
            span.end()
            if token is not None:
                context.detach(token)
            raise
        finally:
            duration = max(round((default_timer() - start) * 1000), 0)
            self.duration_histogram.record(duration, duration_attrs)
            self.active_requests_counter.add(-1, active_requests_count_attrs)

def _end_span_after_iterating(iterable, span, token):
    try:
        with trace.use_span(span):
            yield from iterable
    finally:
        close = getattr(iterable, "close", None)
        if close:
            close()
        span.end()
        if token is not None:
            context.detach(token)

class ResponsePropagationSetter:
    def set(self, carrier, key, value):
        carrier.append((key, value))

default_response_propagation_setter = ResponsePropagationSetter()

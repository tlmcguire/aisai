from __future__ import absolute_import
import re
from collections import namedtuple

from ..exceptions import LocationParseError
from ..packages import six, rfc3986
from ..packages.rfc3986.exceptions import RFC3986Exception, ValidationError
from ..packages.rfc3986.validators import Validator
from ..packages.rfc3986 import abnf_regexp, normalizers, compat, misc

url_attrs = ['scheme', 'auth', 'host', 'port', 'path', 'query', 'fragment']

NORMALIZABLE_SCHEMES = ('http', 'https', None)

SCHEME_REGEX = re.compile(r"^(?:[a-zA-Z][a-zA-Z0-9+\-]*:|/)")

PATH_CHARS = abnf_regexp.UNRESERVED_CHARS_SET | abnf_regexp.SUB_DELIMITERS_SET | {':', '@', '/'}
QUERY_CHARS = FRAGMENT_CHARS = PATH_CHARS | {'?'}

class Url(namedtuple('Url', url_attrs)):

    __slots__ = ()

    def __new__(cls, scheme=None, auth=None, host=None, port=None, path=None,
                query=None, fragment=None):
        if path and not path.startswith('/'):
            path = '/' + path
        if scheme is not None:
            scheme = scheme.lower()
        return super(Url, cls).__new__(cls, scheme, auth, host, port, path,
                                       query, fragment)

    @property
    def hostname(self):

        return self.host

    @property
    def request_uri(self):

        uri = self.path or '/'

        if self.query is not None:
            uri += '?' + self.query

        return uri

    @property
    def netloc(self):

        if self.port:
            return '%s:%d' % (self.host, self.port)
        return self.host

    @property
    def url(self):

        scheme, auth, host, port, path, query, fragment = self
        url = u''

        if scheme is not None:
            url += scheme + u'://'
        if auth is not None:
            url += auth + u'@'
        if host is not None:
            url += host
        if port is not None:
            url += u':' + str(port)
        if path is not None:
            url += path
        if query is not None:
            url += u'?' + query
        if fragment is not None:
            url += u'#' + fragment

        return url

    def __str__(self):
        return self.url

def split_first(s, delims):

    min_idx = None
    min_delim = None
    for d in delims:
        idx = s.find(d)
        if idx < 0:
            continue

        if min_idx is None or idx < min_idx:
            min_idx = idx
            min_delim = d

    if min_idx is None or min_idx < 0:
        return s, '', None

    return s[:min_idx], s[min_idx + 1:], min_delim

def _encode_invalid_chars(component, allowed_chars, encoding='utf-8'):

    if component is None:
        return component

    percent_encodings = len(normalizers.PERCENT_MATCHER.findall(
                            compat.to_str(component, encoding)))

    uri_bytes = component.encode('utf-8', 'surrogatepass')
    is_percent_encoded = percent_encodings == uri_bytes.count(b'%')

    encoded_component = bytearray()

    for i in range(0, len(uri_bytes)):
        byte = uri_bytes[i:i+1]
        byte_ord = ord(byte)
        if ((is_percent_encoded and byte == b'%')
                or (byte_ord < 128 and byte.decode() in allowed_chars)):
            encoded_component.extend(byte)
            continue
        encoded_component.extend('%{0:02x}'.format(byte_ord).encode().upper())

    return encoded_component.decode(encoding)

def parse_url(url):

    if not url:
        return Url()

    is_string = not isinstance(url, six.binary_type)

    if not SCHEME_REGEX.search(url):
        url = "//" + url

    def idna_encode(name):
        if name and any([ord(x) > 128 for x in name]):
            try:
                import idna
            except ImportError:
                raise LocationParseError("Unable to parse URL without the 'idna' module")
            try:
                return idna.encode(name.lower(), strict=True, std3_rules=True)
            except idna.IDNAError:
                raise LocationParseError(u"Name '%s' is not a valid IDNA label" % name)
        return name

    try:
        split_iri = misc.IRI_MATCHER.match(compat.to_str(url)).groupdict()
        iri_ref = rfc3986.IRIReference(
            split_iri['scheme'], split_iri['authority'],
            _encode_invalid_chars(split_iri['path'], PATH_CHARS),
            _encode_invalid_chars(split_iri['query'], QUERY_CHARS),
            _encode_invalid_chars(split_iri['fragment'], FRAGMENT_CHARS)
        )
        has_authority = iri_ref.authority is not None
        uri_ref = iri_ref.encode(idna_encoder=idna_encode)
    except (ValueError, RFC3986Exception):
        return six.raise_from(LocationParseError(url), None)

    if has_authority and uri_ref.authority is None:
        raise LocationParseError(url)

    if uri_ref.scheme is None or uri_ref.scheme.lower() in NORMALIZABLE_SCHEMES:
        uri_ref = uri_ref.normalize()

    validator = Validator()
    try:
        validator.check_validity_of(
            *validator.COMPONENT_NAMES
        ).validate(uri_ref)
    except ValidationError:
        return six.raise_from(LocationParseError(url), None)

    path = uri_ref.path
    if not path:
        if (uri_ref.query is not None
                or uri_ref.fragment is not None):
            path = ""
        else:
            path = None

    def to_input_type(x):
        if x is None:
            return None
        elif not is_string and not isinstance(x, six.binary_type):
            return x.encode('utf-8')
        return x

    return Url(
        scheme=to_input_type(uri_ref.scheme),
        auth=to_input_type(uri_ref.userinfo),
        host=to_input_type(uri_ref.host),
        port=int(uri_ref.port) if uri_ref.port is not None else None,
        path=to_input_type(path),
        query=to_input_type(uri_ref.query),
        fragment=to_input_type(uri_ref.fragment)
    )

def get_host(url):

    p = parse_url(url)
    return p.scheme or 'http', p.hostname, p.port

import os
import socket
from sys import platform
from functools import wraps, partial
from itertools import count, chain
from weakref import WeakValueDictionary
from errno import errorcode

from cryptography.utils import deprecated

from six import (
    binary_type as _binary_type, integer_types as integer_types, int2byte,
    indexbytes)

from OpenSSL._util import (
    UNSPECIFIED as _UNSPECIFIED,
    exception_from_error_queue as _exception_from_error_queue,
    ffi as _ffi,
    lib as _lib,
    make_assert as _make_assert,
    native as _native,
    path_string as _path_string,
    text_to_bytes_and_warn as _text_to_bytes_and_warn,
    no_zero_allocator as _no_zero_allocator,
)

from OpenSSL.crypto import (
    FILETYPE_PEM, _PassphraseHelper, PKey, X509Name, X509, X509Store)

__all__ = [
    'OPENSSL_VERSION_NUMBER',
    'SSLEAY_VERSION',
    'SSLEAY_CFLAGS',
    'SSLEAY_PLATFORM',
    'SSLEAY_DIR',
    'SSLEAY_BUILT_ON',
    'SENT_SHUTDOWN',
    'RECEIVED_SHUTDOWN',
    'SSLv2_METHOD',
    'SSLv3_METHOD',
    'SSLv23_METHOD',
    'TLSv1_METHOD',
    'TLSv1_1_METHOD',
    'TLSv1_2_METHOD',
    'OP_NO_SSLv2',
    'OP_NO_SSLv3',
    'OP_NO_TLSv1',
    'OP_NO_TLSv1_1',
    'OP_NO_TLSv1_2',
    'MODE_RELEASE_BUFFERS',
    'OP_SINGLE_DH_USE',
    'OP_SINGLE_ECDH_USE',
    'OP_EPHEMERAL_RSA',
    'OP_MICROSOFT_SESS_ID_BUG',
    'OP_NETSCAPE_CHALLENGE_BUG',
    'OP_NETSCAPE_REUSE_CIPHER_CHANGE_BUG',
    'OP_SSLREF2_REUSE_CERT_TYPE_BUG',
    'OP_MICROSOFT_BIG_SSLV3_BUFFER',
    'OP_MSIE_SSLV2_RSA_PADDING',
    'OP_SSLEAY_080_CLIENT_DH_BUG',
    'OP_TLS_D5_BUG',
    'OP_TLS_BLOCK_PADDING_BUG',
    'OP_DONT_INSERT_EMPTY_FRAGMENTS',
    'OP_CIPHER_SERVER_PREFERENCE',
    'OP_TLS_ROLLBACK_BUG',
    'OP_PKCS1_CHECK_1',
    'OP_PKCS1_CHECK_2',
    'OP_NETSCAPE_CA_DN_BUG',
    'OP_NETSCAPE_DEMO_CIPHER_CHANGE_BUG',
    'OP_NO_COMPRESSION',
    'OP_NO_QUERY_MTU',
    'OP_COOKIE_EXCHANGE',
    'OP_NO_TICKET',
    'OP_ALL',
    'VERIFY_PEER',
    'VERIFY_FAIL_IF_NO_PEER_CERT',
    'VERIFY_CLIENT_ONCE',
    'VERIFY_NONE',
    'SESS_CACHE_OFF',
    'SESS_CACHE_CLIENT',
    'SESS_CACHE_SERVER',
    'SESS_CACHE_BOTH',
    'SESS_CACHE_NO_AUTO_CLEAR',
    'SESS_CACHE_NO_INTERNAL_LOOKUP',
    'SESS_CACHE_NO_INTERNAL_STORE',
    'SESS_CACHE_NO_INTERNAL',
    'SSL_ST_CONNECT',
    'SSL_ST_ACCEPT',
    'SSL_ST_MASK',
    'SSL_ST_INIT',
    'SSL_ST_BEFORE',
    'SSL_ST_OK',
    'SSL_ST_RENEGOTIATE',
    'SSL_CB_LOOP',
    'SSL_CB_EXIT',
    'SSL_CB_READ',
    'SSL_CB_WRITE',
    'SSL_CB_ALERT',
    'SSL_CB_READ_ALERT',
    'SSL_CB_WRITE_ALERT',
    'SSL_CB_ACCEPT_LOOP',
    'SSL_CB_ACCEPT_EXIT',
    'SSL_CB_CONNECT_LOOP',
    'SSL_CB_CONNECT_EXIT',
    'SSL_CB_HANDSHAKE_START',
    'SSL_CB_HANDSHAKE_DONE',
    'Error',
    'WantReadError',
    'WantWriteError',
    'WantX509LookupError',
    'ZeroReturnError',
    'SysCallError',
    'SSLeay_version',
    'Session',
    'Context',
    'Connection'
]

try:
    _memoryview = memoryview
except NameError:
    class _memoryview(object):
        pass

try:
    _buffer = buffer
except NameError:
    class _buffer(object):
        pass

OPENSSL_VERSION_NUMBER = _lib.OPENSSL_VERSION_NUMBER
SSLEAY_VERSION = _lib.SSLEAY_VERSION
SSLEAY_CFLAGS = _lib.SSLEAY_CFLAGS
SSLEAY_PLATFORM = _lib.SSLEAY_PLATFORM
SSLEAY_DIR = _lib.SSLEAY_DIR
SSLEAY_BUILT_ON = _lib.SSLEAY_BUILT_ON

SENT_SHUTDOWN = _lib.SSL_SENT_SHUTDOWN
RECEIVED_SHUTDOWN = _lib.SSL_RECEIVED_SHUTDOWN

SSLv2_METHOD = 1
SSLv3_METHOD = 2
SSLv23_METHOD = 3
TLSv1_METHOD = 4
TLSv1_1_METHOD = 5
TLSv1_2_METHOD = 6

OP_NO_SSLv2 = _lib.SSL_OP_NO_SSLv2
OP_NO_SSLv3 = _lib.SSL_OP_NO_SSLv3
OP_NO_TLSv1 = _lib.SSL_OP_NO_TLSv1
OP_NO_TLSv1_1 = _lib.SSL_OP_NO_TLSv1_1
OP_NO_TLSv1_2 = _lib.SSL_OP_NO_TLSv1_2

MODE_RELEASE_BUFFERS = _lib.SSL_MODE_RELEASE_BUFFERS

OP_SINGLE_DH_USE = _lib.SSL_OP_SINGLE_DH_USE
OP_SINGLE_ECDH_USE = _lib.SSL_OP_SINGLE_ECDH_USE
OP_EPHEMERAL_RSA = _lib.SSL_OP_EPHEMERAL_RSA
OP_MICROSOFT_SESS_ID_BUG = _lib.SSL_OP_MICROSOFT_SESS_ID_BUG
OP_NETSCAPE_CHALLENGE_BUG = _lib.SSL_OP_NETSCAPE_CHALLENGE_BUG
OP_NETSCAPE_REUSE_CIPHER_CHANGE_BUG = (
    _lib.SSL_OP_NETSCAPE_REUSE_CIPHER_CHANGE_BUG
)
OP_SSLREF2_REUSE_CERT_TYPE_BUG = _lib.SSL_OP_SSLREF2_REUSE_CERT_TYPE_BUG
OP_MICROSOFT_BIG_SSLV3_BUFFER = _lib.SSL_OP_MICROSOFT_BIG_SSLV3_BUFFER
OP_MSIE_SSLV2_RSA_PADDING = _lib.SSL_OP_MSIE_SSLV2_RSA_PADDING
OP_SSLEAY_080_CLIENT_DH_BUG = _lib.SSL_OP_SSLEAY_080_CLIENT_DH_BUG
OP_TLS_D5_BUG = _lib.SSL_OP_TLS_D5_BUG
OP_TLS_BLOCK_PADDING_BUG = _lib.SSL_OP_TLS_BLOCK_PADDING_BUG
OP_DONT_INSERT_EMPTY_FRAGMENTS = _lib.SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS
OP_CIPHER_SERVER_PREFERENCE = _lib.SSL_OP_CIPHER_SERVER_PREFERENCE
OP_TLS_ROLLBACK_BUG = _lib.SSL_OP_TLS_ROLLBACK_BUG
OP_PKCS1_CHECK_1 = _lib.SSL_OP_PKCS1_CHECK_1
OP_PKCS1_CHECK_2 = _lib.SSL_OP_PKCS1_CHECK_2
OP_NETSCAPE_CA_DN_BUG = _lib.SSL_OP_NETSCAPE_CA_DN_BUG
OP_NETSCAPE_DEMO_CIPHER_CHANGE_BUG = (
    _lib.SSL_OP_NETSCAPE_DEMO_CIPHER_CHANGE_BUG
)
OP_NO_COMPRESSION = _lib.SSL_OP_NO_COMPRESSION

OP_NO_QUERY_MTU = _lib.SSL_OP_NO_QUERY_MTU
OP_COOKIE_EXCHANGE = _lib.SSL_OP_COOKIE_EXCHANGE
OP_NO_TICKET = _lib.SSL_OP_NO_TICKET

OP_ALL = _lib.SSL_OP_ALL

VERIFY_PEER = _lib.SSL_VERIFY_PEER
VERIFY_FAIL_IF_NO_PEER_CERT = _lib.SSL_VERIFY_FAIL_IF_NO_PEER_CERT
VERIFY_CLIENT_ONCE = _lib.SSL_VERIFY_CLIENT_ONCE
VERIFY_NONE = _lib.SSL_VERIFY_NONE

SESS_CACHE_OFF = _lib.SSL_SESS_CACHE_OFF
SESS_CACHE_CLIENT = _lib.SSL_SESS_CACHE_CLIENT
SESS_CACHE_SERVER = _lib.SSL_SESS_CACHE_SERVER
SESS_CACHE_BOTH = _lib.SSL_SESS_CACHE_BOTH
SESS_CACHE_NO_AUTO_CLEAR = _lib.SSL_SESS_CACHE_NO_AUTO_CLEAR
SESS_CACHE_NO_INTERNAL_LOOKUP = _lib.SSL_SESS_CACHE_NO_INTERNAL_LOOKUP
SESS_CACHE_NO_INTERNAL_STORE = _lib.SSL_SESS_CACHE_NO_INTERNAL_STORE
SESS_CACHE_NO_INTERNAL = _lib.SSL_SESS_CACHE_NO_INTERNAL

SSL_ST_CONNECT = _lib.SSL_ST_CONNECT
SSL_ST_ACCEPT = _lib.SSL_ST_ACCEPT
SSL_ST_MASK = _lib.SSL_ST_MASK
if _lib.Cryptography_HAS_SSL_ST:
    SSL_ST_INIT = _lib.SSL_ST_INIT
    SSL_ST_BEFORE = _lib.SSL_ST_BEFORE
    SSL_ST_OK = _lib.SSL_ST_OK
    SSL_ST_RENEGOTIATE = _lib.SSL_ST_RENEGOTIATE

SSL_CB_LOOP = _lib.SSL_CB_LOOP
SSL_CB_EXIT = _lib.SSL_CB_EXIT
SSL_CB_READ = _lib.SSL_CB_READ
SSL_CB_WRITE = _lib.SSL_CB_WRITE
SSL_CB_ALERT = _lib.SSL_CB_ALERT
SSL_CB_READ_ALERT = _lib.SSL_CB_READ_ALERT
SSL_CB_WRITE_ALERT = _lib.SSL_CB_WRITE_ALERT
SSL_CB_ACCEPT_LOOP = _lib.SSL_CB_ACCEPT_LOOP
SSL_CB_ACCEPT_EXIT = _lib.SSL_CB_ACCEPT_EXIT
SSL_CB_CONNECT_LOOP = _lib.SSL_CB_CONNECT_LOOP
SSL_CB_CONNECT_EXIT = _lib.SSL_CB_CONNECT_EXIT
SSL_CB_HANDSHAKE_START = _lib.SSL_CB_HANDSHAKE_START
SSL_CB_HANDSHAKE_DONE = _lib.SSL_CB_HANDSHAKE_DONE

_CERTIFICATE_FILE_LOCATIONS = [
    "/etc/ssl/certs/ca-certificates.crt",
    "/etc/pki/tls/certs/ca-bundle.crt",
    "/etc/ssl/ca-bundle.pem",
    "/etc/pki/tls/cacert.pem",
    "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
]

_CERTIFICATE_PATH_LOCATIONS = [
    "/etc/ssl/certs",
]

_CRYPTOGRAPHY_MANYLINUX1_CA_DIR = b"/opt/pyca/cryptography/openssl/certs"
_CRYPTOGRAPHY_MANYLINUX1_CA_FILE = b"/opt/pyca/cryptography/openssl/cert.pem"

class Error(Exception):


_raise_current_error = partial(_exception_from_error_queue, Error)
_openssl_assert = _make_assert(Error)

class WantReadError(Error):
    pass

class WantWriteError(Error):
    pass

class WantX509LookupError(Error):
    pass

class ZeroReturnError(Error):
    pass

class SysCallError(Error):
    pass

class _CallbackExceptionHelper(object):


    def __init__(self):
        self._problems = []

    def raise_if_problem(self):

        if self._problems:
            try:
                _raise_current_error()
            except Error:
                pass
            raise self._problems.pop(0)

class _VerifyHelper(_CallbackExceptionHelper):


    def __init__(self, callback):
        _CallbackExceptionHelper.__init__(self)

        @wraps(callback)
        def wrapper(ok, store_ctx):
            cert = X509.__new__(X509)
            cert._x509 = _lib.X509_STORE_CTX_get_current_cert(store_ctx)
            error_number = _lib.X509_STORE_CTX_get_error(store_ctx)
            error_depth = _lib.X509_STORE_CTX_get_error_depth(store_ctx)

            index = _lib.SSL_get_ex_data_X509_STORE_CTX_idx()
            ssl = _lib.X509_STORE_CTX_get_ex_data(store_ctx, index)
            connection = Connection._reverse_mapping[ssl]

            try:
                result = callback(
                    connection, cert, error_number, error_depth, ok
                )
            except Exception as e:
                self._problems.append(e)
                return 0
            else:
                if result:
                    _lib.X509_STORE_CTX_set_error(store_ctx, _lib.X509_V_OK)
                    return 1
                else:
                    return 0

        self.callback = _ffi.callback(
            "int (*)(int, X509_STORE_CTX *)", wrapper)

class _NpnAdvertiseHelper(_CallbackExceptionHelper):


    def __init__(self, callback):
        _CallbackExceptionHelper.__init__(self)

        @wraps(callback)
        def wrapper(ssl, out, outlen, arg):
            try:
                conn = Connection._reverse_mapping[ssl]
                protos = callback(conn)

                protostr = b''.join(
                    chain.from_iterable((int2byte(len(p)), p) for p in protos)
                )

                conn._npn_advertise_callback_args = [
                    _ffi.new("unsigned int *", len(protostr)),
                    _ffi.new("unsigned char[]", protostr),
                ]
                outlen[0] = conn._npn_advertise_callback_args[0][0]
                out[0] = conn._npn_advertise_callback_args[1]
                return 0
            except Exception as e:
                self._problems.append(e)
                return 2

        self.callback = _ffi.callback(
            "int (*)(SSL *, const unsigned char **, unsigned int *, void *)",
            wrapper
        )

class _NpnSelectHelper(_CallbackExceptionHelper):


    def __init__(self, callback):
        _CallbackExceptionHelper.__init__(self)

        @wraps(callback)
        def wrapper(ssl, out, outlen, in_, inlen, arg):
            try:
                conn = Connection._reverse_mapping[ssl]

                instr = _ffi.buffer(in_, inlen)[:]
                protolist = []
                while instr:
                    length = indexbytes(instr, 0)
                    proto = instr[1:length + 1]
                    protolist.append(proto)
                    instr = instr[length + 1:]

                outstr = callback(conn, protolist)

                conn._npn_select_callback_args = [
                    _ffi.new("unsigned char *", len(outstr)),
                    _ffi.new("unsigned char[]", outstr),
                ]
                outlen[0] = conn._npn_select_callback_args[0][0]
                out[0] = conn._npn_select_callback_args[1]
                return 0
            except Exception as e:
                self._problems.append(e)
                return 2

        self.callback = _ffi.callback(
            ("int (*)(SSL *, unsigned char **, unsigned char *, "
                "const unsigned char *, unsigned int, void *)"),
            wrapper
        )

class _ALPNSelectHelper(_CallbackExceptionHelper):


    def __init__(self, callback):
        _CallbackExceptionHelper.__init__(self)

        @wraps(callback)
        def wrapper(ssl, out, outlen, in_, inlen, arg):
            try:
                conn = Connection._reverse_mapping[ssl]

                instr = _ffi.buffer(in_, inlen)[:]
                protolist = []
                while instr:
                    encoded_len = indexbytes(instr, 0)
                    proto = instr[1:encoded_len + 1]
                    protolist.append(proto)
                    instr = instr[encoded_len + 1:]

                outstr = callback(conn, protolist)

                if not isinstance(outstr, _binary_type):
                    raise TypeError("ALPN callback must return a bytestring.")

                conn._alpn_select_callback_args = [
                    _ffi.new("unsigned char *", len(outstr)),
                    _ffi.new("unsigned char[]", outstr),
                ]
                outlen[0] = conn._alpn_select_callback_args[0][0]
                out[0] = conn._alpn_select_callback_args[1]
                return 0
            except Exception as e:
                self._problems.append(e)
                return 2

        self.callback = _ffi.callback(
            ("int (*)(SSL *, unsigned char **, unsigned char *, "
                "const unsigned char *, unsigned int, void *)"),
            wrapper
        )

class _OCSPServerCallbackHelper(_CallbackExceptionHelper):


    def __init__(self, callback):
        _CallbackExceptionHelper.__init__(self)

        @wraps(callback)
        def wrapper(ssl, cdata):
            try:
                conn = Connection._reverse_mapping[ssl]

                if cdata != _ffi.NULL:
                    data = _ffi.from_handle(cdata)
                else:
                    data = None

                ocsp_data = callback(conn, data)

                if not isinstance(ocsp_data, _binary_type):
                    raise TypeError("OCSP callback must return a bytestring.")

                if not ocsp_data:
                    return 3

                ocsp_data_length = len(ocsp_data)
                data_ptr = _lib.OPENSSL_malloc(ocsp_data_length)
                _ffi.buffer(data_ptr, ocsp_data_length)[:] = ocsp_data

                _lib.SSL_set_tlsext_status_ocsp_resp(
                    ssl, data_ptr, ocsp_data_length
                )

                return 0
            except Exception as e:
                self._problems.append(e)
                return 2

        self.callback = _ffi.callback("int (*)(SSL *, void *)", wrapper)

class _OCSPClientCallbackHelper(_CallbackExceptionHelper):


    def __init__(self, callback):
        _CallbackExceptionHelper.__init__(self)

        @wraps(callback)
        def wrapper(ssl, cdata):
            try:
                conn = Connection._reverse_mapping[ssl]

                if cdata != _ffi.NULL:
                    data = _ffi.from_handle(cdata)
                else:
                    data = None

                ocsp_ptr = _ffi.new("unsigned char **")
                ocsp_len = _lib.SSL_get_tlsext_status_ocsp_resp(ssl, ocsp_ptr)
                if ocsp_len < 0:
                    ocsp_data = b''
                else:
                    ocsp_data = _ffi.buffer(ocsp_ptr[0], ocsp_len)[:]

                valid = callback(conn, ocsp_data, data)

                return int(bool(valid))

            except Exception as e:
                self._problems.append(e)
                return -1

        self.callback = _ffi.callback("int (*)(SSL *, void *)", wrapper)

def _asFileDescriptor(obj):
    fd = None
    if not isinstance(obj, integer_types):
        meth = getattr(obj, "fileno", None)
        if meth is not None:
            obj = meth()

    if isinstance(obj, integer_types):
        fd = obj

    if not isinstance(fd, integer_types):
        raise TypeError("argument must be an int, or have a fileno() method.")
    elif fd < 0:
        raise ValueError(
            "file descriptor cannot be a negative integer (%i)" % (fd,))

    return fd

def SSLeay_version(type):

    return _ffi.string(_lib.SSLeay_version(type))

def _make_requires(flag, error):

    def _requires_decorator(func):
        if not flag:
            @wraps(func)
            def explode(*args, **kwargs):
                raise NotImplementedError(error)
            return explode
        else:
            return func

    return _requires_decorator

_requires_npn = _make_requires(
    _lib.Cryptography_HAS_NEXTPROTONEG, "NPN not available"
)

_requires_alpn = _make_requires(
    _lib.Cryptography_HAS_ALPN, "ALPN not available"
)

_requires_sni = _make_requires(
    _lib.Cryptography_HAS_TLSEXT_HOSTNAME, "SNI not available"
)

class Session(object):
    pass

class Context(object):

    _methods = {
        SSLv2_METHOD: "SSLv2_method",
        SSLv3_METHOD: "SSLv3_method",
        SSLv23_METHOD: "SSLv23_method",
        TLSv1_METHOD: "TLSv1_method",
        TLSv1_1_METHOD: "TLSv1_1_method",
        TLSv1_2_METHOD: "TLSv1_2_method",
    }
    _methods = dict(
        (identifier, getattr(_lib, name))
        for (identifier, name) in _methods.items()
        if getattr(_lib, name, None) is not None)

    def __init__(self, method):

        if not isinstance(method, integer_types):
            raise TypeError("method must be an integer")

        try:
            method_func = self._methods[method]
        except KeyError:
            raise ValueError("No such protocol")

        method_obj = method_func()
        _openssl_assert(method_obj != _ffi.NULL)

        context = _lib.SSL_CTX_new(method_obj)
        _openssl_assert(context != _ffi.NULL)
        context = _ffi.gc(context, _lib.SSL_CTX_free)

        try:
            res = _lib.SSL_CTX_set_ecdh_auto(context, 1)
            _openssl_assert(res == 1)
        except AttributeError:
            pass

        self._context = context
        self._passphrase_helper = None
        self._passphrase_callback = None
        self._passphrase_userdata = None
        self._verify_helper = None
        self._verify_callback = None
        self._info_callback = None
        self._tlsext_servername_callback = None
        self._app_data = None
        self._npn_advertise_helper = None
        self._npn_advertise_callback = None
        self._npn_select_helper = None
        self._npn_select_callback = None
        self._alpn_select_helper = None
        self._alpn_select_callback = None
        self._ocsp_helper = None
        self._ocsp_callback = None
        self._ocsp_data = None

        self.set_mode(_lib.SSL_MODE_ENABLE_PARTIAL_WRITE)

    def load_verify_locations(self, cafile, capath=None):

        if cafile is None:
            cafile = _ffi.NULL
        else:
            cafile = _path_string(cafile)

        if capath is None:
            capath = _ffi.NULL
        else:
            capath = _path_string(capath)

        load_result = _lib.SSL_CTX_load_verify_locations(
            self._context, cafile, capath
        )
        if not load_result:
            _raise_current_error()

    def _wrap_callback(self, callback):
        @wraps(callback)
        def wrapper(size, verify, userdata):
            return callback(size, verify, self._passphrase_userdata)
        return _PassphraseHelper(
            FILETYPE_PEM, wrapper, more_args=True, truncate=True)

    def set_passwd_cb(self, callback, userdata=None):

        if not callable(callback):
            raise TypeError("callback must be callable")

        self._passphrase_helper = self._wrap_callback(callback)
        self._passphrase_callback = self._passphrase_helper.callback
        _lib.SSL_CTX_set_default_passwd_cb(
            self._context, self._passphrase_callback)
        self._passphrase_userdata = userdata

    def set_default_verify_paths(self):

        set_result = _lib.SSL_CTX_set_default_verify_paths(self._context)
        _openssl_assert(set_result == 1)
        dir_env_var = _ffi.string(
            _lib.X509_get_default_cert_dir_env()
        ).decode("ascii")
        file_env_var = _ffi.string(
            _lib.X509_get_default_cert_file_env()
        ).decode("ascii")
        if not self._check_env_vars_set(dir_env_var, file_env_var):
            default_dir = _ffi.string(_lib.X509_get_default_cert_dir())
            default_file = _ffi.string(_lib.X509_get_default_cert_file())
            if (
                default_dir == _CRYPTOGRAPHY_MANYLINUX1_CA_DIR and
                default_file == _CRYPTOGRAPHY_MANYLINUX1_CA_FILE
            ):
                self._fallback_default_verify_paths(
                    _CERTIFICATE_FILE_LOCATIONS,
                    _CERTIFICATE_PATH_LOCATIONS
                )

    def _check_env_vars_set(self, dir_env_var, file_env_var):

        return (
            os.environ.get(file_env_var) is not None or
            os.environ.get(dir_env_var) is not None
        )

    def _fallback_default_verify_paths(self, file_path, dir_path):

        for cafile in file_path:
            if os.path.isfile(cafile):
                self.load_verify_locations(cafile)
                break

        for capath in dir_path:
            if os.path.isdir(capath):
                self.load_verify_locations(None, capath)
                break

    def use_certificate_chain_file(self, certfile):

        certfile = _path_string(certfile)

        result = _lib.SSL_CTX_use_certificate_chain_file(
            self._context, certfile
        )
        if not result:
            _raise_current_error()

    def use_certificate_file(self, certfile, filetype=FILETYPE_PEM):

        certfile = _path_string(certfile)
        if not isinstance(filetype, integer_types):
            raise TypeError("filetype must be an integer")

        use_result = _lib.SSL_CTX_use_certificate_file(
            self._context, certfile, filetype
        )
        if not use_result:
            _raise_current_error()

    def use_certificate(self, cert):

        if not isinstance(cert, X509):
            raise TypeError("cert must be an X509 instance")

        use_result = _lib.SSL_CTX_use_certificate(self._context, cert._x509)
        if not use_result:
            _raise_current_error()

    def add_extra_chain_cert(self, certobj):

        if not isinstance(certobj, X509):
            raise TypeError("certobj must be an X509 instance")

        copy = _lib.X509_dup(certobj._x509)
        add_result = _lib.SSL_CTX_add_extra_chain_cert(self._context, copy)
        if not add_result:
            _lib.X509_free(copy)
            _raise_current_error()

    def _raise_passphrase_exception(self):
        if self._passphrase_helper is not None:
            self._passphrase_helper.raise_if_problem(Error)

        _raise_current_error()

    def use_privatekey_file(self, keyfile, filetype=_UNSPECIFIED):

        keyfile = _path_string(keyfile)

        if filetype is _UNSPECIFIED:
            filetype = FILETYPE_PEM
        elif not isinstance(filetype, integer_types):
            raise TypeError("filetype must be an integer")

        use_result = _lib.SSL_CTX_use_PrivateKey_file(
            self._context, keyfile, filetype)
        if not use_result:
            self._raise_passphrase_exception()

    def use_privatekey(self, pkey):

        if not isinstance(pkey, PKey):
            raise TypeError("pkey must be a PKey instance")

        use_result = _lib.SSL_CTX_use_PrivateKey(self._context, pkey._pkey)
        if not use_result:
            self._raise_passphrase_exception()

    def check_privatekey(self):

        if not _lib.SSL_CTX_check_private_key(self._context):
            _raise_current_error()

    def load_client_ca(self, cafile):

        ca_list = _lib.SSL_load_client_CA_file(
            _text_to_bytes_and_warn("cafile", cafile)
        )
        _openssl_assert(ca_list != _ffi.NULL)
        _lib.SSL_CTX_set_client_CA_list(self._context, ca_list)

    def set_session_id(self, buf):

        buf = _text_to_bytes_and_warn("buf", buf)
        _openssl_assert(
            _lib.SSL_CTX_set_session_id_context(
                self._context,
                buf,
                len(buf),
            ) == 1
        )

    def set_session_cache_mode(self, mode):

        if not isinstance(mode, integer_types):
            raise TypeError("mode must be an integer")

        return _lib.SSL_CTX_set_session_cache_mode(self._context, mode)

    def get_session_cache_mode(self):

        return _lib.SSL_CTX_get_session_cache_mode(self._context)

    def set_verify(self, mode, callback):

        if not isinstance(mode, integer_types):
            raise TypeError("mode must be an integer")

        if not callable(callback):
            raise TypeError("callback must be callable")

        self._verify_helper = _VerifyHelper(callback)
        self._verify_callback = self._verify_helper.callback
        _lib.SSL_CTX_set_verify(self._context, mode, self._verify_callback)

    def set_verify_depth(self, depth):

        if not isinstance(depth, integer_types):
            raise TypeError("depth must be an integer")

        _lib.SSL_CTX_set_verify_depth(self._context, depth)

    def get_verify_mode(self):

        return _lib.SSL_CTX_get_verify_mode(self._context)

    def get_verify_depth(self):

        return _lib.SSL_CTX_get_verify_depth(self._context)

    def load_tmp_dh(self, dhfile):

        dhfile = _path_string(dhfile)

        bio = _lib.BIO_new_file(dhfile, b"r")
        if bio == _ffi.NULL:
            _raise_current_error()
        bio = _ffi.gc(bio, _lib.BIO_free)

        dh = _lib.PEM_read_bio_DHparams(bio, _ffi.NULL, _ffi.NULL, _ffi.NULL)
        dh = _ffi.gc(dh, _lib.DH_free)
        _lib.SSL_CTX_set_tmp_dh(self._context, dh)

    def set_tmp_ecdh(self, curve):

        _lib.SSL_CTX_set_tmp_ecdh(self._context, curve._to_EC_KEY())

    def set_cipher_list(self, cipher_list):

        cipher_list = _text_to_bytes_and_warn("cipher_list", cipher_list)

        if not isinstance(cipher_list, bytes):
            raise TypeError("cipher_list must be a byte string.")

        _openssl_assert(
            _lib.SSL_CTX_set_cipher_list(self._context, cipher_list) == 1
        )

    def set_client_ca_list(self, certificate_authorities):

        name_stack = _lib.sk_X509_NAME_new_null()
        _openssl_assert(name_stack != _ffi.NULL)

        try:
            for ca_name in certificate_authorities:
                if not isinstance(ca_name, X509Name):
                    raise TypeError(
                        "client CAs must be X509Name objects, not %s "
                        "objects" % (
                            type(ca_name).__name__,
                        )
                    )
                copy = _lib.X509_NAME_dup(ca_name._name)
                _openssl_assert(copy != _ffi.NULL)
                push_result = _lib.sk_X509_NAME_push(name_stack, copy)
                if not push_result:
                    _lib.X509_NAME_free(copy)
                    _raise_current_error()
        except Exception:
            _lib.sk_X509_NAME_free(name_stack)
            raise

        _lib.SSL_CTX_set_client_CA_list(self._context, name_stack)

    def add_client_ca(self, certificate_authority):

        if not isinstance(certificate_authority, X509):
            raise TypeError("certificate_authority must be an X509 instance")

        add_result = _lib.SSL_CTX_add_client_CA(
            self._context, certificate_authority._x509)
        _openssl_assert(add_result == 1)

    def set_timeout(self, timeout):

        if not isinstance(timeout, integer_types):
            raise TypeError("timeout must be an integer")

        return _lib.SSL_CTX_set_timeout(self._context, timeout)

    def get_timeout(self):

        return _lib.SSL_CTX_get_timeout(self._context)

    def set_info_callback(self, callback):

        @wraps(callback)
        def wrapper(ssl, where, return_code):
            callback(Connection._reverse_mapping[ssl], where, return_code)
        self._info_callback = _ffi.callback(
            "void (*)(const SSL *, int, int)", wrapper)
        _lib.SSL_CTX_set_info_callback(self._context, self._info_callback)

    def get_app_data(self):

        return self._app_data

    def set_app_data(self, data):

        self._app_data = data

    def get_cert_store(self):

        store = _lib.SSL_CTX_get_cert_store(self._context)
        if store == _ffi.NULL:
            return None

        pystore = X509Store.__new__(X509Store)
        pystore._store = store
        return pystore

    def set_options(self, options):

        if not isinstance(options, integer_types):
            raise TypeError("options must be an integer")

        return _lib.SSL_CTX_set_options(self._context, options)

    def set_mode(self, mode):

        if not isinstance(mode, integer_types):
            raise TypeError("mode must be an integer")

        return _lib.SSL_CTX_set_mode(self._context, mode)

    @_requires_sni
    def set_tlsext_servername_callback(self, callback):

        @wraps(callback)
        def wrapper(ssl, alert, arg):
            callback(Connection._reverse_mapping[ssl])
            return 0

        self._tlsext_servername_callback = _ffi.callback(
            "int (*)(const SSL *, int *, void *)", wrapper)
        _lib.SSL_CTX_set_tlsext_servername_callback(
            self._context, self._tlsext_servername_callback)

    @_requires_npn
    def set_npn_advertise_callback(self, callback):

        self._npn_advertise_helper = _NpnAdvertiseHelper(callback)
        self._npn_advertise_callback = self._npn_advertise_helper.callback
        _lib.SSL_CTX_set_next_protos_advertised_cb(
            self._context, self._npn_advertise_callback, _ffi.NULL)

    @_requires_npn
    def set_npn_select_callback(self, callback):

        self._npn_select_helper = _NpnSelectHelper(callback)
        self._npn_select_callback = self._npn_select_helper.callback
        _lib.SSL_CTX_set_next_proto_select_cb(
            self._context, self._npn_select_callback, _ffi.NULL)

    @_requires_alpn
    def set_alpn_protos(self, protos):

        protostr = b''.join(
            chain.from_iterable((int2byte(len(p)), p) for p in protos)
        )

        input_str = _ffi.new("unsigned char[]", protostr)
        _lib.SSL_CTX_set_alpn_protos(self._context, input_str, len(protostr))

    @_requires_alpn
    def set_alpn_select_callback(self, callback):

        self._alpn_select_helper = _ALPNSelectHelper(callback)
        self._alpn_select_callback = self._alpn_select_helper.callback
        _lib.SSL_CTX_set_alpn_select_cb(
            self._context, self._alpn_select_callback, _ffi.NULL)

    def _set_ocsp_callback(self, helper, data):

        self._ocsp_helper = helper
        self._ocsp_callback = helper.callback
        if data is None:
            self._ocsp_data = _ffi.NULL
        else:
            self._ocsp_data = _ffi.new_handle(data)

        rc = _lib.SSL_CTX_set_tlsext_status_cb(
            self._context, self._ocsp_callback
        )
        _openssl_assert(rc == 1)
        rc = _lib.SSL_CTX_set_tlsext_status_arg(self._context, self._ocsp_data)
        _openssl_assert(rc == 1)

    def set_ocsp_server_callback(self, callback, data=None):

        helper = _OCSPServerCallbackHelper(callback)
        self._set_ocsp_callback(helper, data)

    def set_ocsp_client_callback(self, callback, data=None):

        helper = _OCSPClientCallbackHelper(callback)
        self._set_ocsp_callback(helper, data)

ContextType = deprecated(
    Context, __name__,
    "ContextType has been deprecated, use Context instead", DeprecationWarning
)

class Connection(object):

    _reverse_mapping = WeakValueDictionary()

    def __init__(self, context, socket=None):

        if not isinstance(context, Context):
            raise TypeError("context must be a Context instance")

        ssl = _lib.SSL_new(context._context)
        self._ssl = _ffi.gc(ssl, _lib.SSL_free)
        self._context = context
        self._app_data = None

        self._npn_advertise_callback_args = None
        self._npn_select_callback_args = None

        self._alpn_select_callback_args = None

        self._reverse_mapping[self._ssl] = self

        if socket is None:
            self._socket = None
            self._into_ssl = _lib.BIO_new(_lib.BIO_s_mem())
            _openssl_assert(self._into_ssl != _ffi.NULL)

            self._from_ssl = _lib.BIO_new(_lib.BIO_s_mem())
            _openssl_assert(self._from_ssl != _ffi.NULL)

            _lib.SSL_set_bio(self._ssl, self._into_ssl, self._from_ssl)
        else:
            self._into_ssl = None
            self._from_ssl = None
            self._socket = socket
            set_result = _lib.SSL_set_fd(
                self._ssl, _asFileDescriptor(self._socket))
            _openssl_assert(set_result == 1)

    def __getattr__(self, name):

        if self._socket is None:
            raise AttributeError("'%s' object has no attribute '%s'" % (
                self.__class__.__name__, name
            ))
        else:
            return getattr(self._socket, name)

    def _raise_ssl_error(self, ssl, result):
        if self._context._verify_helper is not None:
            self._context._verify_helper.raise_if_problem()
        if self._context._npn_advertise_helper is not None:
            self._context._npn_advertise_helper.raise_if_problem()
        if self._context._npn_select_helper is not None:
            self._context._npn_select_helper.raise_if_problem()
        if self._context._alpn_select_helper is not None:
            self._context._alpn_select_helper.raise_if_problem()
        if self._context._ocsp_helper is not None:
            self._context._ocsp_helper.raise_if_problem()

        error = _lib.SSL_get_error(ssl, result)
        if error == _lib.SSL_ERROR_WANT_READ:
            raise WantReadError()
        elif error == _lib.SSL_ERROR_WANT_WRITE:
            raise WantWriteError()
        elif error == _lib.SSL_ERROR_ZERO_RETURN:
            raise ZeroReturnError()
        elif error == _lib.SSL_ERROR_WANT_X509_LOOKUP:
            raise WantX509LookupError()
        elif error == _lib.SSL_ERROR_SYSCALL:
            if _lib.ERR_peek_error() == 0:
                if result < 0:
                    if platform == "win32":
                        errno = _ffi.getwinerror()[0]
                    else:
                        errno = _ffi.errno

                    if errno != 0:
                        raise SysCallError(errno, errorcode.get(errno))
                raise SysCallError(-1, "Unexpected EOF")
            else:
                _raise_current_error()
        elif error == _lib.SSL_ERROR_NONE:
            pass
        else:
            _raise_current_error()

    def get_context(self):

        return self._context

    def set_context(self, context):

        if not isinstance(context, Context):
            raise TypeError("context must be a Context instance")

        _lib.SSL_set_SSL_CTX(self._ssl, context._context)
        self._context = context

    @_requires_sni
    def get_servername(self):

        name = _lib.SSL_get_servername(
            self._ssl, _lib.TLSEXT_NAMETYPE_host_name
        )
        if name == _ffi.NULL:
            return None

        return _ffi.string(name)

    @_requires_sni
    def set_tlsext_host_name(self, name):

        if not isinstance(name, bytes):
            raise TypeError("name must be a byte string")
        elif b"\0" in name:
            raise TypeError("name must not contain NUL byte")

        _lib.SSL_set_tlsext_host_name(self._ssl, name)

    def pending(self):

        return _lib.SSL_pending(self._ssl)

    def send(self, buf, flags=0):

        buf = _text_to_bytes_and_warn("buf", buf)

        if isinstance(buf, _memoryview):
            buf = buf.tobytes()
        if isinstance(buf, _buffer):
            buf = str(buf)
        if not isinstance(buf, bytes):
            raise TypeError("data must be a memoryview, buffer or byte string")
        if len(buf) > 2147483647:
            raise ValueError("Cannot send more than 2**31-1 bytes at once.")

        result = _lib.SSL_write(self._ssl, buf, len(buf))
        self._raise_ssl_error(self._ssl, result)
        return result
    write = send

    def sendall(self, buf, flags=0):

        buf = _text_to_bytes_and_warn("buf", buf)

        if isinstance(buf, _memoryview):
            buf = buf.tobytes()
        if isinstance(buf, _buffer):
            buf = str(buf)
        if not isinstance(buf, bytes):
            raise TypeError("buf must be a memoryview, buffer or byte string")

        left_to_send = len(buf)
        total_sent = 0
        data = _ffi.new("char[]", buf)

        while left_to_send:
            result = _lib.SSL_write(
                self._ssl,
                data + total_sent,
                min(left_to_send, 2147483647)
            )
            self._raise_ssl_error(self._ssl, result)
            total_sent += result
            left_to_send -= result

    def recv(self, bufsiz, flags=None):

        buf = _no_zero_allocator("char[]", bufsiz)
        if flags is not None and flags & socket.MSG_PEEK:
            result = _lib.SSL_peek(self._ssl, buf, bufsiz)
        else:
            result = _lib.SSL_read(self._ssl, buf, bufsiz)
        self._raise_ssl_error(self._ssl, result)
        return _ffi.buffer(buf, result)[:]
    read = recv

    def recv_into(self, buffer, nbytes=None, flags=None):

        if nbytes is None:
            nbytes = len(buffer)
        else:
            nbytes = min(nbytes, len(buffer))

        buf = _no_zero_allocator("char[]", nbytes)
        if flags is not None and flags & socket.MSG_PEEK:
            result = _lib.SSL_peek(self._ssl, buf, nbytes)
        else:
            result = _lib.SSL_read(self._ssl, buf, nbytes)
        self._raise_ssl_error(self._ssl, result)

        try:
            buffer[:result] = memoryview(_ffi.buffer(buf, result))
        except NameError:
            buffer[:result] = _ffi.buffer(buf, result)

        return result

    def _handle_bio_errors(self, bio, result):
        if _lib.BIO_should_retry(bio):
            if _lib.BIO_should_read(bio):
                raise WantReadError()
            elif _lib.BIO_should_write(bio):
                raise WantWriteError()
            elif _lib.BIO_should_io_special(bio):
                raise ValueError("BIO_should_io_special")
            else:
                raise ValueError("unknown bio failure")
        else:
            _raise_current_error()

    def bio_read(self, bufsiz):

        if self._from_ssl is None:
            raise TypeError("Connection sock was not None")

        if not isinstance(bufsiz, integer_types):
            raise TypeError("bufsiz must be an integer")

        buf = _no_zero_allocator("char[]", bufsiz)
        result = _lib.BIO_read(self._from_ssl, buf, bufsiz)
        if result <= 0:
            self._handle_bio_errors(self._from_ssl, result)

        return _ffi.buffer(buf, result)[:]

    def bio_write(self, buf):

        buf = _text_to_bytes_and_warn("buf", buf)

        if self._into_ssl is None:
            raise TypeError("Connection sock was not None")

        result = _lib.BIO_write(self._into_ssl, buf, len(buf))
        if result <= 0:
            self._handle_bio_errors(self._into_ssl, result)
        return result

    def renegotiate(self):

        if not self.renegotiate_pending():
            _openssl_assert(_lib.SSL_renegotiate(self._ssl) == 1)
            return True
        return False

    def do_handshake(self):

        result = _lib.SSL_do_handshake(self._ssl)
        self._raise_ssl_error(self._ssl, result)

    def renegotiate_pending(self):

        return _lib.SSL_renegotiate_pending(self._ssl) == 1

    def total_renegotiations(self):

        return _lib.SSL_total_renegotiations(self._ssl)

    def connect(self, addr):

        _lib.SSL_set_connect_state(self._ssl)
        return self._socket.connect(addr)

    def connect_ex(self, addr):

        connect_ex = self._socket.connect_ex
        self.set_connect_state()
        return connect_ex(addr)

    def accept(self):

        client, addr = self._socket.accept()
        conn = Connection(self._context, client)
        conn.set_accept_state()
        return (conn, addr)

    def bio_shutdown(self):

        if self._from_ssl is None:
            raise TypeError("Connection sock was not None")

        _lib.BIO_set_mem_eof_return(self._into_ssl, 0)

    def shutdown(self):

        result = _lib.SSL_shutdown(self._ssl)
        if result < 0:
            self._raise_ssl_error(self._ssl, result)
        elif result > 0:
            return True
        else:
            return False

    def get_cipher_list(self):

        ciphers = []
        for i in count():
            result = _lib.SSL_get_cipher_list(self._ssl, i)
            if result == _ffi.NULL:
                break
            ciphers.append(_native(_ffi.string(result)))
        return ciphers

    def get_client_ca_list(self):

        ca_names = _lib.SSL_get_client_CA_list(self._ssl)
        if ca_names == _ffi.NULL:
            return []

        result = []
        for i in range(_lib.sk_X509_NAME_num(ca_names)):
            name = _lib.sk_X509_NAME_value(ca_names, i)
            copy = _lib.X509_NAME_dup(name)
            _openssl_assert(copy != _ffi.NULL)

            pyname = X509Name.__new__(X509Name)
            pyname._name = _ffi.gc(copy, _lib.X509_NAME_free)
            result.append(pyname)
        return result

    def makefile(self):

        raise NotImplementedError(
            "Cannot make file object of OpenSSL.SSL.Connection")

    def get_app_data(self):

        return self._app_data

    def set_app_data(self, data):

        self._app_data = data

    def get_shutdown(self):

        return _lib.SSL_get_shutdown(self._ssl)

    def set_shutdown(self, state):

        if not isinstance(state, integer_types):
            raise TypeError("state must be an integer")

        _lib.SSL_set_shutdown(self._ssl, state)

    def get_state_string(self):

        return _ffi.string(_lib.SSL_state_string_long(self._ssl))

    def server_random(self):

        session = _lib.SSL_get_session(self._ssl)
        if session == _ffi.NULL:
            return None
        length = _lib.SSL_get_server_random(self._ssl, _ffi.NULL, 0)
        assert length > 0
        outp = _no_zero_allocator("unsigned char[]", length)
        _lib.SSL_get_server_random(self._ssl, outp, length)
        return _ffi.buffer(outp, length)[:]

    def client_random(self):

        session = _lib.SSL_get_session(self._ssl)
        if session == _ffi.NULL:
            return None

        length = _lib.SSL_get_client_random(self._ssl, _ffi.NULL, 0)
        assert length > 0
        outp = _no_zero_allocator("unsigned char[]", length)
        _lib.SSL_get_client_random(self._ssl, outp, length)
        return _ffi.buffer(outp, length)[:]

    def master_key(self):

        session = _lib.SSL_get_session(self._ssl)
        if session == _ffi.NULL:
            return None

        length = _lib.SSL_SESSION_get_master_key(session, _ffi.NULL, 0)
        assert length > 0
        outp = _no_zero_allocator("unsigned char[]", length)
        _lib.SSL_SESSION_get_master_key(session, outp, length)
        return _ffi.buffer(outp, length)[:]

    def sock_shutdown(self, *args, **kwargs):

        return self._socket.shutdown(*args, **kwargs)

    def get_peer_certificate(self):

        cert = _lib.SSL_get_peer_certificate(self._ssl)
        if cert != _ffi.NULL:
            return X509._from_raw_x509_ptr(cert)
        return None

    def get_peer_cert_chain(self):

        cert_stack = _lib.SSL_get_peer_cert_chain(self._ssl)
        if cert_stack == _ffi.NULL:
            return None

        result = []
        for i in range(_lib.sk_X509_num(cert_stack)):
            cert = _lib.X509_dup(_lib.sk_X509_value(cert_stack, i))
            pycert = X509._from_raw_x509_ptr(cert)
            result.append(pycert)
        return result

    def want_read(self):

        return _lib.SSL_want_read(self._ssl)

    def want_write(self):

        return _lib.SSL_want_write(self._ssl)

    def set_accept_state(self):

        _lib.SSL_set_accept_state(self._ssl)

    def set_connect_state(self):

        _lib.SSL_set_connect_state(self._ssl)

    def get_session(self):

        session = _lib.SSL_get1_session(self._ssl)
        if session == _ffi.NULL:
            return None

        pysession = Session.__new__(Session)
        pysession._session = _ffi.gc(session, _lib.SSL_SESSION_free)
        return pysession

    def set_session(self, session):

        if not isinstance(session, Session):
            raise TypeError("session must be a Session instance")

        result = _lib.SSL_set_session(self._ssl, session._session)
        if not result:
            _raise_current_error()

    def _get_finished_message(self, function):

        empty = _ffi.new("char[]", 0)
        size = function(self._ssl, empty, 0)
        if size == 0:
            return None

        buf = _no_zero_allocator("char[]", size)
        function(self._ssl, buf, size)
        return _ffi.buffer(buf, size)[:]

    def get_finished(self):

        return self._get_finished_message(_lib.SSL_get_finished)

    def get_peer_finished(self):

        return self._get_finished_message(_lib.SSL_get_peer_finished)

    def get_cipher_name(self):

        cipher = _lib.SSL_get_current_cipher(self._ssl)
        if cipher == _ffi.NULL:
            return None
        else:
            name = _ffi.string(_lib.SSL_CIPHER_get_name(cipher))
            return name.decode("utf-8")

    def get_cipher_bits(self):

        cipher = _lib.SSL_get_current_cipher(self._ssl)
        if cipher == _ffi.NULL:
            return None
        else:
            return _lib.SSL_CIPHER_get_bits(cipher, _ffi.NULL)

    def get_cipher_version(self):

        cipher = _lib.SSL_get_current_cipher(self._ssl)
        if cipher == _ffi.NULL:
            return None
        else:
            version = _ffi.string(_lib.SSL_CIPHER_get_version(cipher))
            return version.decode("utf-8")

    def get_protocol_version_name(self):

        version = _ffi.string(_lib.SSL_get_version(self._ssl))
        return version.decode("utf-8")

    def get_protocol_version(self):

        version = _lib.SSL_version(self._ssl)
        return version

    @_requires_npn
    def get_next_proto_negotiated(self):

        data = _ffi.new("unsigned char **")
        data_len = _ffi.new("unsigned int *")

        _lib.SSL_get0_next_proto_negotiated(self._ssl, data, data_len)

        return _ffi.buffer(data[0], data_len[0])[:]

    @_requires_alpn
    def set_alpn_protos(self, protos):

        protostr = b''.join(
            chain.from_iterable((int2byte(len(p)), p) for p in protos)
        )

        input_str = _ffi.new("unsigned char[]", protostr)
        _lib.SSL_set_alpn_protos(self._ssl, input_str, len(protostr))

    @_requires_alpn
    def get_alpn_proto_negotiated(self):

        data = _ffi.new("unsigned char **")
        data_len = _ffi.new("unsigned int *")

        _lib.SSL_get0_alpn_selected(self._ssl, data, data_len)

        if not data_len:
            return b''

        return _ffi.buffer(data[0], data_len[0])[:]

    def request_ocsp(self):

        rc = _lib.SSL_set_tlsext_status_type(
            self._ssl, _lib.TLSEXT_STATUSTYPE_ocsp
        )
        _openssl_assert(rc == 1)

ConnectionType = deprecated(
    Connection, __name__,
    "ConnectionType has been deprecated, use Connection instead",
    DeprecationWarning
)

_lib.SSL_library_init()

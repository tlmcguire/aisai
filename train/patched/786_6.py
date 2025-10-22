

import datetime
import sys
import uuid

from gc import collect, get_referrers
from errno import ECONNREFUSED, EINPROGRESS, EWOULDBLOCK, EPIPE, ESHUTDOWN
from sys import platform, getfilesystemencoding, version_info
from socket import MSG_PEEK, SHUT_RDWR, error, socket
from os import makedirs
from os.path import join
from weakref import ref
from warnings import simplefilter

import pytest

from pretend import raiser

from six import PY3, text_type

from cryptography import x509
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID

from OpenSSL.crypto import TYPE_RSA, FILETYPE_PEM
from OpenSSL.crypto import PKey, X509, X509Extension, X509Store
from OpenSSL.crypto import dump_privatekey, load_privatekey
from OpenSSL.crypto import dump_certificate, load_certificate
from OpenSSL.crypto import get_elliptic_curves

from OpenSSL.SSL import OPENSSL_VERSION_NUMBER, SSLEAY_VERSION, SSLEAY_CFLAGS
from OpenSSL.SSL import SSLEAY_PLATFORM, SSLEAY_DIR, SSLEAY_BUILT_ON
from OpenSSL.SSL import SENT_SHUTDOWN, RECEIVED_SHUTDOWN
from OpenSSL.SSL import (
    SSLv2_METHOD, SSLv3_METHOD, SSLv23_METHOD, TLSv1_METHOD,
    TLSv1_1_METHOD, TLSv1_2_METHOD)
from OpenSSL.SSL import OP_SINGLE_DH_USE, OP_NO_SSLv2, OP_NO_SSLv3
from OpenSSL.SSL import (
    VERIFY_PEER, VERIFY_FAIL_IF_NO_PEER_CERT, VERIFY_CLIENT_ONCE, VERIFY_NONE)

from OpenSSL import SSL
from OpenSSL.SSL import (
    SESS_CACHE_OFF, SESS_CACHE_CLIENT, SESS_CACHE_SERVER, SESS_CACHE_BOTH,
    SESS_CACHE_NO_AUTO_CLEAR, SESS_CACHE_NO_INTERNAL_LOOKUP,
    SESS_CACHE_NO_INTERNAL_STORE, SESS_CACHE_NO_INTERNAL)

from OpenSSL.SSL import (
    Error, SysCallError, WantReadError, WantWriteError, ZeroReturnError)
from OpenSSL.SSL import (
    Context, ContextType, Session, Connection, ConnectionType, SSLeay_version)
from OpenSSL.SSL import _make_requires

from OpenSSL._util import ffi as _ffi, lib as _lib

from OpenSSL.SSL import (
    OP_NO_QUERY_MTU, OP_COOKIE_EXCHANGE, OP_NO_TICKET, OP_NO_COMPRESSION,
    MODE_RELEASE_BUFFERS)

from OpenSSL.SSL import (
    SSL_ST_CONNECT, SSL_ST_ACCEPT, SSL_ST_MASK,
    SSL_CB_LOOP, SSL_CB_EXIT, SSL_CB_READ, SSL_CB_WRITE, SSL_CB_ALERT,
    SSL_CB_READ_ALERT, SSL_CB_WRITE_ALERT, SSL_CB_ACCEPT_LOOP,
    SSL_CB_ACCEPT_EXIT, SSL_CB_CONNECT_LOOP, SSL_CB_CONNECT_EXIT,
    SSL_CB_HANDSHAKE_START, SSL_CB_HANDSHAKE_DONE)

try:
    from OpenSSL.SSL import (
        SSL_ST_INIT, SSL_ST_BEFORE, SSL_ST_OK, SSL_ST_RENEGOTIATE
    )
except ImportError:
    SSL_ST_INIT = SSL_ST_BEFORE = SSL_ST_OK = SSL_ST_RENEGOTIATE = None

from .util import WARNING_TYPE_EXPECTED, NON_ASCII, is_consistent_type
from .test_crypto import (
    cleartextCertificatePEM, cleartextPrivateKeyPEM,
    client_cert_pem, client_key_pem, server_cert_pem, server_key_pem,
    root_cert_pem)

dhparam = """\
-----BEGIN DH PARAMETERS-----
MIGHAoGBALdUMvn+C9MM+y5BWZs11mSeH6HHoEq0UVbzVq7UojC1hbsZUuGukQ3a
Qh2/pwqb18BZFykrWB0zv/OkLa0kx4cuUgNrUVq1EFheBiX6YqryJ7t2sO09NQiO
V7H54LmltOT/hEh6QWsJqb6BQgH65bswvV/XkYGja8/T0GzvbaVzAgEC
-----END DH PARAMETERS-----
"""

skip_if_py3 = pytest.mark.skipif(PY3, reason="Python 2 only")
skip_if_py26 = pytest.mark.skipif(
    version_info[0:2] == (2, 6),
    reason="Python 2.7 and later only"
)

def join_bytes_or_unicode(prefix, suffix):

    if type(prefix) == type(suffix):
        return join(prefix, suffix)

    if isinstance(prefix, text_type):
        return join(prefix, suffix.decode(getfilesystemencoding()))
    else:
        return join(prefix, suffix.encode(getfilesystemencoding()))

def verify_cb(conn, cert, errnum, depth, ok):
    return ok

def socket_pair():

    port = socket()
    port.bind(('', 0))
    port.listen(1)
    client = socket()
    client.setblocking(False)
    client.connect_ex(("127.0.0.1", port.getsockname()[1]))
    client.setblocking(True)
    server = port.accept()[0]

    server.send(b"x")
    assert client.recv(1024) == b"x"
    client.send(b"y")
    assert server.recv(1024) == b"y"

    server.setblocking(False)
    client.setblocking(False)

    return (server, client)

def handshake(client, server):
    conns = [client, server]
    while conns:
        for conn in conns:
            try:
                conn.do_handshake()
            except WantReadError:
                pass
            else:
                conns.remove(conn)

def _create_certificate_chain():

    caext = X509Extension(b'basicConstraints', False, b'CA:true')

    cakey = PKey()
    cakey.generate_key(TYPE_RSA, 1024)
    cacert = X509()
    cacert.get_subject().commonName = "Authority Certificate"
    cacert.set_issuer(cacert.get_subject())
    cacert.set_pubkey(cakey)
    cacert.set_notBefore(b"20000101000000Z")
    cacert.set_notAfter(b"20200101000000Z")
    cacert.add_extensions([caext])
    cacert.set_serial_number(0)
    cacert.sign(cakey, "sha1")

    ikey = PKey()
    ikey.generate_key(TYPE_RSA, 1024)
    icert = X509()
    icert.get_subject().commonName = "Intermediate Certificate"
    icert.set_issuer(cacert.get_subject())
    icert.set_pubkey(ikey)
    icert.set_notBefore(b"20000101000000Z")
    icert.set_notAfter(b"20200101000000Z")
    icert.add_extensions([caext])
    icert.set_serial_number(0)
    icert.sign(cakey, "sha1")

    skey = PKey()
    skey.generate_key(TYPE_RSA, 1024)
    scert = X509()
    scert.get_subject().commonName = "Server Certificate"
    scert.set_issuer(icert.get_subject())
    scert.set_pubkey(skey)
    scert.set_notBefore(b"20000101000000Z")
    scert.set_notAfter(b"20200101000000Z")
    scert.add_extensions([
        X509Extension(b'basicConstraints', True, b'CA:false')])
    scert.set_serial_number(0)
    scert.sign(ikey, "sha1")

    return [(cakey, cacert), (ikey, icert), (skey, scert)]

def loopback_client_factory(socket):
    client = Connection(Context(SSLv23_METHOD), socket)
    client.set_connect_state()
    return client

def loopback_server_factory(socket):
    ctx = Context(SSLv23_METHOD)
    ctx.use_privatekey(load_privatekey(FILETYPE_PEM, server_key_pem))
    ctx.use_certificate(load_certificate(FILETYPE_PEM, server_cert_pem))
    server = Connection(ctx, socket)
    server.set_accept_state()
    return server

def loopback(server_factory=None, client_factory=None):

    if server_factory is None:
        server_factory = loopback_server_factory
    if client_factory is None:
        client_factory = loopback_client_factory

    (server, client) = socket_pair()
    server = server_factory(server)
    client = client_factory(client)

    handshake(client, server)

    server.setblocking(True)
    client.setblocking(True)
    return server, client

def interact_in_memory(client_conn, server_conn):

    wrote = True
    while wrote:
        wrote = False

        for (read, write) in [(client_conn, server_conn),
                              (server_conn, client_conn)]:

            try:
                data = read.recv(2 ** 16)
            except WantReadError:
                pass
            else:
                return (read, data)

            while True:
                try:
                    dirty = read.bio_read(4096)
                except WantReadError:
                    break
                else:
                    wrote = True
                    write.bio_write(dirty)

def handshake_in_memory(client_conn, server_conn):

    client_conn.set_connect_state()
    server_conn.set_accept_state()

    for conn in [client_conn, server_conn]:
        try:
            conn.do_handshake()
        except WantReadError:
            pass

    interact_in_memory(client_conn, server_conn)

class TestVersion(object):

    def test_OPENSSL_VERSION_NUMBER(self):

        assert isinstance(OPENSSL_VERSION_NUMBER, int)

    def test_SSLeay_version(self):

        versions = {}
        for t in [SSLEAY_VERSION, SSLEAY_CFLAGS, SSLEAY_BUILT_ON,
                  SSLEAY_PLATFORM, SSLEAY_DIR]:
            version = SSLeay_version(t)
            versions[version] = t
            assert isinstance(version, bytes)
        assert len(versions) == 5

@pytest.fixture
def ca_file(tmpdir):

    key = rsa.generate_private_key(
        public_exponent=65537,
        key_size=2048,
        backend=default_backend()
    )
    public_key = key.public_key()

    builder = x509.CertificateBuilder()
    builder = builder.subject_name(x509.Name([
        x509.NameAttribute(NameOID.COMMON_NAME, u"pyopenssl.org"),
    ]))
    builder = builder.issuer_name(x509.Name([
        x509.NameAttribute(NameOID.COMMON_NAME, u"pyopenssl.org"),
    ]))
    one_day = datetime.timedelta(1, 0, 0)
    builder = builder.not_valid_before(datetime.datetime.today() - one_day)
    builder = builder.not_valid_after(datetime.datetime.today() + one_day)
    builder = builder.serial_number(int(uuid.uuid4()))
    builder = builder.public_key(public_key)
    builder = builder.add_extension(
        x509.BasicConstraints(ca=True, path_length=None), critical=True,
    )

    certificate = builder.sign(
        private_key=key, algorithm=hashes.SHA256(),
        backend=default_backend()
    )

    ca_file = tmpdir.join("test.pem")
    ca_file.write_binary(
        certificate.public_bytes(
            encoding=serialization.Encoding.PEM,
        )
    )

    return str(ca_file).encode("ascii")

@pytest.fixture
def context():

    return Context(TLSv1_METHOD)

class TestContext(object):

    @pytest.mark.parametrize("cipher_string", [
        b"hello world:AES128-SHA",
        u"hello world:AES128-SHA",
    ])
    def test_set_cipher_list(self, context, cipher_string):

        context.set_cipher_list(cipher_string)
        conn = Connection(context, None)

        assert "AES128-SHA" in conn.get_cipher_list()

    @pytest.mark.parametrize("cipher_list,error", [
        (object(), TypeError),
        ("imaginary-cipher", Error),
    ])
    def test_set_cipher_list_wrong_args(self, context, cipher_list, error):

        with pytest.raises(error):
            context.set_cipher_list(cipher_list)

    def test_load_client_ca(self, context, ca_file):

        context.load_client_ca(ca_file)

    def test_load_client_ca_invalid(self, context, tmpdir):

        ca_file = tmpdir.join("test.pem")
        ca_file.write("")

        with pytest.raises(Error) as e:
            context.load_client_ca(str(ca_file).encode("ascii"))

        assert "PEM routines" == e.value.args[0][0][0]

    def test_load_client_ca_unicode(self, context, ca_file):

        pytest.deprecated_call(
            context.load_client_ca, ca_file.decode("ascii")
        )

    def test_set_session_id(self, context):

        context.set_session_id(b"abc")

    def test_set_session_id_fail(self, context):

        with pytest.raises(Error) as e:
            context.set_session_id(b"abc" * 1000)

        assert [
            ("SSL routines",
             "SSL_CTX_set_session_id_context",
             "ssl session id context too long")
        ] == e.value.args[0]

    def test_set_session_id_unicode(self, context):

        pytest.deprecated_call(context.set_session_id, u"abc")

    def test_method(self):

        methods = [SSLv23_METHOD, TLSv1_METHOD]
        for meth in methods:
            Context(meth)

        maybe = [SSLv2_METHOD, SSLv3_METHOD, TLSv1_1_METHOD, TLSv1_2_METHOD]
        for meth in maybe:
            try:
                Context(meth)
            except (Error, ValueError):
                pass

        with pytest.raises(TypeError):
            Context("")
        with pytest.raises(ValueError):
            Context(10)

    @skip_if_py3
    def test_method_long(self):

        Context(long(TLSv1_METHOD))

    def test_type(self):

        assert Context is ContextType
        assert is_consistent_type(Context, 'Context', TLSv1_METHOD)

    def test_use_privatekey(self):

        key = PKey()
        key.generate_key(TYPE_RSA, 128)
        ctx = Context(TLSv1_METHOD)
        ctx.use_privatekey(key)
        with pytest.raises(TypeError):
            ctx.use_privatekey("")

    def test_use_privatekey_file_missing(self, tmpfile):

        ctx = Context(TLSv1_METHOD)
        with pytest.raises(Error):
            ctx.use_privatekey_file(tmpfile)

    def _use_privatekey_file_test(self, pemfile, filetype):

        key = PKey()
        key.generate_key(TYPE_RSA, 128)

        with open(pemfile, "wt") as pem:
            pem.write(
                dump_privatekey(FILETYPE_PEM, key).decode("ascii")
            )

        ctx = Context(TLSv1_METHOD)
        ctx.use_privatekey_file(pemfile, filetype)

    @pytest.mark.parametrize('filetype', [object(), "", None, 1.0])
    def test_wrong_privatekey_file_wrong_args(self, tmpfile, filetype):

        ctx = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            ctx.use_privatekey_file(tmpfile, filetype)

    def test_use_privatekey_file_bytes(self, tmpfile):

        self._use_privatekey_file_test(
            tmpfile + NON_ASCII.encode(getfilesystemencoding()),
            FILETYPE_PEM,
        )

    def test_use_privatekey_file_unicode(self, tmpfile):

        self._use_privatekey_file_test(
            tmpfile.decode(getfilesystemencoding()) + NON_ASCII,
            FILETYPE_PEM,
        )

    @skip_if_py3
    def test_use_privatekey_file_long(self, tmpfile):

        self._use_privatekey_file_test(tmpfile, long(FILETYPE_PEM))

    def test_use_certificate_wrong_args(self):

        ctx = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            ctx.use_certificate("hello, world")

    def test_use_certificate_uninitialized(self):

        ctx = Context(TLSv1_METHOD)
        with pytest.raises(Error):
            ctx.use_certificate(X509())

    def test_use_certificate(self):

        ctx = Context(TLSv1_METHOD)
        ctx.use_certificate(
            load_certificate(FILETYPE_PEM, cleartextCertificatePEM)
        )

    def test_use_certificate_file_wrong_args(self):

        ctx = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            ctx.use_certificate_file(object(), FILETYPE_PEM)
        with pytest.raises(TypeError):
            ctx.use_certificate_file(b"somefile", object())
        with pytest.raises(TypeError):
            ctx.use_certificate_file(object(), FILETYPE_PEM)

    def test_use_certificate_file_missing(self, tmpfile):

        ctx = Context(TLSv1_METHOD)
        with pytest.raises(Error):
            ctx.use_certificate_file(tmpfile)

    def _use_certificate_file_test(self, certificate_file):

        with open(certificate_file, "wb") as pem_file:
            pem_file.write(cleartextCertificatePEM)

        ctx = Context(TLSv1_METHOD)
        ctx.use_certificate_file(certificate_file)

    def test_use_certificate_file_bytes(self, tmpfile):

        filename = tmpfile + NON_ASCII.encode(getfilesystemencoding())
        self._use_certificate_file_test(filename)

    def test_use_certificate_file_unicode(self, tmpfile):

        filename = tmpfile.decode(getfilesystemencoding()) + NON_ASCII
        self._use_certificate_file_test(filename)

    @skip_if_py3
    def test_use_certificate_file_long(self, tmpfile):

        pem_filename = tmpfile
        with open(pem_filename, "wb") as pem_file:
            pem_file.write(cleartextCertificatePEM)

        ctx = Context(TLSv1_METHOD)
        ctx.use_certificate_file(pem_filename, long(FILETYPE_PEM))

    def test_check_privatekey_valid(self):

        key = load_privatekey(FILETYPE_PEM, client_key_pem)
        cert = load_certificate(FILETYPE_PEM, client_cert_pem)
        context = Context(TLSv1_METHOD)
        context.use_privatekey(key)
        context.use_certificate(cert)
        assert None is context.check_privatekey()

    def test_check_privatekey_invalid(self):

        key = load_privatekey(FILETYPE_PEM, client_key_pem)
        cert = load_certificate(FILETYPE_PEM, server_cert_pem)
        context = Context(TLSv1_METHOD)
        context.use_privatekey(key)
        context.use_certificate(cert)
        with pytest.raises(Error):
            context.check_privatekey()

    def test_app_data(self):

        app_data = object()
        context = Context(TLSv1_METHOD)
        context.set_app_data(app_data)
        assert context.get_app_data() is app_data

    def test_set_options_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.set_options(None)

    def test_set_options(self):

        context = Context(TLSv1_METHOD)
        options = context.set_options(OP_NO_SSLv2)
        assert options & OP_NO_SSLv2 == OP_NO_SSLv2

    @skip_if_py3
    def test_set_options_long(self):

        context = Context(TLSv1_METHOD)
        options = context.set_options(long(OP_NO_SSLv2))
        assert options & OP_NO_SSLv2 == OP_NO_SSLv2

    def test_set_mode_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.set_mode(None)

    def test_set_mode(self):

        context = Context(TLSv1_METHOD)
        assert MODE_RELEASE_BUFFERS & context.set_mode(MODE_RELEASE_BUFFERS)

    @skip_if_py3
    def test_set_mode_long(self):

        context = Context(TLSv1_METHOD)
        mode = context.set_mode(long(MODE_RELEASE_BUFFERS))
        assert MODE_RELEASE_BUFFERS & mode

    def test_set_timeout_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.set_timeout(None)

    def test_timeout(self):

        context = Context(TLSv1_METHOD)
        context.set_timeout(1234)
        assert context.get_timeout() == 1234

    @skip_if_py3
    def test_timeout_long(self):

        context = Context(TLSv1_METHOD)
        context.set_timeout(long(1234))
        assert context.get_timeout() == 1234

    def test_set_verify_depth_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.set_verify_depth(None)

    def test_verify_depth(self):

        context = Context(TLSv1_METHOD)
        context.set_verify_depth(11)
        assert context.get_verify_depth() == 11

    @skip_if_py3
    def test_verify_depth_long(self):

        context = Context(TLSv1_METHOD)
        context.set_verify_depth(long(11))
        assert context.get_verify_depth() == 11

    def _write_encrypted_pem(self, passphrase, tmpfile):

        key = PKey()
        key.generate_key(TYPE_RSA, 128)
        pem = dump_privatekey(FILETYPE_PEM, key, "blowfish", passphrase)
        with open(tmpfile, 'w') as fObj:
            fObj.write(pem.decode('ascii'))
        return tmpfile

    def test_set_passwd_cb_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.set_passwd_cb(None)

    def test_set_passwd_cb(self, tmpfile):

        passphrase = b"foobar"
        pemFile = self._write_encrypted_pem(passphrase, tmpfile)
        calledWith = []

        def passphraseCallback(maxlen, verify, extra):
            calledWith.append((maxlen, verify, extra))
            return passphrase
        context = Context(TLSv1_METHOD)
        context.set_passwd_cb(passphraseCallback)
        context.use_privatekey_file(pemFile)
        assert len(calledWith) == 1
        assert isinstance(calledWith[0][0], int)
        assert isinstance(calledWith[0][1], int)
        assert calledWith[0][2] is None

    def test_passwd_callback_exception(self, tmpfile):

        pemFile = self._write_encrypted_pem(b"monkeys are nice", tmpfile)

        def passphraseCallback(maxlen, verify, extra):
            raise RuntimeError("Sorry, I am a fail.")

        context = Context(TLSv1_METHOD)
        context.set_passwd_cb(passphraseCallback)
        with pytest.raises(RuntimeError):
            context.use_privatekey_file(pemFile)

    def test_passwd_callback_false(self, tmpfile):

        pemFile = self._write_encrypted_pem(b"monkeys are nice", tmpfile)

        def passphraseCallback(maxlen, verify, extra):
            return b""

        context = Context(TLSv1_METHOD)
        context.set_passwd_cb(passphraseCallback)
        with pytest.raises(Error):
            context.use_privatekey_file(pemFile)

    def test_passwd_callback_non_string(self, tmpfile):

        pemFile = self._write_encrypted_pem(b"monkeys are nice", tmpfile)

        def passphraseCallback(maxlen, verify, extra):
            return 10

        context = Context(TLSv1_METHOD)
        context.set_passwd_cb(passphraseCallback)
        with pytest.raises(ValueError):
            context.use_privatekey_file(pemFile)

    def test_passwd_callback_too_long(self, tmpfile):

        passphrase = b"x" * 1024
        pemFile = self._write_encrypted_pem(passphrase, tmpfile)

        def passphraseCallback(maxlen, verify, extra):
            assert maxlen == 1024
            return passphrase + b"y"

        context = Context(TLSv1_METHOD)
        context.set_passwd_cb(passphraseCallback)
        context.use_privatekey_file(pemFile)

    def test_set_info_callback(self):

        (server, client) = socket_pair()

        clientSSL = Connection(Context(TLSv1_METHOD), client)
        clientSSL.set_connect_state()

        called = []

        def info(conn, where, ret):
            called.append((conn, where, ret))
        context = Context(TLSv1_METHOD)
        context.set_info_callback(info)
        context.use_certificate(
            load_certificate(FILETYPE_PEM, cleartextCertificatePEM))
        context.use_privatekey(
            load_privatekey(FILETYPE_PEM, cleartextPrivateKeyPEM))

        serverSSL = Connection(context, server)
        serverSSL.set_accept_state()

        handshake(clientSSL, serverSSL)

        notConnections = [
            conn for (conn, where, ret) in called
            if not isinstance(conn, Connection)]
        assert [] == notConnections, (
            "Some info callback arguments were not Connection instances.")

    def _load_verify_locations_test(self, *args):

        (server, client) = socket_pair()

        clientContext = Context(TLSv1_METHOD)
        clientContext.load_verify_locations(*args)
        clientContext.set_verify(
            VERIFY_PEER,
            lambda conn, cert, errno, depth, preverify_ok: preverify_ok)

        clientSSL = Connection(clientContext, client)
        clientSSL.set_connect_state()

        serverContext = Context(TLSv1_METHOD)
        serverContext.use_certificate(
            load_certificate(FILETYPE_PEM, cleartextCertificatePEM))
        serverContext.use_privatekey(
            load_privatekey(FILETYPE_PEM, cleartextPrivateKeyPEM))

        serverSSL = Connection(serverContext, server)
        serverSSL.set_accept_state()

        handshake(clientSSL, serverSSL)

        cert = clientSSL.get_peer_certificate()
        assert cert.get_subject().CN == 'Testing Root CA'

    def _load_verify_cafile(self, cafile):

        with open(cafile, 'w') as fObj:
            fObj.write(cleartextCertificatePEM.decode('ascii'))

        self._load_verify_locations_test(cafile)

    def test_load_verify_bytes_cafile(self, tmpfile):

        cafile = tmpfile + NON_ASCII.encode(getfilesystemencoding())
        self._load_verify_cafile(cafile)

    def test_load_verify_unicode_cafile(self, tmpfile):

        self._load_verify_cafile(
            tmpfile.decode(getfilesystemencoding()) + NON_ASCII
        )

    def test_load_verify_invalid_file(self, tmpfile):

        clientContext = Context(TLSv1_METHOD)
        with pytest.raises(Error):
            clientContext.load_verify_locations(tmpfile)

    def _load_verify_directory_locations_capath(self, capath):

        makedirs(capath)
        for name in [b'c7adac82.0', b'c3705638.0']:
            cafile = join_bytes_or_unicode(capath, name)
            with open(cafile, 'w') as fObj:
                fObj.write(cleartextCertificatePEM.decode('ascii'))

        self._load_verify_locations_test(None, capath)

    def test_load_verify_directory_bytes_capath(self, tmpfile):

        self._load_verify_directory_locations_capath(
            tmpfile + NON_ASCII.encode(getfilesystemencoding())
        )

    def test_load_verify_directory_unicode_capath(self, tmpfile):

        self._load_verify_directory_locations_capath(
            tmpfile.decode(getfilesystemencoding()) + NON_ASCII
        )

    def test_load_verify_locations_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.load_verify_locations(object())
        with pytest.raises(TypeError):
            context.load_verify_locations(object(), object())

    @pytest.mark.skipif(
        not platform.startswith("linux"),
        reason="Loading fallback paths is a linux-specific behavior to "
        "accommodate pyca/cryptography manylinux1 wheels"
    )
    def test_fallback_default_verify_paths(self, monkeypatch):

        context = Context(TLSv1_METHOD)
        monkeypatch.setattr(
            _lib, "SSL_CTX_set_default_verify_paths", lambda x: 1
        )
        monkeypatch.setattr(
            SSL,
            "_CRYPTOGRAPHY_MANYLINUX1_CA_FILE",
            _ffi.string(_lib.X509_get_default_cert_file())
        )
        monkeypatch.setattr(
            SSL,
            "_CRYPTOGRAPHY_MANYLINUX1_CA_DIR",
            _ffi.string(_lib.X509_get_default_cert_dir())
        )
        context.set_default_verify_paths()
        store = context.get_cert_store()
        sk_obj = _lib.X509_STORE_get0_objects(store._store)
        assert sk_obj != _ffi.NULL
        num = _lib.sk_X509_OBJECT_num(sk_obj)
        assert num != 0

    def test_check_env_vars(self, monkeypatch):

        context = Context(TLSv1_METHOD)
        dir_var = "CUSTOM_DIR_VAR"
        file_var = "CUSTOM_FILE_VAR"
        assert context._check_env_vars_set(dir_var, file_var) is False
        monkeypatch.setenv(dir_var, "value")
        monkeypatch.setenv(file_var, "value")
        assert context._check_env_vars_set(dir_var, file_var) is True
        assert context._check_env_vars_set(dir_var, file_var) is True

    def test_verify_no_fallback_if_env_vars_set(self, monkeypatch):

        context = Context(TLSv1_METHOD)
        monkeypatch.setattr(
            _lib, "SSL_CTX_set_default_verify_paths", lambda x: 1
        )
        dir_env_var = _ffi.string(
            _lib.X509_get_default_cert_dir_env()
        ).decode("ascii")
        file_env_var = _ffi.string(
            _lib.X509_get_default_cert_file_env()
        ).decode("ascii")
        monkeypatch.setenv(dir_env_var, "value")
        monkeypatch.setenv(file_env_var, "value")
        context.set_default_verify_paths()

        monkeypatch.setattr(
            context,
            "_fallback_default_verify_paths",
            raiser(SystemError)
        )
        context.set_default_verify_paths()

    @pytest.mark.skipif(
        platform == "win32",
        reason="set_default_verify_paths appears not to work on Windows.  "
        "See LP#404343 and LP#404344."
    )
    def test_set_default_verify_paths(self):

        context = Context(SSLv23_METHOD)
        context.set_default_verify_paths()
        context.set_verify(
            VERIFY_PEER,
            lambda conn, cert, errno, depth, preverify_ok: preverify_ok)

        client = socket()
        client.connect(("encrypted.google.com", 443))
        clientSSL = Connection(context, client)
        clientSSL.set_connect_state()
        clientSSL.do_handshake()
        clientSSL.send(b"GET / HTTP/1.0\r\n\r\n")
        assert clientSSL.recv(1024)

    def test_fallback_path_is_not_file_or_dir(self):

        context = Context(TLSv1_METHOD)
        context._fallback_default_verify_paths([], [])
        context._fallback_default_verify_paths(
            ["/not/a/file"], ["/not/a/dir"]
        )

    def test_add_extra_chain_cert_invalid_cert(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.add_extra_chain_cert(object())

    def _handshake_test(self, serverContext, clientContext):

        serverSocket, clientSocket = socket_pair()

        server = Connection(serverContext, serverSocket)
        server.set_accept_state()

        client = Connection(clientContext, clientSocket)
        client.set_connect_state()

        for _ in range(3):
            for s in [client, server]:
                try:
                    s.do_handshake()
                except WantReadError:
                    pass

    def test_set_verify_callback_connection_argument(self):

        serverContext = Context(TLSv1_METHOD)
        serverContext.use_privatekey(
            load_privatekey(FILETYPE_PEM, cleartextPrivateKeyPEM))
        serverContext.use_certificate(
            load_certificate(FILETYPE_PEM, cleartextCertificatePEM))
        serverConnection = Connection(serverContext, None)

        class VerifyCallback(object):
            def callback(self, connection, *args):
                self.connection = connection
                return 1

        verify = VerifyCallback()
        clientContext = Context(TLSv1_METHOD)
        clientContext.set_verify(VERIFY_PEER, verify.callback)
        clientConnection = Connection(clientContext, None)
        clientConnection.set_connect_state()

        handshake_in_memory(clientConnection, serverConnection)

        assert verify.connection is clientConnection

    def test_x509_in_verify_works(self):

        serverContext = Context(TLSv1_METHOD)
        serverContext.use_privatekey(
            load_privatekey(FILETYPE_PEM, cleartextPrivateKeyPEM))
        serverContext.use_certificate(
            load_certificate(FILETYPE_PEM, cleartextCertificatePEM))
        serverConnection = Connection(serverContext, None)

        def verify_cb_get_subject(conn, cert, errnum, depth, ok):
            assert cert.get_subject()
            return 1

        clientContext = Context(TLSv1_METHOD)
        clientContext.set_verify(VERIFY_PEER, verify_cb_get_subject)
        clientConnection = Connection(clientContext, None)
        clientConnection.set_connect_state()

        handshake_in_memory(clientConnection, serverConnection)

    def test_set_verify_callback_exception(self):

        serverContext = Context(TLSv1_METHOD)
        serverContext.use_privatekey(
            load_privatekey(FILETYPE_PEM, cleartextPrivateKeyPEM))
        serverContext.use_certificate(
            load_certificate(FILETYPE_PEM, cleartextCertificatePEM))

        clientContext = Context(TLSv1_METHOD)

        def verify_callback(*args):
            raise Exception("silly verify failure")
        clientContext.set_verify(VERIFY_PEER, verify_callback)

        with pytest.raises(Exception) as exc:
            self._handshake_test(serverContext, clientContext)

        assert "silly verify failure" == str(exc.value)

    def test_add_extra_chain_cert(self, tmpdir):

        chain = _create_certificate_chain()
        [(cakey, cacert), (ikey, icert), (skey, scert)] = chain

        for cert, name in [(cacert, 'ca.pem'),
                           (icert, 'i.pem'),
                           (scert, 's.pem')]:
            with tmpdir.join(name).open('w') as f:
                f.write(dump_certificate(FILETYPE_PEM, cert).decode('ascii'))

        for key, name in [(cakey, 'ca.key'),
                          (ikey, 'i.key'),
                          (skey, 's.key')]:
            with tmpdir.join(name).open('w') as f:
                f.write(dump_privatekey(FILETYPE_PEM, key).decode('ascii'))

        serverContext = Context(TLSv1_METHOD)
        serverContext.use_privatekey(skey)
        serverContext.use_certificate(scert)
        serverContext.add_extra_chain_cert(icert)

        clientContext = Context(TLSv1_METHOD)
        clientContext.set_verify(
            VERIFY_PEER | VERIFY_FAIL_IF_NO_PEER_CERT, verify_cb)
        clientContext.load_verify_locations(str(tmpdir.join("ca.pem")))

        self._handshake_test(serverContext, clientContext)

    def _use_certificate_chain_file_test(self, certdir):

        chain = _create_certificate_chain()
        [(cakey, cacert), (ikey, icert), (skey, scert)] = chain

        makedirs(certdir)

        chainFile = join_bytes_or_unicode(certdir, "chain.pem")
        caFile = join_bytes_or_unicode(certdir, "ca.pem")

        with open(chainFile, 'wb') as fObj:
            fObj.write(dump_certificate(FILETYPE_PEM, scert))
            fObj.write(dump_certificate(FILETYPE_PEM, icert))
            fObj.write(dump_certificate(FILETYPE_PEM, cacert))

        with open(caFile, 'w') as fObj:
            fObj.write(dump_certificate(FILETYPE_PEM, cacert).decode('ascii'))

        serverContext = Context(TLSv1_METHOD)
        serverContext.use_certificate_chain_file(chainFile)
        serverContext.use_privatekey(skey)

        clientContext = Context(TLSv1_METHOD)
        clientContext.set_verify(
            VERIFY_PEER | VERIFY_FAIL_IF_NO_PEER_CERT, verify_cb)
        clientContext.load_verify_locations(caFile)

        self._handshake_test(serverContext, clientContext)

    def test_use_certificate_chain_file_bytes(self, tmpfile):

        self._use_certificate_chain_file_test(
            tmpfile + NON_ASCII.encode(getfilesystemencoding())
        )

    def test_use_certificate_chain_file_unicode(self, tmpfile):

        self._use_certificate_chain_file_test(
            tmpfile.decode(getfilesystemencoding()) + NON_ASCII
        )

    def test_use_certificate_chain_file_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.use_certificate_chain_file(object())

    def test_use_certificate_chain_file_missing_file(self, tmpfile):

        context = Context(TLSv1_METHOD)
        with pytest.raises(Error):
            context.use_certificate_chain_file(tmpfile)

    def test_set_verify_mode(self):

        context = Context(TLSv1_METHOD)
        assert context.get_verify_mode() == 0
        context.set_verify(
            VERIFY_PEER | VERIFY_CLIENT_ONCE, lambda *args: None)
        assert context.get_verify_mode() == (VERIFY_PEER | VERIFY_CLIENT_ONCE)

    @skip_if_py3
    def test_set_verify_mode_long(self):

        context = Context(TLSv1_METHOD)
        assert context.get_verify_mode() == 0
        context.set_verify(
            long(VERIFY_PEER | VERIFY_CLIENT_ONCE), lambda *args: None
        )
        assert context.get_verify_mode() == (VERIFY_PEER | VERIFY_CLIENT_ONCE)

    @pytest.mark.parametrize('mode', [None, 1.0, object(), 'mode'])
    def test_set_verify_wrong_mode_arg(self, mode):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.set_verify(mode=mode, callback=lambda *args: None)

    @pytest.mark.parametrize('callback', [None, 1.0, 'mode', ('foo', 'bar')])
    def test_set_verify_wrong_callable_arg(self, callback):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.set_verify(mode=VERIFY_PEER, callback=callback)

    def test_load_tmp_dh_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.load_tmp_dh(object())

    def test_load_tmp_dh_missing_file(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(Error):
            context.load_tmp_dh(b"hello")

    def _load_tmp_dh_test(self, dhfilename):

        context = Context(TLSv1_METHOD)
        with open(dhfilename, "w") as dhfile:
            dhfile.write(dhparam)

        context.load_tmp_dh(dhfilename)

    def test_load_tmp_dh_bytes(self, tmpfile):

        self._load_tmp_dh_test(
            tmpfile + NON_ASCII.encode(getfilesystemencoding()),
        )

    def test_load_tmp_dh_unicode(self, tmpfile):

        self._load_tmp_dh_test(
            tmpfile.decode(getfilesystemencoding()) + NON_ASCII,
        )

    def test_set_tmp_ecdh(self):

        context = Context(TLSv1_METHOD)
        for curve in get_elliptic_curves():
            if curve.name.startswith(u"Oakley-"):
                continue
            context.set_tmp_ecdh(curve)

    def test_set_session_cache_mode_wrong_args(self):

        context = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            context.set_session_cache_mode(object())

    def test_session_cache_mode(self):

        context = Context(TLSv1_METHOD)
        context.set_session_cache_mode(SESS_CACHE_OFF)
        off = context.set_session_cache_mode(SESS_CACHE_BOTH)
        assert SESS_CACHE_OFF == off
        assert SESS_CACHE_BOTH == context.get_session_cache_mode()

    @skip_if_py3
    def test_session_cache_mode_long(self):

        context = Context(TLSv1_METHOD)
        context.set_session_cache_mode(long(SESS_CACHE_BOTH))
        assert SESS_CACHE_BOTH == context.get_session_cache_mode()

    def test_get_cert_store(self):

        context = Context(TLSv1_METHOD)
        store = context.get_cert_store()
        assert isinstance(store, X509Store)

class TestServerNameCallback(object):

    def test_old_callback_forgotten(self):

        def callback(connection):
            pass

        def replacement(connection):
            pass

        context = Context(TLSv1_METHOD)
        context.set_tlsext_servername_callback(callback)

        tracker = ref(callback)
        del callback

        context.set_tlsext_servername_callback(replacement)

        collect()
        collect()

        callback = tracker()
        if callback is not None:
            referrers = get_referrers(callback)
            if len(referrers) > 1:
                pytest.fail("Some references remain: %r" % (referrers,))

    def test_no_servername(self):

        args = []

        def servername(conn):
            args.append((conn, conn.get_servername()))
        context = Context(TLSv1_METHOD)
        context.set_tlsext_servername_callback(servername)

        del servername
        collect()

        context.use_privatekey(load_privatekey(FILETYPE_PEM, server_key_pem))
        context.use_certificate(
            load_certificate(FILETYPE_PEM, server_cert_pem))

        server = Connection(context, None)
        server.set_accept_state()

        client = Connection(Context(TLSv1_METHOD), None)
        client.set_connect_state()

        interact_in_memory(server, client)

        assert args == [(server, None)]

    def test_servername(self):

        args = []

        def servername(conn):
            args.append((conn, conn.get_servername()))
        context = Context(TLSv1_METHOD)
        context.set_tlsext_servername_callback(servername)

        context.use_privatekey(load_privatekey(FILETYPE_PEM, server_key_pem))
        context.use_certificate(
            load_certificate(FILETYPE_PEM, server_cert_pem))

        server = Connection(context, None)
        server.set_accept_state()

        client = Connection(Context(TLSv1_METHOD), None)
        client.set_connect_state()
        client.set_tlsext_host_name(b"foo1.example.com")

        interact_in_memory(server, client)

        assert args == [(server, b"foo1.example.com")]

class TestNextProtoNegotiation(object):

    def test_npn_success(self):

        advertise_args = []
        select_args = []

        def advertise(conn):
            advertise_args.append((conn,))
            return [b'http/1.1', b'spdy/2']

        def select(conn, options):
            select_args.append((conn, options))
            return b'spdy/2'

        server_context = Context(TLSv1_METHOD)
        server_context.set_npn_advertise_callback(advertise)

        client_context = Context(TLSv1_METHOD)
        client_context.set_npn_select_callback(select)

        server_context.use_privatekey(
            load_privatekey(FILETYPE_PEM, server_key_pem))
        server_context.use_certificate(
            load_certificate(FILETYPE_PEM, server_cert_pem))

        server = Connection(server_context, None)
        server.set_accept_state()

        client = Connection(client_context, None)
        client.set_connect_state()

        interact_in_memory(server, client)

        assert advertise_args == [(server,)]
        assert select_args == [(client, [b'http/1.1', b'spdy/2'])]

        assert server.get_next_proto_negotiated() == b'spdy/2'
        assert client.get_next_proto_negotiated() == b'spdy/2'

    def test_npn_client_fail(self):

        advertise_args = []
        select_args = []

        def advertise(conn):
            advertise_args.append((conn,))
            return [b'http/1.1', b'spdy/2']

        def select(conn, options):
            select_args.append((conn, options))
            return b''

        server_context = Context(TLSv1_METHOD)
        server_context.set_npn_advertise_callback(advertise)

        client_context = Context(TLSv1_METHOD)
        client_context.set_npn_select_callback(select)

        server_context.use_privatekey(
            load_privatekey(FILETYPE_PEM, server_key_pem))
        server_context.use_certificate(
            load_certificate(FILETYPE_PEM, server_cert_pem))

        server = Connection(server_context, None)
        server.set_accept_state()

        client = Connection(client_context, None)
        client.set_connect_state()

        with pytest.raises(Error):
            interact_in_memory(server, client)

        assert advertise_args == [(server,)]
        assert select_args == [(client, [b'http/1.1', b'spdy/2'])]

    def test_npn_select_error(self):

        advertise_args = []

        def advertise(conn):
            advertise_args.append((conn,))
            return [b'http/1.1', b'spdy/2']

        def select(conn, options):
            raise TypeError

        server_context = Context(TLSv1_METHOD)
        server_context.set_npn_advertise_callback(advertise)

        client_context = Context(TLSv1_METHOD)
        client_context.set_npn_select_callback(select)

        server_context.use_privatekey(
            load_privatekey(FILETYPE_PEM, server_key_pem))
        server_context.use_certificate(
            load_certificate(FILETYPE_PEM, server_cert_pem))

        server = Connection(server_context, None)
        server.set_accept_state()

        client = Connection(client_context, None)
        client.set_connect_state()

        with pytest.raises(TypeError):
            interact_in_memory(server, client)
        assert advertise_args == [(server,), ]

    def test_npn_advertise_error(self):

        select_args = []

        def advertise(conn):
            raise TypeError

        def select(conn, options):

            select_args.append((conn, options))
            return b''

        server_context = Context(TLSv1_METHOD)
        server_context.set_npn_advertise_callback(advertise)

        client_context = Context(TLSv1_METHOD)
        client_context.set_npn_select_callback(select)

        server_context.use_privatekey(
            load_privatekey(FILETYPE_PEM, server_key_pem))
        server_context.use_certificate(
            load_certificate(FILETYPE_PEM, server_cert_pem))

        server = Connection(server_context, None)
        server.set_accept_state()

        client = Connection(client_context, None)
        client.set_connect_state()

        with pytest.raises(TypeError):
            interact_in_memory(server, client)
        assert select_args == []

class TestApplicationLayerProtoNegotiation(object):

    if _lib.Cryptography_HAS_ALPN:

        def test_alpn_success(self):

            select_args = []

            def select(conn, options):
                select_args.append((conn, options))
                return b'spdy/2'

            client_context = Context(TLSv1_METHOD)
            client_context.set_alpn_protos([b'http/1.1', b'spdy/2'])

            server_context = Context(TLSv1_METHOD)
            server_context.set_alpn_select_callback(select)

            server_context.use_privatekey(
                load_privatekey(FILETYPE_PEM, server_key_pem))
            server_context.use_certificate(
                load_certificate(FILETYPE_PEM, server_cert_pem))

            server = Connection(server_context, None)
            server.set_accept_state()

            client = Connection(client_context, None)
            client.set_connect_state()

            interact_in_memory(server, client)

            assert select_args == [(server, [b'http/1.1', b'spdy/2'])]

            assert server.get_alpn_proto_negotiated() == b'spdy/2'
            assert client.get_alpn_proto_negotiated() == b'spdy/2'

        def test_alpn_set_on_connection(self):

            select_args = []

            def select(conn, options):
                select_args.append((conn, options))
                return b'spdy/2'

            client_context = Context(TLSv1_METHOD)

            server_context = Context(TLSv1_METHOD)
            server_context.set_alpn_select_callback(select)

            server_context.use_privatekey(
                load_privatekey(FILETYPE_PEM, server_key_pem))
            server_context.use_certificate(
                load_certificate(FILETYPE_PEM, server_cert_pem))

            server = Connection(server_context, None)
            server.set_accept_state()

            client = Connection(client_context, None)
            client.set_alpn_protos([b'http/1.1', b'spdy/2'])
            client.set_connect_state()

            interact_in_memory(server, client)

            assert select_args == [(server, [b'http/1.1', b'spdy/2'])]

            assert server.get_alpn_proto_negotiated() == b'spdy/2'
            assert client.get_alpn_proto_negotiated() == b'spdy/2'

        def test_alpn_server_fail(self):

            select_args = []

            def select(conn, options):
                select_args.append((conn, options))
                return b''

            client_context = Context(TLSv1_METHOD)
            client_context.set_alpn_protos([b'http/1.1', b'spdy/2'])

            server_context = Context(TLSv1_METHOD)
            server_context.set_alpn_select_callback(select)

            server_context.use_privatekey(
                load_privatekey(FILETYPE_PEM, server_key_pem))
            server_context.use_certificate(
                load_certificate(FILETYPE_PEM, server_cert_pem))

            server = Connection(server_context, None)
            server.set_accept_state()

            client = Connection(client_context, None)
            client.set_connect_state()

            with pytest.raises(Error):
                interact_in_memory(server, client)

            assert select_args == [(server, [b'http/1.1', b'spdy/2'])]

        def test_alpn_no_server(self):

            client_context = Context(TLSv1_METHOD)
            client_context.set_alpn_protos([b'http/1.1', b'spdy/2'])

            server_context = Context(TLSv1_METHOD)

            server_context.use_privatekey(
                load_privatekey(FILETYPE_PEM, server_key_pem))
            server_context.use_certificate(
                load_certificate(FILETYPE_PEM, server_cert_pem))

            server = Connection(server_context, None)
            server.set_accept_state()

            client = Connection(client_context, None)
            client.set_connect_state()

            interact_in_memory(server, client)

            assert client.get_alpn_proto_negotiated() == b''

        def test_alpn_callback_exception(self):

            select_args = []

            def select(conn, options):
                select_args.append((conn, options))
                raise TypeError()

            client_context = Context(TLSv1_METHOD)
            client_context.set_alpn_protos([b'http/1.1', b'spdy/2'])

            server_context = Context(TLSv1_METHOD)
            server_context.set_alpn_select_callback(select)

            server_context.use_privatekey(
                load_privatekey(FILETYPE_PEM, server_key_pem))
            server_context.use_certificate(
                load_certificate(FILETYPE_PEM, server_cert_pem))

            server = Connection(server_context, None)
            server.set_accept_state()

            client = Connection(client_context, None)
            client.set_connect_state()

            with pytest.raises(TypeError):
                interact_in_memory(server, client)
            assert select_args == [(server, [b'http/1.1', b'spdy/2'])]

    else:
        def test_alpn_not_implemented(self):

            context = Context(TLSv1_METHOD)
            with pytest.raises(NotImplementedError):
                context.set_alpn_protos(None)
            with pytest.raises(NotImplementedError):
                context.set_alpn_select_callback(None)

            conn = Connection(context)
            with pytest.raises(NotImplementedError):
                conn.set_alpn_protos(None)

class TestSession(object):

    def test_construction(self):

        new_session = Session()
        assert isinstance(new_session, Session)

class TestConnection(object):


    def test_type(self):

        assert Connection is ConnectionType
        ctx = Context(TLSv1_METHOD)
        assert is_consistent_type(Connection, 'Connection', ctx, None)

    @pytest.mark.parametrize('bad_context', [object(), 'context', None, 1])
    def test_wrong_args(self, bad_context):

        with pytest.raises(TypeError):
            Connection(bad_context)

    def test_get_context(self):

        context = Context(TLSv1_METHOD)
        connection = Connection(context, None)
        assert connection.get_context() is context

    def test_set_context_wrong_args(self):

        ctx = Context(TLSv1_METHOD)
        connection = Connection(ctx, None)
        with pytest.raises(TypeError):
            connection.set_context(object())
        with pytest.raises(TypeError):
            connection.set_context("hello")
        with pytest.raises(TypeError):
            connection.set_context(1)
        assert ctx is connection.get_context()

    def test_set_context(self):

        original = Context(SSLv23_METHOD)
        replacement = Context(TLSv1_METHOD)
        connection = Connection(original, None)
        connection.set_context(replacement)
        assert replacement is connection.get_context()
        del original, replacement
        collect()

    def test_set_tlsext_host_name_wrong_args(self):

        conn = Connection(Context(TLSv1_METHOD), None)
        with pytest.raises(TypeError):
            conn.set_tlsext_host_name(object())
        with pytest.raises(TypeError):
            conn.set_tlsext_host_name(b"with\0null")

        if PY3:
            with pytest.raises(TypeError):
                conn.set_tlsext_host_name(b"example.com".decode("ascii"))

    def test_pending(self):

        connection = Connection(Context(TLSv1_METHOD), None)
        assert connection.pending() == 0

    def test_peek(self):

        server, client = loopback()
        server.send(b'xy')
        assert client.recv(2, MSG_PEEK) == b'xy'
        assert client.recv(2, MSG_PEEK) == b'xy'
        assert client.recv(2) == b'xy'

    def test_connect_wrong_args(self):

        connection = Connection(Context(TLSv1_METHOD), socket())
        with pytest.raises(TypeError):
            connection.connect(None)

    def test_connect_refused(self):

        client = socket()
        context = Context(TLSv1_METHOD)
        clientSSL = Connection(context, client)
        try:
            clientSSL.connect(("127.0.0.1", 1))
        except error as e:
            exc = e
        assert exc.args[0] == ECONNREFUSED

    def test_connect(self):

        port = socket()
        port.bind(('', 0))
        port.listen(3)

        clientSSL = Connection(Context(TLSv1_METHOD), socket())
        clientSSL.connect(('127.0.0.1', port.getsockname()[1]))

    @pytest.mark.skipif(
        platform == "darwin",
        reason="connect_ex sometimes causes a kernel panic on OS X 10.6.4"
    )
    def test_connect_ex(self):

        port = socket()
        port.bind(('', 0))
        port.listen(3)

        clientSSL = Connection(Context(TLSv1_METHOD), socket())
        clientSSL.setblocking(False)
        result = clientSSL.connect_ex(port.getsockname())
        expected = (EINPROGRESS, EWOULDBLOCK)
        assert result in expected

    def test_accept(self):

        ctx = Context(TLSv1_METHOD)
        ctx.use_privatekey(load_privatekey(FILETYPE_PEM, server_key_pem))
        ctx.use_certificate(load_certificate(FILETYPE_PEM, server_cert_pem))
        port = socket()
        portSSL = Connection(ctx, port)
        portSSL.bind(('', 0))
        portSSL.listen(3)

        clientSSL = Connection(Context(TLSv1_METHOD), socket())

        clientSSL.connect(('127.0.0.1', portSSL.getsockname()[1]))

        serverSSL, address = portSSL.accept()

        assert isinstance(serverSSL, Connection)
        assert serverSSL.get_context() is ctx
        assert address == clientSSL.getsockname()

    def test_shutdown_wrong_args(self):

        connection = Connection(Context(TLSv1_METHOD), None)
        with pytest.raises(TypeError):
            connection.set_shutdown(None)

    def test_shutdown(self):

        server, client = loopback()
        assert not server.shutdown()
        assert server.get_shutdown() == SENT_SHUTDOWN
        with pytest.raises(ZeroReturnError):
            client.recv(1024)
        assert client.get_shutdown() == RECEIVED_SHUTDOWN
        client.shutdown()
        assert client.get_shutdown() == (SENT_SHUTDOWN | RECEIVED_SHUTDOWN)
        with pytest.raises(ZeroReturnError):
            server.recv(1024)
        assert server.get_shutdown() == (SENT_SHUTDOWN | RECEIVED_SHUTDOWN)

    def test_shutdown_closed(self):

        server, client = loopback()
        server.sock_shutdown(2)
        with pytest.raises(SysCallError) as exc:
            server.shutdown()
        if platform == "win32":
            assert exc.value.args[0] == ESHUTDOWN
        else:
            assert exc.value.args[0] == EPIPE

    def test_shutdown_truncated(self):

        server_ctx = Context(TLSv1_METHOD)
        client_ctx = Context(TLSv1_METHOD)
        server_ctx.use_privatekey(
            load_privatekey(FILETYPE_PEM, server_key_pem))
        server_ctx.use_certificate(
            load_certificate(FILETYPE_PEM, server_cert_pem))
        server = Connection(server_ctx, None)
        client = Connection(client_ctx, None)
        handshake_in_memory(client, server)
        assert not server.shutdown()
        with pytest.raises(WantReadError):
            server.shutdown()
        server.bio_shutdown()
        with pytest.raises(Error):
            server.shutdown()

    def test_set_shutdown(self):

        connection = Connection(Context(TLSv1_METHOD), socket())
        connection.set_shutdown(RECEIVED_SHUTDOWN)
        assert connection.get_shutdown() == RECEIVED_SHUTDOWN

    @skip_if_py3
    def test_set_shutdown_long(self):

        connection = Connection(Context(TLSv1_METHOD), socket())
        connection.set_shutdown(long(RECEIVED_SHUTDOWN))
        assert connection.get_shutdown() == RECEIVED_SHUTDOWN

    def test_state_string(self):

        server, client = socket_pair()
        server = loopback_server_factory(server)
        client = loopback_client_factory(client)

        assert server.get_state_string() in [
            b"before/accept initialization", b"before SSL initialization"
        ]
        assert client.get_state_string() in [
            b"before/connect initialization", b"before SSL initialization"
        ]

    def test_app_data(self):

        conn = Connection(Context(TLSv1_METHOD), None)
        assert None is conn.get_app_data()
        app_data = object()
        conn.set_app_data(app_data)
        assert conn.get_app_data() is app_data

    def test_makefile(self):

        conn = Connection(Context(TLSv1_METHOD), None)
        with pytest.raises(NotImplementedError):
            conn.makefile()

    def test_get_peer_cert_chain(self):

        chain = _create_certificate_chain()
        [(cakey, cacert), (ikey, icert), (skey, scert)] = chain

        serverContext = Context(TLSv1_METHOD)
        serverContext.use_privatekey(skey)
        serverContext.use_certificate(scert)
        serverContext.add_extra_chain_cert(icert)
        serverContext.add_extra_chain_cert(cacert)
        server = Connection(serverContext, None)
        server.set_accept_state()

        clientContext = Context(TLSv1_METHOD)
        clientContext.set_verify(VERIFY_NONE, verify_cb)
        client = Connection(clientContext, None)
        client.set_connect_state()

        interact_in_memory(client, server)

        chain = client.get_peer_cert_chain()
        assert len(chain) == 3
        assert "Server Certificate" == chain[0].get_subject().CN
        assert "Intermediate Certificate" == chain[1].get_subject().CN
        assert "Authority Certificate" == chain[2].get_subject().CN

    def test_get_peer_cert_chain_none(self):

        ctx = Context(TLSv1_METHOD)
        ctx.use_privatekey(load_privatekey(FILETYPE_PEM, server_key_pem))
        ctx.use_certificate(load_certificate(FILETYPE_PEM, server_cert_pem))
        server = Connection(ctx, None)
        server.set_accept_state()
        client = Connection(Context(TLSv1_METHOD), None)
        client.set_connect_state()
        interact_in_memory(client, server)
        assert None is server.get_peer_cert_chain()

    def test_get_session_unconnected(self):

        ctx = Context(TLSv1_METHOD)
        server = Connection(ctx, None)
        session = server.get_session()
        assert None is session

    def test_server_get_session(self):

        server, client = loopback()
        session = server.get_session()
        assert isinstance(session, Session)

    def test_client_get_session(self):

        server, client = loopback()
        session = client.get_session()
        assert isinstance(session, Session)

    def test_set_session_wrong_args(self):

        ctx = Context(TLSv1_METHOD)
        connection = Connection(ctx, None)
        with pytest.raises(TypeError):
            connection.set_session(123)
        with pytest.raises(TypeError):
            connection.set_session("hello")
        with pytest.raises(TypeError):
            connection.set_session(object())

    def test_client_set_session(self):

        key = load_privatekey(FILETYPE_PEM, server_key_pem)
        cert = load_certificate(FILETYPE_PEM, server_cert_pem)
        ctx = Context(SSLv23_METHOD)
        ctx.use_privatekey(key)
        ctx.use_certificate(cert)
        ctx.set_session_id("unity-test")

        def makeServer(socket):
            server = Connection(ctx, socket)
            server.set_accept_state()
            return server

        originalServer, originalClient = loopback(
            server_factory=makeServer)
        originalSession = originalClient.get_session()

        def makeClient(socket):
            client = loopback_client_factory(socket)
            client.set_session(originalSession)
            return client
        resumedServer, resumedClient = loopback(
            server_factory=makeServer,
            client_factory=makeClient)

        assert originalServer.master_key() == resumedServer.master_key()

    def test_set_session_wrong_method(self):

        if SSL_ST_INIT is None:
            v1 = TLSv1_2_METHOD
            v2 = TLSv1_METHOD
        elif hasattr(_lib, "SSLv3_method"):
            v1 = TLSv1_METHOD
            v2 = SSLv3_METHOD
        else:
            pytest.skip("Test requires either OpenSSL 1.1.0 or SSLv3")

        key = load_privatekey(FILETYPE_PEM, server_key_pem)
        cert = load_certificate(FILETYPE_PEM, server_cert_pem)
        ctx = Context(v1)
        ctx.use_privatekey(key)
        ctx.use_certificate(cert)
        ctx.set_session_id("unity-test")

        def makeServer(socket):
            server = Connection(ctx, socket)
            server.set_accept_state()
            return server

        def makeOriginalClient(socket):
            client = Connection(Context(v1), socket)
            client.set_connect_state()
            return client

        originalServer, originalClient = loopback(
            server_factory=makeServer, client_factory=makeOriginalClient)
        originalSession = originalClient.get_session()

        def makeClient(socket):
            client = Connection(Context(v2), socket)
            client.set_connect_state()
            client.set_session(originalSession)
            return client

        with pytest.raises(Error):
            loopback(client_factory=makeClient, server_factory=makeServer)

    def test_wantWriteError(self):

        client_socket, server_socket = socket_pair()
        msg = b"x"
        for i in range(1024 * 1024 * 4):
            try:
                client_socket.send(msg)
            except error as e:
                if e.errno == EWOULDBLOCK:
                    break
                raise
        else:
            pytest.fail(
                "Failed to fill socket buffer, cannot test BIO want write")

        ctx = Context(TLSv1_METHOD)
        conn = Connection(ctx, client_socket)
        conn.set_connect_state()
        with pytest.raises(WantWriteError):
            conn.do_handshake()

    def test_get_finished_before_connect(self):

        ctx = Context(TLSv1_METHOD)
        connection = Connection(ctx, None)
        assert connection.get_finished() is None

    def test_get_peer_finished_before_connect(self):

        ctx = Context(TLSv1_METHOD)
        connection = Connection(ctx, None)
        assert connection.get_peer_finished() is None

    def test_get_finished(self):

        server, client = loopback()

        assert server.get_finished() is not None
        assert len(server.get_finished()) > 0

    def test_get_peer_finished(self):

        server, client = loopback()

        assert server.get_peer_finished() is not None
        assert len(server.get_peer_finished()) > 0

    def test_tls_finished_message_symmetry(self):

        server, client = loopback()

        assert server.get_finished() == client.get_peer_finished()
        assert client.get_finished() == server.get_peer_finished()

    def test_get_cipher_name_before_connect(self):

        ctx = Context(TLSv1_METHOD)
        conn = Connection(ctx, None)
        assert conn.get_cipher_name() is None

    def test_get_cipher_name(self):

        server, client = loopback()
        server_cipher_name, client_cipher_name = \
            server.get_cipher_name(), client.get_cipher_name()

        assert isinstance(server_cipher_name, text_type)
        assert isinstance(client_cipher_name, text_type)

        assert server_cipher_name == client_cipher_name

    def test_get_cipher_version_before_connect(self):

        ctx = Context(TLSv1_METHOD)
        conn = Connection(ctx, None)
        assert conn.get_cipher_version() is None

    def test_get_cipher_version(self):

        server, client = loopback()
        server_cipher_version, client_cipher_version = \
            server.get_cipher_version(), client.get_cipher_version()

        assert isinstance(server_cipher_version, text_type)
        assert isinstance(client_cipher_version, text_type)

        assert server_cipher_version == client_cipher_version

    def test_get_cipher_bits_before_connect(self):

        ctx = Context(TLSv1_METHOD)
        conn = Connection(ctx, None)
        assert conn.get_cipher_bits() is None

    def test_get_cipher_bits(self):

        server, client = loopback()
        server_cipher_bits, client_cipher_bits = \
            server.get_cipher_bits(), client.get_cipher_bits()

        assert isinstance(server_cipher_bits, int)
        assert isinstance(client_cipher_bits, int)

        assert server_cipher_bits == client_cipher_bits

    def test_get_protocol_version_name(self):

        server, client = loopback()
        client_protocol_version_name = client.get_protocol_version_name()
        server_protocol_version_name = server.get_protocol_version_name()

        assert isinstance(server_protocol_version_name, text_type)
        assert isinstance(client_protocol_version_name, text_type)

        assert server_protocol_version_name == client_protocol_version_name

    def test_get_protocol_version(self):

        server, client = loopback()
        client_protocol_version = client.get_protocol_version()
        server_protocol_version = server.get_protocol_version()

        assert isinstance(server_protocol_version, int)
        assert isinstance(client_protocol_version, int)

        assert server_protocol_version == client_protocol_version

    def test_wantReadError(self):

        ctx = Context(TLSv1_METHOD)
        conn = Connection(ctx, None)
        with pytest.raises(WantReadError):
            conn.bio_read(1024)

    @pytest.mark.parametrize('bufsize', [1.0, None, object(), 'bufsize'])
    def test_bio_read_wrong_args(self, bufsize):

        ctx = Context(TLSv1_METHOD)
        conn = Connection(ctx, None)
        with pytest.raises(TypeError):
            conn.bio_read(bufsize)

    def test_buffer_size(self):

        ctx = Context(TLSv1_METHOD)
        conn = Connection(ctx, None)
        conn.set_connect_state()
        try:
            conn.do_handshake()
        except WantReadError:
            pass
        data = conn.bio_read(2)
        assert 2 == len(data)

    @skip_if_py3
    def test_buffer_size_long(self):

        ctx = Context(TLSv1_METHOD)
        conn = Connection(ctx, None)
        conn.set_connect_state()
        try:
            conn.do_handshake()
        except WantReadError:
            pass
        data = conn.bio_read(long(2))
        assert 2 == len(data)

class TestConnectionGetCipherList(object):

    def test_result(self):

        connection = Connection(Context(TLSv1_METHOD), None)
        ciphers = connection.get_cipher_list()
        assert isinstance(ciphers, list)
        for cipher in ciphers:
            assert isinstance(cipher, str)

class VeryLarge(bytes):

    def __len__(self):
        return 2**31

class TestConnectionSend(object):

    def test_wrong_args(self):

        connection = Connection(Context(TLSv1_METHOD), None)
        with pytest.raises(TypeError):
            connection.send(object())

    def test_short_bytes(self):

        server, client = loopback()
        count = server.send(b'xy')
        assert count == 2
        assert client.recv(2) == b'xy'

    def test_text(self):

        server, client = loopback()
        with pytest.warns(DeprecationWarning) as w:
            simplefilter("always")
            count = server.send(b"xy".decode("ascii"))
            assert (
                "{0} for buf is no longer accepted, use bytes".format(
                    WARNING_TYPE_EXPECTED
                ) == str(w[-1].message))
        assert count == 2
        assert client.recv(2) == b'xy'

    @skip_if_py26
    def test_short_memoryview(self):

        server, client = loopback()
        count = server.send(memoryview(b'xy'))
        assert count == 2
        assert client.recv(2) == b'xy'

    @skip_if_py3
    def test_short_buffer(self):

        server, client = loopback()
        count = server.send(buffer(b'xy'))
        assert count == 2
        assert client.recv(2) == b'xy'

    @pytest.mark.skipif(
        sys.maxsize < 2**31,
        reason="sys.maxsize < 2**31 - test requires 64 bit"
    )
    def test_buf_too_large(self):

        connection = Connection(Context(TLSv1_METHOD), None)
        with pytest.raises(ValueError) as exc_info:
            connection.send(VeryLarge())
        exc_info.match(r"Cannot send more than .+ bytes at once")

def _make_memoryview(size):

    return memoryview(bytearray(size))

class TestConnectionRecvInto(object):

    def _no_length_test(self, factory):

        output_buffer = factory(5)

        server, client = loopback()
        server.send(b'xy')

        assert client.recv_into(output_buffer) == 2
        assert output_buffer == bytearray(b'xy\x00\x00\x00')

    def test_bytearray_no_length(self):

        self._no_length_test(bytearray)

    def _respects_length_test(self, factory):

        output_buffer = factory(10)

        server, client = loopback()
        server.send(b'abcdefghij')

        assert client.recv_into(output_buffer, 5) == 5
        assert output_buffer == bytearray(b'abcde\x00\x00\x00\x00\x00')

    def test_bytearray_respects_length(self):

        self._respects_length_test(bytearray)

    def _doesnt_overfill_test(self, factory):

        output_buffer = factory(5)

        server, client = loopback()
        server.send(b'abcdefghij')

        assert client.recv_into(output_buffer) == 5
        assert output_buffer == bytearray(b'abcde')
        rest = client.recv(5)
        assert b'fghij' == rest

    def test_bytearray_doesnt_overfill(self):

        self._doesnt_overfill_test(bytearray)

    def test_bytearray_really_doesnt_overfill(self):

        self._doesnt_overfill_test(bytearray)

    def test_peek(self):
        server, client = loopback()
        server.send(b'xy')

        for _ in range(2):
            output_buffer = bytearray(5)
            assert client.recv_into(output_buffer, flags=MSG_PEEK) == 2
            assert output_buffer == bytearray(b'xy\x00\x00\x00')

    @skip_if_py26
    def test_memoryview_no_length(self):

        self._no_length_test(_make_memoryview)

    @skip_if_py26
    def test_memoryview_respects_length(self):

        self._respects_length_test(_make_memoryview)

    @skip_if_py26
    def test_memoryview_doesnt_overfill(self):

        self._doesnt_overfill_test(_make_memoryview)

    @skip_if_py26
    def test_memoryview_really_doesnt_overfill(self):

        self._doesnt_overfill_test(_make_memoryview)

class TestConnectionSendall(object):

    def test_wrong_args(self):

        connection = Connection(Context(TLSv1_METHOD), None)
        with pytest.raises(TypeError):
            connection.sendall(object())

    def test_short(self):

        server, client = loopback()
        server.sendall(b'x')
        assert client.recv(1) == b'x'

    def test_text(self):

        server, client = loopback()
        with pytest.warns(DeprecationWarning) as w:
            simplefilter("always")
            server.sendall(b"x".decode("ascii"))
            assert (
                "{0} for buf is no longer accepted, use bytes".format(
                    WARNING_TYPE_EXPECTED
                ) == str(w[-1].message))
        assert client.recv(1) == b"x"

    @skip_if_py26
    def test_short_memoryview(self):

        server, client = loopback()
        server.sendall(memoryview(b'x'))
        assert client.recv(1) == b'x'

    @skip_if_py3
    def test_short_buffers(self):

        server, client = loopback()
        server.sendall(buffer(b'x'))
        assert client.recv(1) == b'x'

    def test_long(self):

        server, client = loopback()
        message = b'x' * (1024 * 32 - 1) + b'y'
        server.sendall(message)
        accum = []
        received = 0
        while received < len(message):
            data = client.recv(1024)
            accum.append(data)
            received += len(data)
        assert message == b''.join(accum)

    def test_closed(self):

        server, client = loopback()
        server.sock_shutdown(2)
        with pytest.raises(SysCallError) as err:
            server.sendall(b"hello, world")
        if platform == "win32":
            assert err.value.args[0] == ESHUTDOWN
        else:
            assert err.value.args[0] == EPIPE

class TestConnectionRenegotiate(object):

    def test_total_renegotiations(self):

        connection = Connection(Context(TLSv1_METHOD), None)
        assert connection.total_renegotiations() == 0

    def test_renegotiate(self):

        server, client = loopback()

        server.send(b"hello world")

        assert b"hello world" == client.recv(len(b"hello world"))

        assert 0 == server.total_renegotiations()
        assert False is server.renegotiate_pending()

        assert True is server.renegotiate()

        assert True is server.renegotiate_pending()

        server.setblocking(False)
        client.setblocking(False)

        client.do_handshake()
        server.do_handshake()

        assert 1 == server.total_renegotiations()
        while False is server.renegotiate_pending():
            pass

class TestError(object):

    def test_type(self):

        assert issubclass(Error, Exception)
        assert Error.__name__ == 'Error'

class TestConstants(object):

    @pytest.mark.skipif(
        OP_NO_QUERY_MTU is None,
        reason="OP_NO_QUERY_MTU unavailable - OpenSSL version may be too old"
    )
    def test_op_no_query_mtu(self):

        assert OP_NO_QUERY_MTU == 0x1000

    @pytest.mark.skipif(
        OP_COOKIE_EXCHANGE is None,
        reason="OP_COOKIE_EXCHANGE unavailable - "
        "OpenSSL version may be too old"
    )
    def test_op_cookie_exchange(self):

        assert OP_COOKIE_EXCHANGE == 0x2000

    @pytest.mark.skipif(
        OP_NO_TICKET is None,
        reason="OP_NO_TICKET unavailable - OpenSSL version may be too old"
    )
    def test_op_no_ticket(self):

        assert OP_NO_TICKET == 0x4000

    @pytest.mark.skipif(
        OP_NO_COMPRESSION is None,
        reason="OP_NO_COMPRESSION unavailable - OpenSSL version may be too old"
    )
    def test_op_no_compression(self):

        assert OP_NO_COMPRESSION == 0x20000

    def test_sess_cache_off(self):

        assert 0x0 == SESS_CACHE_OFF

    def test_sess_cache_client(self):

        assert 0x1 == SESS_CACHE_CLIENT

    def test_sess_cache_server(self):

        assert 0x2 == SESS_CACHE_SERVER

    def test_sess_cache_both(self):

        assert 0x3 == SESS_CACHE_BOTH

    def test_sess_cache_no_auto_clear(self):

        assert 0x80 == SESS_CACHE_NO_AUTO_CLEAR

    def test_sess_cache_no_internal_lookup(self):

        assert 0x100 == SESS_CACHE_NO_INTERNAL_LOOKUP

    def test_sess_cache_no_internal_store(self):

        assert 0x200 == SESS_CACHE_NO_INTERNAL_STORE

    def test_sess_cache_no_internal(self):

        assert 0x300 == SESS_CACHE_NO_INTERNAL

class TestMemoryBIO(object):

    def _server(self, sock):

        server_ctx = Context(TLSv1_METHOD)
        server_ctx.set_options(OP_NO_SSLv2 | OP_NO_SSLv3 | OP_SINGLE_DH_USE)
        server_ctx.set_verify(
            VERIFY_PEER | VERIFY_FAIL_IF_NO_PEER_CERT | VERIFY_CLIENT_ONCE,
            verify_cb
        )
        server_store = server_ctx.get_cert_store()
        server_ctx.use_privatekey(
            load_privatekey(FILETYPE_PEM, server_key_pem))
        server_ctx.use_certificate(
            load_certificate(FILETYPE_PEM, server_cert_pem))
        server_ctx.check_privatekey()
        server_store.add_cert(load_certificate(FILETYPE_PEM, root_cert_pem))
        server_conn = Connection(server_ctx, sock)
        server_conn.set_accept_state()
        return server_conn

    def _client(self, sock):

        client_ctx = Context(TLSv1_METHOD)
        client_ctx.set_options(OP_NO_SSLv2 | OP_NO_SSLv3 | OP_SINGLE_DH_USE)
        client_ctx.set_verify(
            VERIFY_PEER | VERIFY_FAIL_IF_NO_PEER_CERT | VERIFY_CLIENT_ONCE,
            verify_cb
        )
        client_store = client_ctx.get_cert_store()
        client_ctx.use_privatekey(
            load_privatekey(FILETYPE_PEM, client_key_pem))
        client_ctx.use_certificate(
            load_certificate(FILETYPE_PEM, client_cert_pem))
        client_ctx.check_privatekey()
        client_store.add_cert(load_certificate(FILETYPE_PEM, root_cert_pem))
        client_conn = Connection(client_ctx, sock)
        client_conn.set_connect_state()
        return client_conn

    def test_memory_connect(self):

        server_conn = self._server(None)
        client_conn = self._client(None)

        assert server_conn.master_key() is None
        assert server_conn.client_random() is None
        assert server_conn.server_random() is None

        assert interact_in_memory(client_conn, server_conn) is None

        assert server_conn.master_key() is not None
        assert server_conn.client_random() is not None
        assert server_conn.server_random() is not None
        assert server_conn.client_random() == client_conn.client_random()
        assert server_conn.server_random() == client_conn.server_random()
        assert server_conn.client_random() != server_conn.server_random()
        assert client_conn.client_random() != client_conn.server_random()

        important_message = b'One if by land, two if by sea.'

        server_conn.write(important_message)
        assert (
            interact_in_memory(client_conn, server_conn) ==
            (client_conn, important_message))

        client_conn.write(important_message[::-1])
        assert (
            interact_in_memory(client_conn, server_conn) ==
            (server_conn, important_message[::-1]))

    def test_socket_connect(self):

        server_conn, client_conn = loopback()

        important_message = b"Help me Obi Wan Kenobi, you're my only hope."
        client_conn.send(important_message)
        msg = server_conn.recv(1024)
        assert msg == important_message

        important_message = important_message[::-1]
        server_conn.send(important_message)
        msg = client_conn.recv(1024)
        assert msg == important_message

    def test_socket_overrides_memory(self):

        context = Context(TLSv1_METHOD)
        client = socket()
        clientSSL = Connection(context, client)
        with pytest.raises(TypeError):
            clientSSL.bio_read(100)
        with pytest.raises(TypeError):
            clientSSL.bio_write("foo")
        with pytest.raises(TypeError):
            clientSSL.bio_shutdown()

    def test_outgoing_overflow(self):

        server = self._server(None)
        client = self._client(None)

        interact_in_memory(client, server)

        size = 2 ** 15
        sent = client.send(b"x" * size)
        assert sent < size

        receiver, received = interact_in_memory(client, server)
        assert receiver is server

        assert len(received) == sent

    def test_shutdown(self):

        server = self._server(None)
        server.bio_shutdown()
        with pytest.raises(Error) as err:
            server.recv(1024)
        assert type(err.value) in [Error, SysCallError]

    def test_unexpected_EOF(self):

        server_conn, client_conn = loopback()
        client_conn.sock_shutdown(SHUT_RDWR)
        with pytest.raises(SysCallError) as err:
            server_conn.recv(1024)
        assert err.value.args == (-1, "Unexpected EOF")

    def _check_client_ca_list(self, func):

        server = self._server(None)
        client = self._client(None)
        assert client.get_client_ca_list() == []
        assert server.get_client_ca_list() == []
        ctx = server.get_context()
        expected = func(ctx)
        assert client.get_client_ca_list() == []
        assert server.get_client_ca_list() == expected
        interact_in_memory(client, server)
        assert client.get_client_ca_list() == expected
        assert server.get_client_ca_list() == expected

    def test_set_client_ca_list_errors(self):

        ctx = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            ctx.set_client_ca_list("spam")
        with pytest.raises(TypeError):
            ctx.set_client_ca_list(["spam"])

    def test_set_empty_ca_list(self):

        def no_ca(ctx):
            ctx.set_client_ca_list([])
            return []
        self._check_client_ca_list(no_ca)

    def test_set_one_ca_list(self):

        cacert = load_certificate(FILETYPE_PEM, root_cert_pem)
        cadesc = cacert.get_subject()

        def single_ca(ctx):
            ctx.set_client_ca_list([cadesc])
            return [cadesc]
        self._check_client_ca_list(single_ca)

    def test_set_multiple_ca_list(self):

        secert = load_certificate(FILETYPE_PEM, server_cert_pem)
        clcert = load_certificate(FILETYPE_PEM, server_cert_pem)

        sedesc = secert.get_subject()
        cldesc = clcert.get_subject()

        def multiple_ca(ctx):
            L = [sedesc, cldesc]
            ctx.set_client_ca_list(L)
            return L
        self._check_client_ca_list(multiple_ca)

    def test_reset_ca_list(self):

        cacert = load_certificate(FILETYPE_PEM, root_cert_pem)
        secert = load_certificate(FILETYPE_PEM, server_cert_pem)
        clcert = load_certificate(FILETYPE_PEM, server_cert_pem)

        cadesc = cacert.get_subject()
        sedesc = secert.get_subject()
        cldesc = clcert.get_subject()

        def changed_ca(ctx):
            ctx.set_client_ca_list([sedesc, cldesc])
            ctx.set_client_ca_list([cadesc])
            return [cadesc]
        self._check_client_ca_list(changed_ca)

    def test_mutated_ca_list(self):

        cacert = load_certificate(FILETYPE_PEM, root_cert_pem)
        secert = load_certificate(FILETYPE_PEM, server_cert_pem)

        cadesc = cacert.get_subject()
        sedesc = secert.get_subject()

        def mutated_ca(ctx):
            L = [cadesc]
            ctx.set_client_ca_list([cadesc])
            L.append(sedesc)
            return [cadesc]
        self._check_client_ca_list(mutated_ca)

    def test_add_client_ca_wrong_args(self):

        ctx = Context(TLSv1_METHOD)
        with pytest.raises(TypeError):
            ctx.add_client_ca("spam")

    def test_one_add_client_ca(self):

        cacert = load_certificate(FILETYPE_PEM, root_cert_pem)
        cadesc = cacert.get_subject()

        def single_ca(ctx):
            ctx.add_client_ca(cacert)
            return [cadesc]
        self._check_client_ca_list(single_ca)

    def test_multiple_add_client_ca(self):

        cacert = load_certificate(FILETYPE_PEM, root_cert_pem)
        secert = load_certificate(FILETYPE_PEM, server_cert_pem)

        cadesc = cacert.get_subject()
        sedesc = secert.get_subject()

        def multiple_ca(ctx):
            ctx.add_client_ca(cacert)
            ctx.add_client_ca(secert)
            return [cadesc, sedesc]
        self._check_client_ca_list(multiple_ca)

    def test_set_and_add_client_ca(self):

        cacert = load_certificate(FILETYPE_PEM, root_cert_pem)
        secert = load_certificate(FILETYPE_PEM, server_cert_pem)
        clcert = load_certificate(FILETYPE_PEM, server_cert_pem)

        cadesc = cacert.get_subject()
        sedesc = secert.get_subject()
        cldesc = clcert.get_subject()

        def mixed_set_add_ca(ctx):
            ctx.set_client_ca_list([cadesc, sedesc])
            ctx.add_client_ca(clcert)
            return [cadesc, sedesc, cldesc]
        self._check_client_ca_list(mixed_set_add_ca)

    def test_set_after_add_client_ca(self):

        cacert = load_certificate(FILETYPE_PEM, root_cert_pem)
        secert = load_certificate(FILETYPE_PEM, server_cert_pem)
        clcert = load_certificate(FILETYPE_PEM, server_cert_pem)

        cadesc = cacert.get_subject()
        sedesc = secert.get_subject()

        def set_replaces_add_ca(ctx):
            ctx.add_client_ca(clcert)
            ctx.set_client_ca_list([cadesc])
            ctx.add_client_ca(secert)
            return [cadesc, sedesc]
        self._check_client_ca_list(set_replaces_add_ca)

class TestInfoConstants(object):

    def test_integers(self):

        for const in [
            SSL_ST_CONNECT, SSL_ST_ACCEPT, SSL_ST_MASK,
            SSL_CB_LOOP, SSL_CB_EXIT, SSL_CB_READ, SSL_CB_WRITE, SSL_CB_ALERT,
            SSL_CB_READ_ALERT, SSL_CB_WRITE_ALERT, SSL_CB_ACCEPT_LOOP,
            SSL_CB_ACCEPT_EXIT, SSL_CB_CONNECT_LOOP, SSL_CB_CONNECT_EXIT,
            SSL_CB_HANDSHAKE_START, SSL_CB_HANDSHAKE_DONE
        ]:
            assert isinstance(const, int)

        for const in [
            SSL_ST_INIT, SSL_ST_BEFORE, SSL_ST_OK, SSL_ST_RENEGOTIATE
        ]:
            assert const is None or isinstance(const, int)

class TestRequires(object):

    def test_available(self):

        feature_guard = _make_requires(True, "Error text")
        results = []

        @feature_guard
        def inner():
            results.append(True)
            return True

        assert inner() is True
        assert [True] == results

    def test_unavailable(self):

        feature_guard = _make_requires(False, "Error text")

        @feature_guard
        def inner():
            pytest.fail("Should not be called")

        with pytest.raises(NotImplementedError) as e:
            inner()

        assert "Error text" in str(e.value)

class TestOCSP(object):

    sample_ocsp_data = b"this is totally ocsp data"

    def _client_connection(self, callback, data, request_ocsp=True):

        ctx = Context(SSLv23_METHOD)
        ctx.set_ocsp_client_callback(callback, data)
        client = Connection(ctx)

        if request_ocsp:
            client.request_ocsp()

        client.set_connect_state()
        return client

    def _server_connection(self, callback, data):

        ctx = Context(SSLv23_METHOD)
        ctx.use_privatekey(load_privatekey(FILETYPE_PEM, server_key_pem))
        ctx.use_certificate(load_certificate(FILETYPE_PEM, server_cert_pem))
        ctx.set_ocsp_server_callback(callback, data)
        server = Connection(ctx)
        server.set_accept_state()
        return server

    def test_callbacks_arent_called_by_default(self):

        def ocsp_callback(*args, **kwargs):
            pytest.fail("Should not be called")

        client = self._client_connection(
            callback=ocsp_callback, data=None, request_ocsp=False
        )
        server = self._server_connection(callback=ocsp_callback, data=None)
        handshake_in_memory(client, server)

    def test_client_negotiates_without_server(self):

        called = []

        def ocsp_callback(conn, ocsp_data, ignored):
            called.append(ocsp_data)
            return True

        client = self._client_connection(callback=ocsp_callback, data=None)
        server = loopback_server_factory(socket=None)
        handshake_in_memory(client, server)

        assert len(called) == 1
        assert called[0] == b''

    def test_client_receives_servers_data(self):

        calls = []

        def server_callback(*args, **kwargs):
            return self.sample_ocsp_data

        def client_callback(conn, ocsp_data, ignored):
            calls.append(ocsp_data)
            return True

        client = self._client_connection(callback=client_callback, data=None)
        server = self._server_connection(callback=server_callback, data=None)
        handshake_in_memory(client, server)

        assert len(calls) == 1
        assert calls[0] == self.sample_ocsp_data

    def test_callbacks_are_invoked_with_connections(self):

        client_calls = []
        server_calls = []

        def client_callback(conn, *args, **kwargs):
            client_calls.append(conn)
            return True

        def server_callback(conn, *args, **kwargs):
            server_calls.append(conn)
            return self.sample_ocsp_data

        client = self._client_connection(callback=client_callback, data=None)
        server = self._server_connection(callback=server_callback, data=None)
        handshake_in_memory(client, server)

        assert len(client_calls) == 1
        assert len(server_calls) == 1
        assert client_calls[0] is client
        assert server_calls[0] is server

    def test_opaque_data_is_passed_through(self):

        calls = []

        def server_callback(*args):
            calls.append(args)
            return self.sample_ocsp_data

        def client_callback(*args):
            calls.append(args)
            return True

        sentinel = object()

        client = self._client_connection(
            callback=client_callback, data=sentinel
        )
        server = self._server_connection(
            callback=server_callback, data=sentinel
        )
        handshake_in_memory(client, server)

        assert len(calls) == 2
        assert calls[0][-1] is sentinel
        assert calls[1][-1] is sentinel

    def test_server_returns_empty_string(self):

        client_calls = []

        def server_callback(*args):
            return b''

        def client_callback(conn, ocsp_data, ignored):
            client_calls.append(ocsp_data)
            return True

        client = self._client_connection(callback=client_callback, data=None)
        server = self._server_connection(callback=server_callback, data=None)
        handshake_in_memory(client, server)

        assert len(client_calls) == 1
        assert client_calls[0] == b''

    def test_client_returns_false_terminates_handshake(self):

        def server_callback(*args):
            return self.sample_ocsp_data

        def client_callback(*args):
            return False

        client = self._client_connection(callback=client_callback, data=None)
        server = self._server_connection(callback=server_callback, data=None)

        with pytest.raises(Error):
            handshake_in_memory(client, server)

    def test_exceptions_in_client_bubble_up(self):

        class SentinelException(Exception):
            pass

        def server_callback(*args):
            return self.sample_ocsp_data

        def client_callback(*args):
            raise SentinelException()

        client = self._client_connection(callback=client_callback, data=None)
        server = self._server_connection(callback=server_callback, data=None)

        with pytest.raises(SentinelException):
            handshake_in_memory(client, server)

    def test_exceptions_in_server_bubble_up(self):

        class SentinelException(Exception):
            pass

        def server_callback(*args):
            raise SentinelException()

        def client_callback(*args):
            pytest.fail("Should not be called")

        client = self._client_connection(callback=client_callback, data=None)
        server = self._server_connection(callback=server_callback, data=None)

        with pytest.raises(SentinelException):
            handshake_in_memory(client, server)

    def test_server_must_return_bytes(self):

        def server_callback(*args):
            return self.sample_ocsp_data.decode('ascii')

        def client_callback(*args):
            pytest.fail("Should not be called")

        client = self._client_connection(callback=client_callback, data=None)
        server = self._server_connection(callback=server_callback, data=None)

        with pytest.raises(TypeError):
            handshake_in_memory(client, server)

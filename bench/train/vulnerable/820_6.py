

import zlib

from io import BytesIO

from zope.interface.verify import verifyObject

from twisted.trial.unittest import TestCase
from twisted.web import client, error, http_headers
from twisted.web._newclient import RequestNotSent, RequestTransmissionFailed
from twisted.web._newclient import ResponseNeverReceived, ResponseFailed
from twisted.web._newclient import PotentialDataLoss
from twisted.internet import defer, task
from twisted.python.failure import Failure
from twisted.python.compat import cookielib, intToBytes, unicode
from twisted.python.components import proxyForInterface
from twisted.test.proto_helpers import (StringTransport, MemoryReactorClock,
                                        EventLoggingObserver)
from twisted.internet.task import Clock
from twisted.internet.error import ConnectionRefusedError, ConnectionDone
from twisted.internet.error import ConnectionLost
from twisted.internet.protocol import Protocol, Factory
from twisted.internet.defer import Deferred, succeed, CancelledError
from twisted.internet.endpoints import TCP4ClientEndpoint
from twisted.internet.address import IPv4Address, IPv6Address

from twisted.web.client import (FileBodyProducer, Request, HTTPConnectionPool,
                                ResponseDone, _HTTP11ClientFactory, URI)

from twisted.web.iweb import (
    UNKNOWN_LENGTH, IAgent, IBodyProducer, IResponse, IAgentEndpointFactory,
    )
from twisted.web.http_headers import Headers
from twisted.web._newclient import HTTP11ClientProtocol, Response

from twisted.internet.interfaces import IOpenSSLClientConnectionCreator
from zope.interface.declarations import implementer
from twisted.web.iweb import IPolicyForHTTPS
from twisted.python.deprecate import getDeprecationWarningString
from incremental import Version
from twisted.web.client import (BrowserLikePolicyForHTTPS,
                                HostnameCachingHTTPSPolicy)
from twisted.internet.test.test_endpoints import deterministicResolvingReactor
from twisted.internet.endpoints import HostnameEndpoint
from twisted.test.proto_helpers import AccumulatingProtocol
from twisted.test.iosim import IOPump, FakeTransport
from twisted.test.test_sslverify import certificatesForAuthorityAndServer
from twisted.web.error import SchemeNotSupported
from twisted.logger import globalLogPublisher

try:
    from twisted.internet import ssl
except ImportError:
    ssl = None
    skipWhenNoSSL = "SSL not present, cannot run SSL tests."
    skipWhenSSLPresent = None
else:
    skipWhenSSLPresent = "SSL present."
    skipWhenNoSSL = None
    from twisted.internet._sslverify import ClientTLSOptions, IOpenSSLTrustRoot
    from twisted.internet.ssl import optionsForClientTLS
    from twisted.protocols.tls import TLSMemoryBIOProtocol, TLSMemoryBIOFactory

    @implementer(IOpenSSLTrustRoot)
    class CustomOpenSSLTrustRoot(object):
        called = False
        context = None

        def _addCACertsToContext(self, context):
            self.called = True
            self.context = context

class StubHTTPProtocol(Protocol):

    def __init__(self):
        self.requests = []
        self.state = 'QUIESCENT'

    def request(self, request):

        result = Deferred()
        self.requests.append((request, result))
        return result

class FileConsumer(object):
    def __init__(self, outputFile):
        self.outputFile = outputFile

    def write(self, bytes):
        self.outputFile.write(bytes)

class FileBodyProducerTests(TestCase):

    def _termination(self):

        return lambda: True

    def setUp(self):

        self._scheduled = []
        self.cooperator = task.Cooperator(
            self._termination, self._scheduled.append)

    def test_interface(self):

        self.assertTrue(verifyObject(
                IBodyProducer, FileBodyProducer(BytesIO(b""))))

    def test_unknownLength(self):

        class HasSeek(object):
            def seek(self, offset, whence):
                pass

        class HasTell(object):
            def tell(self):
                pass

        producer = FileBodyProducer(HasSeek())
        self.assertEqual(UNKNOWN_LENGTH, producer.length)
        producer = FileBodyProducer(HasTell())
        self.assertEqual(UNKNOWN_LENGTH, producer.length)

    def test_knownLength(self):

        inputBytes = b"here are some bytes"
        inputFile = BytesIO(inputBytes)
        inputFile.seek(5)
        producer = FileBodyProducer(inputFile)
        self.assertEqual(len(inputBytes) - 5, producer.length)
        self.assertEqual(inputFile.tell(), 5)

    def test_defaultCooperator(self):

        producer = FileBodyProducer(BytesIO(b""))
        self.assertEqual(task.cooperate, producer._cooperate)

    def test_startProducing(self):

        expectedResult = b"hello, world"
        readSize = 3
        output = BytesIO()
        consumer = FileConsumer(output)
        producer = FileBodyProducer(
            BytesIO(expectedResult), self.cooperator, readSize)
        complete = producer.startProducing(consumer)
        for i in range(len(expectedResult) // readSize + 1):
            self._scheduled.pop(0)()
        self.assertEqual([], self._scheduled)
        self.assertEqual(expectedResult, output.getvalue())
        self.assertEqual(None, self.successResultOf(complete))

    def test_inputClosedAtEOF(self):

        readSize = 4
        inputBytes = b"some friendly bytes"
        inputFile = BytesIO(inputBytes)
        producer = FileBodyProducer(inputFile, self.cooperator, readSize)
        consumer = FileConsumer(BytesIO())
        producer.startProducing(consumer)
        for i in range(len(inputBytes) // readSize + 2):
            self._scheduled.pop(0)()
        self.assertTrue(inputFile.closed)

    def test_failedReadWhileProducing(self):

        class BrokenFile(object):
            def read(self, count):
                raise IOError("Simulated bad thing")
        producer = FileBodyProducer(BrokenFile(), self.cooperator)
        complete = producer.startProducing(FileConsumer(BytesIO()))
        self._scheduled.pop(0)()
        self.failureResultOf(complete).trap(IOError)

    def test_stopProducing(self):

        expectedResult = b"hello, world"
        readSize = 3
        output = BytesIO()
        consumer = FileConsumer(output)
        inputFile = BytesIO(expectedResult)
        producer = FileBodyProducer(
            inputFile, self.cooperator, readSize)
        complete = producer.startProducing(consumer)
        producer.stopProducing()
        self.assertTrue(inputFile.closed)
        self._scheduled.pop(0)()
        self.assertEqual(b"", output.getvalue())
        self.assertNoResult(complete)

    def test_pauseProducing(self):

        expectedResult = b"hello, world"
        readSize = 5
        output = BytesIO()
        consumer = FileConsumer(output)
        producer = FileBodyProducer(
            BytesIO(expectedResult), self.cooperator, readSize)
        complete = producer.startProducing(consumer)
        self._scheduled.pop(0)()
        self.assertEqual(output.getvalue(), expectedResult[:5])
        producer.pauseProducing()

        self._scheduled.pop(0)()

        self.assertEqual(output.getvalue(), expectedResult[:5])
        self.assertEqual([], self._scheduled)
        self.assertNoResult(complete)

    def test_resumeProducing(self):

        expectedResult = b"hello, world"
        readSize = 5
        output = BytesIO()
        consumer = FileConsumer(output)
        producer = FileBodyProducer(
            BytesIO(expectedResult), self.cooperator, readSize)
        producer.startProducing(consumer)
        self._scheduled.pop(0)()
        self.assertEqual(expectedResult[:readSize], output.getvalue())
        producer.pauseProducing()
        producer.resumeProducing()
        self._scheduled.pop(0)()
        self.assertEqual(expectedResult[:readSize * 2], output.getvalue())

EXAMPLE_COM_IP = '127.0.0.7'
EXAMPLE_COM_V6_IP = '::7'
EXAMPLE_NET_IP = '127.0.0.8'
EXAMPLE_ORG_IP = '127.0.0.9'
FOO_LOCAL_IP = '127.0.0.10'
FOO_COM_IP = '127.0.0.11'

class FakeReactorAndConnectMixin:

    def createReactor(self):

        mrc = MemoryReactorClock()
        drr = deterministicResolvingReactor(mrc, hostMap={
            u'example.com': [EXAMPLE_COM_IP],
            u'ipv6.example.com': [EXAMPLE_COM_V6_IP],
            u'example.net': [EXAMPLE_NET_IP],
            u'example.org': [EXAMPLE_ORG_IP],
            u'foo': [FOO_LOCAL_IP],
            u'foo.com': [FOO_COM_IP],
            u'127.0.0.7': ['127.0.0.7'],
            u'::7': ['::7'],
        })

        drr.tcpClients = mrc.tcpClients
        drr.advance = mrc.advance
        return drr

    class StubEndpoint(object):


        def __init__(self, endpoint, testCase):
            self.endpoint = endpoint
            self.testCase = testCase
            def nothing():

            self.factory = _HTTP11ClientFactory(nothing,
                                                repr(self.endpoint))
            self.protocol = StubHTTPProtocol()
            self.factory.buildProtocol = lambda addr: self.protocol

        def connect(self, ignoredFactory):
            self.testCase.protocol = self.protocol
            self.endpoint.connect(self.factory)
            return succeed(self.protocol)

    def buildAgentForWrapperTest(self, reactor):

        agent = client.Agent(reactor)
        _oldGetEndpoint = agent._getEndpoint
        agent._getEndpoint = lambda *args: (
            self.StubEndpoint(_oldGetEndpoint(*args), self))
        return agent

    def connect(self, factory):

        protocol = StubHTTPProtocol()
        protocol.makeConnection(None)
        self.protocol = protocol
        return succeed(protocol)

class DummyEndpoint(object):


    def connect(self, factory):
        protocol = factory.buildProtocol(None)
        protocol.makeConnection(StringTransport())
        return succeed(protocol)

class BadEndpoint(object):


    def connect(self, factory):
        raise RuntimeError("This endpoint should not have been used.")

class DummyFactory(Factory):

    def __init__(self, quiescentCallback, metadata):
        pass

    protocol = StubHTTPProtocol

class HTTPConnectionPoolTests(TestCase, FakeReactorAndConnectMixin):

    def setUp(self):
        self.fakeReactor = self.createReactor()
        self.pool = HTTPConnectionPool(self.fakeReactor)
        self.pool._factory = DummyFactory
        self.pool.retryAutomatically = False

    def test_getReturnsNewIfCacheEmpty(self):

        self.assertEqual(self.pool._connections, {})

        def gotConnection(conn):
            self.assertIsInstance(conn, StubHTTPProtocol)
            self.assertNotIn(conn, self.pool._connections.values())

        unknownKey = 12245
        d = self.pool.getConnection(unknownKey, DummyEndpoint())
        return d.addCallback(gotConnection)

    def test_putStartsTimeout(self):

        protocol = StubHTTPProtocol()
        protocol.makeConnection(StringTransport())
        self.pool._putConnection(("http", b"example.com", 80), protocol)

        self.assertEqual(protocol.transport.disconnecting, False)
        self.assertIn(protocol,
                      self.pool._connections[("http", b"example.com", 80)])

        self.fakeReactor.advance(239)
        self.assertEqual(protocol.transport.disconnecting, False)
        self.assertIn(protocol,
                      self.pool._connections[("http", b"example.com", 80)])
        self.assertIn(protocol, self.pool._timeouts)

        self.fakeReactor.advance(1.1)
        self.assertEqual(protocol.transport.disconnecting, True)
        self.assertNotIn(protocol,
                         self.pool._connections[("http", b"example.com", 80)])
        self.assertNotIn(protocol, self.pool._timeouts)

    def test_putExceedsMaxPersistent(self):

        pool = self.pool

        origCached = [StubHTTPProtocol(), StubHTTPProtocol()]
        for p in origCached:
            p.makeConnection(StringTransport())
            pool._putConnection(("http", b"example.com", 80), p)
        self.assertEqual(pool._connections[("http", b"example.com", 80)],
                         origCached)
        timeouts = pool._timeouts.copy()

        newProtocol = StubHTTPProtocol()
        newProtocol.makeConnection(StringTransport())
        pool._putConnection(("http", b"example.com", 80), newProtocol)

        newCached = pool._connections[("http", b"example.com", 80)]
        self.assertEqual(len(newCached), 2)
        self.assertEqual(newCached, [origCached[1], newProtocol])
        self.assertEqual([p.transport.disconnecting for p in newCached],
                         [False, False])
        self.assertEqual(origCached[0].transport.disconnecting, True)
        self.assertTrue(timeouts[origCached[0]].cancelled)
        self.assertNotIn(origCached[0], pool._timeouts)

    def test_maxPersistentPerHost(self):

        def addProtocol(scheme, host, port):
            p = StubHTTPProtocol()
            p.makeConnection(StringTransport())
            self.pool._putConnection((scheme, host, port), p)
            return p
        persistent = []
        persistent.append(addProtocol("http", b"example.com", 80))
        persistent.append(addProtocol("http", b"example.com", 80))
        addProtocol("https", b"example.com", 443)
        addProtocol("http", b"www2.example.com", 80)

        self.assertEqual(
            self.pool._connections[("http", b"example.com", 80)], persistent)
        self.assertEqual(
            len(self.pool._connections[("https", b"example.com", 443)]), 1)
        self.assertEqual(
            len(self.pool._connections[("http", b"www2.example.com", 80)]), 1)

    def test_getCachedConnection(self):

        protocol = StubHTTPProtocol()
        protocol.makeConnection(StringTransport())
        self.pool._putConnection(("http", b"example.com", 80), protocol)

        def gotConnection(conn):
            self.assertIdentical(protocol, conn)
            self.assertNotIn(
                conn, self.pool._connections[("http", b"example.com", 80)])
            self.fakeReactor.advance(241)
            self.assertEqual(conn.transport.disconnecting, False)
            self.assertNotIn(conn, self.pool._timeouts)

        return self.pool.getConnection(("http", b"example.com", 80),
                                       BadEndpoint(),
                                       ).addCallback(gotConnection)

    def test_newConnection(self):

        protocol = StubHTTPProtocol()
        protocol.makeConnection(StringTransport())
        key = 12245
        self.pool._putConnection(key, protocol)

        def gotConnection(newConnection):
            self.assertNotIdentical(protocol, newConnection)
            self.assertIn(protocol, self.pool._connections[key])
            self.assertNotIn(newConnection, self.pool._connections.values())

        d = self.pool._newConnection(key, DummyEndpoint())
        return d.addCallback(gotConnection)

    def test_getSkipsDisconnected(self):

        pool = self.pool
        key = ("http", b"example.com", 80)

        origCached = [StubHTTPProtocol(), StubHTTPProtocol()]
        for p in origCached:
            p.makeConnection(StringTransport())
            pool._putConnection(key, p)
        self.assertEqual(pool._connections[key], origCached)

        origCached[0].state = "DISCONNECTED"

        result = []
        self.pool.getConnection(key,
                                BadEndpoint()).addCallback(result.append)
        self.assertIdentical(result[0], origCached[1])

        self.assertEqual(pool._connections[key], [])
        self.assertEqual(pool._timeouts, {})

    def test_putNotQuiescent(self):

        protocol = StubHTTPProtocol()
        self.assertEqual(protocol.state, "QUIESCENT")

        logObserver = EventLoggingObserver.createWithCleanup(
            self,
            globalLogPublisher
        )

        protocol.state = "NOTQUIESCENT"
        self.pool._putConnection(("http", b"example.com", 80), protocol)
        self.assertEquals(1, len(logObserver))

        event = logObserver[0]
        f = event["log_failure"]

        self.assertIsInstance(f.value, RuntimeError)
        self.assertEqual(
            f.getErrorMessage(),
            "BUG: Non-quiescent protocol added to connection pool.")
        self.assertIdentical(None, self.pool._connections.get(
                ("http", b"example.com", 80)))
        self.flushLoggedErrors(RuntimeError)

    def test_getUsesQuiescentCallback(self):

        class StringEndpoint(object):
            def connect(self, factory):
                p = factory.buildProtocol(None)
                p.makeConnection(StringTransport())
                return succeed(p)

        pool = HTTPConnectionPool(self.fakeReactor, True)
        pool.retryAutomatically = False
        result = []
        key = "a key"
        pool.getConnection(
            key, StringEndpoint()).addCallback(
            result.append)
        protocol = result[0]
        self.assertIsInstance(protocol, HTTP11ClientProtocol)

        protocol._state = "QUIESCENT"
        protocol._quiescentCallback(protocol)

        result2 = []
        pool.getConnection(
            key, StringEndpoint()).addCallback(
            result2.append)
        self.assertIdentical(result2[0], protocol)

    def test_closeCachedConnections(self):

        persistent = []
        def addProtocol(scheme, host, port):
            p = HTTP11ClientProtocol()
            p.makeConnection(StringTransport())
            self.pool._putConnection((scheme, host, port), p)
            persistent.append(p)
        addProtocol("http", b"example.com", 80)
        addProtocol("http", b"www2.example.com", 80)
        doneDeferred = self.pool.closeCachedConnections()

        for p in persistent:
            self.assertEqual(p.transport.disconnecting, True)
        self.assertEqual(self.pool._connections, {})
        for dc in self.fakeReactor.getDelayedCalls():
            self.assertEqual(dc.cancelled, True)
        self.assertEqual(self.pool._timeouts, {})

        result = []
        doneDeferred.addCallback(result.append)
        self.assertEqual(result, [])
        persistent[0].connectionLost(Failure(ConnectionDone()))
        self.assertEqual(result, [])
        persistent[1].connectionLost(Failure(ConnectionDone()))
        self.assertEqual(result, [None])

    def test_cancelGetConnectionCancelsEndpointConnect(self):

        self.assertEqual(self.pool._connections, {})
        connectionResult = Deferred()

        class Endpoint:
            def connect(self, factory):
                return connectionResult

        d = self.pool.getConnection(12345, Endpoint())
        d.cancel()
        self.assertEqual(self.failureResultOf(connectionResult).type,
                         CancelledError)

class AgentTestsMixin(object):

    def test_interface(self):

        self.assertTrue(verifyObject(IAgent, self.makeAgent()))

class IntegrationTestingMixin(object):


    def test_integrationTestIPv4(self):

        self.integrationTest(b'example.com', EXAMPLE_COM_IP, IPv4Address)

    def test_integrationTestIPv4Address(self):

        self.integrationTest(b'127.0.0.7', '127.0.0.7', IPv4Address)

    def test_integrationTestIPv6(self):

        self.integrationTest(b'ipv6.example.com', EXAMPLE_COM_V6_IP,
                             IPv6Address)

    def test_integrationTestIPv6Address(self):

        self.integrationTest(b'[::7]', '::7', IPv6Address)

    def integrationTest(self, hostName, expectedAddress, addressType,
                        serverWrapper=lambda server: server,
                        createAgent=client.Agent,
                        scheme=b'http'):

        reactor = self.createReactor()
        agent = createAgent(reactor)
        deferred = agent.request(b"GET", scheme + b"://" + hostName + b"/")
        host, port, factory, timeout, bind = reactor.tcpClients[0]
        self.assertEqual(host, expectedAddress)
        peerAddress = addressType('TCP', host, port)
        clientProtocol = factory.buildProtocol(peerAddress)
        clientTransport = FakeTransport(clientProtocol, False,
                                        peerAddress=peerAddress)
        clientProtocol.makeConnection(clientTransport)
        @Factory.forProtocol
        def accumulator():
            ap = AccumulatingProtocol()
            accumulator.currentProtocol = ap
            return ap
        accumulator.currentProtocol = None
        accumulator.protocolConnectionMade = None
        wrapper = serverWrapper(accumulator).buildProtocol(None)
        serverTransport = FakeTransport(wrapper, True)
        wrapper.makeConnection(serverTransport)
        pump = IOPump(clientProtocol, wrapper,
                      clientTransport, serverTransport, False)
        pump.flush()
        self.assertNoResult(deferred)
        lines = accumulator.currentProtocol.data.split(b"\r\n")
        self.assertTrue(lines[0].startswith(b"GET / HTTP"), lines[0])
        headers = dict([line.split(b": ", 1) for line in lines[1:] if line])
        self.assertEqual(headers[b'Host'], hostName)
        self.assertNoResult(deferred)
        accumulator.currentProtocol.transport.write(
            b"HTTP/1.1 200 OK"
            b"\r\nX-An-Header: an-value\r\n"
            b"\r\nContent-length: 12\r\n\r\n"
            b"hello world!"
        )
        pump.flush()
        response = self.successResultOf(deferred)
        self.assertEquals(response.headers.getRawHeaders(b'x-an-header')[0],
                          b"an-value")

@implementer(IAgentEndpointFactory)
class StubEndpointFactory(object):

    def endpointForURI(self, uri):

        return (uri.scheme, uri.host, uri.port)

class AgentTests(TestCase, FakeReactorAndConnectMixin, AgentTestsMixin,
                 IntegrationTestingMixin):

    def makeAgent(self):

        return client.Agent(self.reactor)

    def setUp(self):

        self.reactor = self.createReactor()
        self.agent = self.makeAgent()

    def test_defaultPool(self):

        agent = client.Agent(self.reactor)
        self.assertIsInstance(agent._pool, HTTPConnectionPool)
        self.assertEqual(agent._pool.persistent, False)
        self.assertIdentical(agent._reactor, agent._pool._reactor)

    def test_persistent(self):

        pool = HTTPConnectionPool(self.reactor)
        agent = client.Agent(self.reactor, pool=pool)
        agent._getEndpoint = lambda *args: self
        agent.request(b"GET", b"http://127.0.0.1")
        self.assertEqual(self.protocol.requests[0][0].persistent, True)

    def test_nonPersistent(self):

        pool = HTTPConnectionPool(self.reactor, persistent=False)
        agent = client.Agent(self.reactor, pool=pool)
        agent._getEndpoint = lambda *args: self
        agent.request(b"GET", b"http://127.0.0.1")
        self.assertEqual(self.protocol.requests[0][0].persistent, False)

    def test_connectUsesConnectionPool(self):

        endpoint = DummyEndpoint()
        class MyAgent(client.Agent):
            def _getEndpoint(this, uri):
                self.assertEqual((uri.scheme, uri.host, uri.port),
                                 (b"http", b"foo", 80))
                return endpoint

        class DummyPool(object):
            connected = False
            persistent = False
            def getConnection(this, key, ep):
                this.connected = True
                self.assertEqual(ep, endpoint)
                self.assertEqual(key, (b"http", b"foo", 80))
                return defer.succeed(StubHTTPProtocol())

        pool = DummyPool()
        agent = MyAgent(self.reactor, pool=pool)
        self.assertIdentical(pool, agent._pool)

        headers = http_headers.Headers()
        headers.addRawHeader(b"host", b"foo")
        bodyProducer = object()
        agent.request(b'GET', b'http://foo/',
                      bodyProducer=bodyProducer, headers=headers)
        self.assertEqual(agent._pool.connected, True)

    def test_nonBytesMethod(self):

        self.assertRaises(TypeError, self.agent.request,
                          u'GET', b'http://foo.example/')

    def test_unsupportedScheme(self):

        return self.assertFailure(
            self.agent.request(b'GET', b'mailto:alice@example.com'),
            SchemeNotSupported)

    def test_connectionFailed(self):

        result = self.agent.request(b'GET', b'http://foo/')
        host, port, factory = self.reactor.tcpClients.pop()[:3]
        factory.clientConnectionFailed(None, Failure(ConnectionRefusedError()))
        self.reactor.advance(10)
        self.failureResultOf(result, ConnectionRefusedError)

    def test_connectHTTP(self):

        expectedHost = b'example.com'
        expectedPort = 1234
        endpoint = self.agent._getEndpoint(URI.fromBytes(
            b'http://' + expectedHost + b":" + intToBytes(expectedPort)))
        self.assertEqual(endpoint._hostStr, "example.com")
        self.assertEqual(endpoint._port, expectedPort)
        self.assertIsInstance(endpoint, HostnameEndpoint)

    def test_nonDecodableURI(self):

        uri = URI.fromBytes(b"http://example.com:80")
        uri.host = u'\u2603.com'.encode('utf8')

        with self.assertRaises(ValueError) as e:
            self.agent._getEndpoint(uri)

        self.assertEqual(e.exception.args[0],
                         ("The host of the provided URI ({reprout}) contains "
                          "non-ASCII octets, it should be ASCII "
                          "decodable.").format(reprout=repr(uri.host)))

    def test_hostProvided(self):

        self.agent._getEndpoint = lambda *args: self
        self.agent.request(
            b'GET', b'http://example.com/foo?bar')

        req, res = self.protocol.requests.pop()
        self.assertEqual(req.headers.getRawHeaders(b'host'), [b'example.com'])

    def test_hostIPv6Bracketed(self):

        self.agent._getEndpoint = lambda *args: self
        self.agent.request(b'GET', b'http://[::1]/')

        req, res = self.protocol.requests.pop()
        self.assertEqual(req.headers.getRawHeaders(b'host'), [b'[::1]'])

    def test_hostOverride(self):

        headers = http_headers.Headers({b'foo': [b'bar'], b'host': [b'quux']})
        self.agent._getEndpoint = lambda *args: self
        self.agent.request(
            b'GET', b'http://example.com/foo?bar', headers)

        req, res = self.protocol.requests.pop()
        self.assertEqual(req.headers.getRawHeaders(b'host'), [b'quux'])

    def test_headersUnmodified(self):

        headers = http_headers.Headers()
        self.agent._getEndpoint = lambda *args: self
        self.agent.request(
            b'GET', b'http://example.com/foo', headers)

        protocol = self.protocol

        self.assertEqual(len(protocol.requests), 1)
        self.assertEqual(headers, http_headers.Headers())

    def test_hostValueStandardHTTP(self):

        self.assertEqual(
            self.agent._computeHostValue(b'http', b'example.com', 80),
            b'example.com')

    def test_hostValueNonStandardHTTP(self):

        self.assertEqual(
            self.agent._computeHostValue(b'http', b'example.com', 54321),
            b'example.com:54321')

    def test_hostValueStandardHTTPS(self):

        self.assertEqual(
            self.agent._computeHostValue(b'https', b'example.com', 443),
            b'example.com')

    def test_hostValueNonStandardHTTPS(self):

        self.assertEqual(
            self.agent._computeHostValue(b'https', b'example.com', 54321),
            b'example.com:54321')

    def test_request(self):

        self.agent._getEndpoint = lambda *args: self

        headers = http_headers.Headers({b'foo': [b'bar']})
        body = object()
        self.agent.request(
            b'GET', b'http://example.com:1234/foo?bar', headers, body)

        protocol = self.protocol

        self.assertEqual(len(protocol.requests), 1)
        req, res = protocol.requests.pop()
        self.assertIsInstance(req, Request)
        self.assertEqual(req.method, b'GET')
        self.assertEqual(req.uri, b'/foo?bar')
        self.assertEqual(
            req.headers,
            http_headers.Headers({b'foo': [b'bar'],
                                  b'host': [b'example.com:1234']}))
        self.assertIdentical(req.bodyProducer, body)

    def test_connectTimeout(self):

        agent = client.Agent(self.reactor, connectTimeout=5)
        agent.request(b'GET', b'http://foo/')
        timeout = self.reactor.tcpClients.pop()[3]
        self.assertEqual(5, timeout)

    def test_connectTimeoutHTTPS(self):

        agent = client.Agent(self.reactor, connectTimeout=5)
        agent.request(b'GET', b'https://foo/')
        timeout = self.reactor.tcpClients.pop()[3]
        self.assertEqual(5, timeout)

    test_connectTimeoutHTTPS.skip = skipWhenNoSSL

    def test_bindAddress(self):

        agent = client.Agent(self.reactor, bindAddress='192.168.0.1')
        agent.request(b'GET', b'http://foo/')
        address = self.reactor.tcpClients.pop()[4]
        self.assertEqual('192.168.0.1', address)

    def test_bindAddressSSL(self):

        agent = client.Agent(self.reactor, bindAddress='192.168.0.1')
        agent.request(b'GET', b'https://foo/')
        address = self.reactor.tcpClients.pop()[4]
        self.assertEqual('192.168.0.1', address)

    test_bindAddressSSL.skip = skipWhenNoSSL

    def test_responseIncludesRequest(self):

        uri = b'http://example.com/'
        agent = self.buildAgentForWrapperTest(self.reactor)
        d = agent.request(b'GET', uri)

        self.assertEqual(len(self.protocol.requests), 1)
        req, res = self.protocol.requests.pop()
        self.assertIsInstance(req, Request)

        resp = client.Response._construct(
            (b'HTTP', 1, 1),
            200,
            b'OK',
            client.Headers({}),
            None,
            req)
        res.callback(resp)

        response = self.successResultOf(d)
        self.assertEqual(
            (response.request.method, response.request.absoluteURI,
             response.request.headers),
            (req.method, req.absoluteURI, req.headers))

    def test_requestAbsoluteURI(self):

        uri = b'http://example.com/foo;1234?bar#frag'
        agent = self.buildAgentForWrapperTest(self.reactor)
        agent.request(b'GET', uri)

        self.assertEqual(len(self.protocol.requests), 1)
        req, res = self.protocol.requests.pop()
        self.assertIsInstance(req, Request)
        self.assertEqual(req.absoluteURI, uri)

    def test_requestMissingAbsoluteURI(self):

        request = client.Request(b'FOO', b'/', client.Headers(), None)
        self.assertIdentical(request.absoluteURI, None)

    def test_endpointFactory(self):

        factory = StubEndpointFactory()
        agent = client.Agent.usingEndpointFactory(
            None, endpointFactory=factory)
        uri = URI.fromBytes(b'http://example.com/')
        returnedEndpoint = agent._getEndpoint(uri)
        self.assertEqual(returnedEndpoint, (b"http", b"example.com", 80))

    def test_endpointFactoryDefaultPool(self):

        agent = client.Agent.usingEndpointFactory(
            self.reactor, StubEndpointFactory())
        pool = agent._pool
        self.assertEqual((pool.__class__, pool.persistent, pool._reactor),
                          (HTTPConnectionPool, False, agent._reactor))

    def test_endpointFactoryPool(self):

        pool = object()
        agent = client.Agent.usingEndpointFactory(
            self.reactor, StubEndpointFactory(), pool)
        self.assertIs(pool, agent._pool)

class AgentHTTPSTests(TestCase, FakeReactorAndConnectMixin,
                      IntegrationTestingMixin):

    skip = skipWhenNoSSL

    def makeEndpoint(self, host=b'example.com', port=443):

        return client.Agent(self.createReactor())._getEndpoint(
            URI.fromBytes(b'https://' + host + b":" + intToBytes(port) + b"/"))

    def test_endpointType(self):

        from twisted.internet.endpoints import _WrapperEndpoint
        endpoint = self.makeEndpoint()
        self.assertIsInstance(endpoint, _WrapperEndpoint)
        self.assertIsInstance(endpoint._wrappedEndpoint, HostnameEndpoint)

    def test_hostArgumentIsRespected(self):

        endpoint = self.makeEndpoint(host=b"example.com")
        self.assertEqual(endpoint._wrappedEndpoint._hostStr, "example.com")

    def test_portArgumentIsRespected(self):

        expectedPort = 4321
        endpoint = self.makeEndpoint(port=expectedPort)
        self.assertEqual(endpoint._wrappedEndpoint._port, expectedPort)

    def test_contextFactoryType(self):

        endpoint = self.makeEndpoint()
        contextFactory = endpoint._wrapperFactory(None)._connectionCreator
        self.assertIsInstance(contextFactory, ClientTLSOptions)
        self.assertEqual(contextFactory._hostname, u"example.com")

    def test_connectHTTPSCustomConnectionCreator(self):

        expectedHost = b'example.org'
        expectedPort = 20443
        class JustEnoughConnection(object):
            handshakeStarted = False
            connectState = False
            def do_handshake(self):

                self.handshakeStarted = True
            def set_connect_state(self):

                self.connectState = True

        contextArgs = []

        @implementer(IOpenSSLClientConnectionCreator)
        class JustEnoughCreator(object):
            def __init__(self, hostname, port):
                self.hostname = hostname
                self.port = port

            def clientConnectionForTLS(self, tlsProtocol):

                contextArgs.append((tlsProtocol, self.hostname, self.port))
                return expectedConnection

        expectedConnection = JustEnoughConnection()
        @implementer(IPolicyForHTTPS)
        class StubBrowserLikePolicyForHTTPS(object):
            def creatorForNetloc(self, hostname, port):

                return JustEnoughCreator(hostname, port)

        expectedCreatorCreator = StubBrowserLikePolicyForHTTPS()
        reactor = self.createReactor()
        agent = client.Agent(reactor, expectedCreatorCreator)
        endpoint = agent._getEndpoint(URI.fromBytes(
            b'https://' + expectedHost + b":" + intToBytes(expectedPort)))
        endpoint.connect(Factory.forProtocol(Protocol))
        tlsFactory = reactor.tcpClients[-1][2]
        tlsProtocol = tlsFactory.buildProtocol(None)
        tlsProtocol.makeConnection(StringTransport())
        tls = contextArgs[0][0]
        self.assertIsInstance(tls, TLSMemoryBIOProtocol)
        self.assertEqual(contextArgs[0][1:], (expectedHost, expectedPort))
        self.assertTrue(expectedConnection.handshakeStarted)
        self.assertTrue(expectedConnection.connectState)

    def test_deprecatedDuckPolicy(self):

        def warnMe():
            client.Agent(deterministicResolvingReactor(MemoryReactorClock()),
                         "does-not-provide-IPolicyForHTTPS")
        warnMe()
        warnings = self.flushWarnings([warnMe])
        self.assertEqual(len(warnings), 1)
        [warning] = warnings
        self.assertEqual(warning['category'], DeprecationWarning)
        self.assertEqual(
            warning['message'],
            "'does-not-provide-IPolicyForHTTPS' was passed as the HTTPS "
            "policy for an Agent, but it does not provide IPolicyForHTTPS.  "
            "Since Twisted 14.0, you must pass a provider of IPolicyForHTTPS."
        )

    def test_alternateTrustRoot(self):

        trustRoot = CustomOpenSSLTrustRoot()
        policy = BrowserLikePolicyForHTTPS(trustRoot=trustRoot)
        creator = policy.creatorForNetloc(b"thingy", 4321)
        self.assertTrue(trustRoot.called)
        connection = creator.clientConnectionForTLS(None)
        self.assertIs(trustRoot.context, connection.get_context())

    def integrationTest(self, hostName, expectedAddress, addressType):

        certHostName = hostName.strip(b'[]')
        authority, server = certificatesForAuthorityAndServer(certHostName
                                                              .decode('ascii'))
        def tlsify(serverFactory):
            return TLSMemoryBIOFactory(server.options(), False, serverFactory)
        def tlsagent(reactor):
            from twisted.web.iweb import IPolicyForHTTPS
            from zope.interface import implementer
            @implementer(IPolicyForHTTPS)
            class Policy(object):
                def creatorForNetloc(self, hostname, port):
                    return optionsForClientTLS(hostname.decode("ascii"),
                                               trustRoot=authority)
            return client.Agent(reactor, contextFactory=Policy())
        (super(AgentHTTPSTests, self)
         .integrationTest(hostName, expectedAddress, addressType,
                          serverWrapper=tlsify,
                          createAgent=tlsagent,
                          scheme=b'https'))

class WebClientContextFactoryTests(TestCase):


    def setUp(self):

        from twisted.web.client import WebClientContextFactory
        self.warned = self.flushWarnings([WebClientContextFactoryTests.setUp])
        self.webClientContextFactory = WebClientContextFactory

    def test_deprecated(self):

        self.assertEqual(len(self.warned), 1)
        [warning] = self.warned
        self.assertEqual(warning['category'], DeprecationWarning)
        self.assertEqual(
            warning['message'],
            getDeprecationWarningString(
                self.webClientContextFactory, Version("Twisted", 14, 0, 0),
                replacement=BrowserLikePolicyForHTTPS,
            )

            .replace(";", ":")
        )

    def test_missingSSL(self):

        self.assertRaises(
            NotImplementedError,
            self.webClientContextFactory().getContext,
            b'example.com', 443,
        )

    def test_returnsContext(self):

        ctx = self.webClientContextFactory().getContext('example.com', 443)
        self.assertIsInstance(ctx, ssl.SSL.Context)

    def test_setsTrustRootOnContextToDefaultTrustRoot(self):

        ctx = self.webClientContextFactory()
        certificateOptions = ctx._getCertificateOptions('example.com', 443)
        self.assertIsInstance(
            certificateOptions.trustRoot, ssl.OpenSSLDefaultPaths)

    test_returnsContext.skip \
        = test_setsTrustRootOnContextToDefaultTrustRoot.skip \
        = skipWhenNoSSL
    test_missingSSL.skip = skipWhenSSLPresent

class HTTPConnectionPoolRetryTests(TestCase, FakeReactorAndConnectMixin):


    def test_onlyRetryIdempotentMethods(self):

        pool = client.HTTPConnectionPool(None)
        connection = client._RetryingHTTP11ClientProtocol(None, pool)
        self.assertTrue(connection._shouldRetry(
            b"GET", RequestNotSent(), None))
        self.assertTrue(connection._shouldRetry(
            b"HEAD", RequestNotSent(), None))
        self.assertTrue(connection._shouldRetry(
            b"OPTIONS", RequestNotSent(), None))
        self.assertTrue(connection._shouldRetry(
            b"TRACE", RequestNotSent(), None))
        self.assertTrue(connection._shouldRetry(
            b"DELETE", RequestNotSent(), None))
        self.assertFalse(connection._shouldRetry(
            b"POST", RequestNotSent(), None))
        self.assertFalse(connection._shouldRetry(
            b"MYMETHOD", RequestNotSent(), None))

    def test_onlyRetryIfNoResponseReceived(self):

        pool = client.HTTPConnectionPool(None)
        connection = client._RetryingHTTP11ClientProtocol(None, pool)
        self.assertTrue(connection._shouldRetry(
            b"GET", RequestNotSent(), None))
        self.assertTrue(connection._shouldRetry(
            b"GET", RequestTransmissionFailed([]), None))
        self.assertTrue(connection._shouldRetry(
            b"GET", ResponseNeverReceived([]),None))
        self.assertFalse(connection._shouldRetry(
            b"GET", ResponseFailed([]), None))
        self.assertFalse(connection._shouldRetry(
            b"GET", ConnectionRefusedError(), None))

    def test_dontRetryIfFailedDueToCancel(self):

        pool = client.HTTPConnectionPool(None)
        connection = client._RetryingHTTP11ClientProtocol(None, pool)
        exception = ResponseNeverReceived([Failure(defer.CancelledError())])
        self.assertFalse(connection._shouldRetry(b"GET", exception, None))

    def test_retryIfFailedDueToNonCancelException(self):

        pool = client.HTTPConnectionPool(None)
        connection = client._RetryingHTTP11ClientProtocol(None, pool)
        self.assertTrue(connection._shouldRetry(
            b"GET", ResponseNeverReceived([Failure(Exception())]), None))

    def test_wrappedOnPersistentReturned(self):

        pool = client.HTTPConnectionPool(Clock())

        protocol = StubHTTPProtocol()
        protocol.makeConnection(StringTransport())
        pool._putConnection(123, protocol)

        d = pool.getConnection(123, DummyEndpoint())

        def gotConnection(connection):
            self.assertIsInstance(connection,
                                  client._RetryingHTTP11ClientProtocol)
            self.assertIdentical(connection._clientProtocol, protocol)
        return d.addCallback(gotConnection)

    def test_notWrappedOnNewReturned(self):

        pool = client.HTTPConnectionPool(None)
        d = pool.getConnection(123, DummyEndpoint())

        def gotConnection(connection):
            self.assertIdentical(connection.__class__, HTTP11ClientProtocol)
        return d.addCallback(gotConnection)

    def retryAttempt(self, willWeRetry):

        protocols = []
        def newProtocol():
            protocol = StubHTTPProtocol()
            protocols.append(protocol)
            return defer.succeed(protocol)

        bodyProducer = object()
        request = client.Request(b"FOO", b"/", client.Headers(), bodyProducer,
                                 persistent=True)
        newProtocol()
        protocol = protocols[0]
        retrier = client._RetryingHTTP11ClientProtocol(protocol, newProtocol)

        def _shouldRetry(m, e, bp):
            self.assertEqual(m, b"FOO")
            self.assertIdentical(bp, bodyProducer)
            self.assertIsInstance(e, (RequestNotSent, ResponseNeverReceived))
            return willWeRetry
        retrier._shouldRetry = _shouldRetry

        d = retrier.request(request)

        self.assertEqual(len(protocols), 1)
        self.assertEqual(len(protocols[0].requests), 1)

        protocol.requests[0][1].errback(RequestNotSent())
        return d, protocols

    def test_retryIfShouldRetryReturnsTrue(self):

        d, protocols = self.retryAttempt(True)
        self.assertEqual(len(protocols), 2)
        response = object()
        protocols[1].requests[0][1].callback(response)
        return d.addCallback(self.assertIdentical, response)

    def test_dontRetryIfShouldRetryReturnsFalse(self):

        d, protocols = self.retryAttempt(False)
        self.assertEqual(len(protocols), 1)
        return self.assertFailure(d, RequestNotSent)

    def test_onlyRetryWithoutBody(self):

        pool = client.HTTPConnectionPool(None)
        connection = client._RetryingHTTP11ClientProtocol(None, pool)
        self.assertTrue(connection._shouldRetry(b"GET", RequestNotSent(), None))
        self.assertFalse(connection._shouldRetry(b"GET", RequestNotSent(), object()))

    def test_onlyRetryOnce(self):

        d, protocols = self.retryAttempt(True)
        self.assertEqual(len(protocols), 2)
        protocols[1].requests[0][1].errback(ResponseNeverReceived([]))
        self.assertEqual(len(protocols), 2)
        return self.assertFailure(d, ResponseNeverReceived)

    def test_dontRetryIfRetryAutomaticallyFalse(self):

        pool = client.HTTPConnectionPool(Clock())
        pool.retryAutomatically = False

        protocol = StubHTTPProtocol()
        protocol.makeConnection(StringTransport())
        pool._putConnection(123, protocol)

        d = pool.getConnection(123, DummyEndpoint())

        def gotConnection(connection):
            self.assertIdentical(connection, protocol)
        return d.addCallback(gotConnection)

    def test_retryWithNewConnection(self):

        pool = client.HTTPConnectionPool(Clock())
        key = 123
        endpoint = DummyEndpoint()
        newConnections = []

        def newConnection(k, e):
            newConnections.append((k, e))
        pool._newConnection = newConnection

        protocol = StubHTTPProtocol()
        protocol.makeConnection(StringTransport())
        pool._putConnection(key, protocol)

        d = pool.getConnection(key, endpoint)

        def gotConnection(connection):
            self.assertIsInstance(connection,
                                  client._RetryingHTTP11ClientProtocol)
            self.assertIdentical(connection._clientProtocol, protocol)
            self.assertEqual(newConnections, [])
            connection._newConnection()
            self.assertEqual(len(newConnections), 1)
            self.assertEqual(newConnections[0][0], key)
            self.assertIdentical(newConnections[0][1], endpoint)
        return d.addCallback(gotConnection)

class CookieTestsMixin(object):

    def addCookies(self, cookieJar, uri, cookies):

        response = client._FakeUrllib2Response(
            client.Response(
                (b'HTTP', 1, 1),
                200,
                b'OK',
                client.Headers({b'Set-Cookie': cookies}),
                None))
        request = client._FakeUrllib2Request(uri)
        cookieJar.extract_cookies(response, request)
        return request, response

class CookieJarTests(TestCase, CookieTestsMixin):

    def makeCookieJar(self):

        cookieJar = cookielib.CookieJar()
        reqres = self.addCookies(
            cookieJar,
            b'http://example.com:1234/foo?bar',
            [b'foo=1; cow=moo; Path=/foo; Comment=hello',
             b'bar=2; Comment=goodbye'])
        return cookieJar, reqres

    def test_extractCookies(self):

        jar = self.makeCookieJar()[0]
        cookies = dict([(c.name, c) for c in jar])

        cookie = cookies['foo']
        self.assertEqual(cookie.version, 0)
        self.assertEqual(cookie.name, 'foo')
        self.assertEqual(cookie.value, '1')
        self.assertEqual(cookie.path, '/foo')
        self.assertEqual(cookie.comment, 'hello')
        self.assertEqual(cookie.get_nonstandard_attr('cow'), 'moo')

        cookie = cookies['bar']
        self.assertEqual(cookie.version, 0)
        self.assertEqual(cookie.name, 'bar')
        self.assertEqual(cookie.value, '2')
        self.assertEqual(cookie.path, '/')
        self.assertEqual(cookie.comment, 'goodbye')
        self.assertIdentical(cookie.get_nonstandard_attr('cow'), None)

    def test_sendCookie(self):

        jar, (request, response) = self.makeCookieJar()

        self.assertIdentical(
            request.get_header('Cookie', None),
            None)

        jar.add_cookie_header(request)
        self.assertEqual(
            request.get_header('Cookie', None),
            'foo=1; bar=2')

class CookieAgentTests(TestCase, CookieTestsMixin, FakeReactorAndConnectMixin,
                       AgentTestsMixin):

    def makeAgent(self):

        return client.CookieAgent(
            self.buildAgentForWrapperTest(self.reactor),
            cookielib.CookieJar())

    def setUp(self):
        self.reactor = self.createReactor()

    def test_emptyCookieJarRequest(self):

        cookieJar = cookielib.CookieJar()
        self.assertEqual(list(cookieJar), [])

        agent = self.buildAgentForWrapperTest(self.reactor)
        cookieAgent = client.CookieAgent(agent, cookieJar)
        d = cookieAgent.request(
            b'GET', b'http://example.com:1234/foo?bar')

        def _checkCookie(ignored):
            cookies = list(cookieJar)
            self.assertEqual(len(cookies), 1)
            self.assertEqual(cookies[0].name, 'foo')
            self.assertEqual(cookies[0].value, '1')

        d.addCallback(_checkCookie)

        req, res = self.protocol.requests.pop()
        self.assertIdentical(req.headers.getRawHeaders(b'cookie'), None)

        resp = client.Response(
            (b'HTTP', 1, 1),
            200,
            b'OK',
            client.Headers({b'Set-Cookie': [b'foo=1',]}),
            None)
        res.callback(resp)

        return d

    def test_requestWithCookie(self):

        uri = b'http://example.com:1234/foo?bar'
        cookie = b'foo=1'

        cookieJar = cookielib.CookieJar()
        self.addCookies(cookieJar, uri, [cookie])
        self.assertEqual(len(list(cookieJar)), 1)

        agent = self.buildAgentForWrapperTest(self.reactor)
        cookieAgent = client.CookieAgent(agent, cookieJar)
        cookieAgent.request(b'GET', uri)

        req, res = self.protocol.requests.pop()
        self.assertEqual(req.headers.getRawHeaders(b'cookie'), [cookie])

    def test_secureCookie(self):

        uri = b'https://example.com:1234/foo?bar'
        cookie = b'foo=1;secure'

        cookieJar = cookielib.CookieJar()
        self.addCookies(cookieJar, uri, [cookie])
        self.assertEqual(len(list(cookieJar)), 1)

        agent = self.buildAgentForWrapperTest(self.reactor)
        cookieAgent = client.CookieAgent(agent, cookieJar)
        cookieAgent.request(b'GET', uri)

        req, res = self.protocol.requests.pop()
        self.assertEqual(req.headers.getRawHeaders(b'cookie'), [b'foo=1'])

    test_secureCookie.skip = skipWhenNoSSL

    def test_secureCookieOnInsecureConnection(self):

        uri = b'http://example.com/foo?bar'
        cookie = b'foo=1;secure'

        cookieJar = cookielib.CookieJar()
        self.addCookies(cookieJar, uri, [cookie])
        self.assertEqual(len(list(cookieJar)), 1)

        agent = self.buildAgentForWrapperTest(self.reactor)
        cookieAgent = client.CookieAgent(agent, cookieJar)
        cookieAgent.request(b'GET', uri)

        req, res = self.protocol.requests.pop()
        self.assertIdentical(None, req.headers.getRawHeaders(b'cookie'))

    def test_portCookie(self):

        uri = b'http://example.com:1234/foo?bar'
        cookie = b'foo=1;port=1234'

        cookieJar = cookielib.CookieJar()
        self.addCookies(cookieJar, uri, [cookie])
        self.assertEqual(len(list(cookieJar)), 1)

        agent = self.buildAgentForWrapperTest(self.reactor)
        cookieAgent = client.CookieAgent(agent, cookieJar)
        cookieAgent.request(b'GET', uri)

        req, res = self.protocol.requests.pop()
        self.assertEqual(req.headers.getRawHeaders(b'cookie'), [b'foo=1'])

    def test_portCookieOnWrongPort(self):

        uri = b'http://example.com:4567/foo?bar'
        cookie = b'foo=1;port=1234'

        cookieJar = cookielib.CookieJar()
        self.addCookies(cookieJar, uri, [cookie])
        self.assertEqual(len(list(cookieJar)), 0)

class Decoder1(proxyForInterface(IResponse)):


class Decoder2(Decoder1):


class ContentDecoderAgentTests(TestCase, FakeReactorAndConnectMixin,
                               AgentTestsMixin):

    def makeAgent(self):

        return client.ContentDecoderAgent(self.agent, [])

    def setUp(self):

        self.reactor = self.createReactor()
        self.agent = self.buildAgentForWrapperTest(self.reactor)

    def test_acceptHeaders(self):

        agent = client.ContentDecoderAgent(
            self.agent, [(b'decoder1', Decoder1), (b'decoder2', Decoder2)])

        agent.request(b'GET', b'http://example.com/foo')

        protocol = self.protocol

        self.assertEqual(len(protocol.requests), 1)
        req, res = protocol.requests.pop()
        self.assertEqual(req.headers.getRawHeaders(b'accept-encoding'),
                         [b'decoder1,decoder2'])

    def test_existingHeaders(self):

        headers = http_headers.Headers({b'foo': [b'bar'],
                                        b'accept-encoding': [b'fizz']})
        agent = client.ContentDecoderAgent(
            self.agent, [(b'decoder1', Decoder1), (b'decoder2', Decoder2)])
        agent.request(b'GET', b'http://example.com/foo', headers=headers)

        protocol = self.protocol

        self.assertEqual(len(protocol.requests), 1)
        req, res = protocol.requests.pop()
        self.assertEqual(
            list(sorted(req.headers.getAllRawHeaders())),
            [(b'Accept-Encoding', [b'fizz', b'decoder1,decoder2']),
             (b'Foo', [b'bar']),
             (b'Host', [b'example.com'])])

    def test_plainEncodingResponse(self):

        agent = client.ContentDecoderAgent(
            self.agent, [(b'decoder1', Decoder1), (b'decoder2', Decoder2)])
        deferred = agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        response = Response((b'HTTP', 1, 1), 200, b'OK', http_headers.Headers(),
                            None)
        res.callback(response)

        return deferred.addCallback(self.assertIdentical, response)

    def test_unsupportedEncoding(self):

        agent = client.ContentDecoderAgent(
            self.agent, [(b'decoder1', Decoder1), (b'decoder2', Decoder2)])
        deferred = agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers({b'foo': [b'bar'],
                                        b'content-encoding': [b'fizz']})
        response = Response((b'HTTP', 1, 1), 200, b'OK', headers, None)
        res.callback(response)

        return deferred.addCallback(self.assertIdentical, response)

    def test_unknownEncoding(self):

        agent = client.ContentDecoderAgent(
            self.agent, [(b'decoder1', Decoder1), (b'decoder2', Decoder2)])
        deferred = agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers({b'foo': [b'bar'],
                                        b'content-encoding':
                                        [b'decoder1,fizz,decoder2']})
        response = Response((b'HTTP', 1, 1), 200, b'OK', headers, None)
        res.callback(response)

        def check(result):
            self.assertNotIdentical(response, result)
            self.assertIsInstance(result, Decoder2)
            self.assertEqual([b'decoder1,fizz'],
                             result.headers.getRawHeaders(b'content-encoding'))

        return deferred.addCallback(check)

class SimpleAgentProtocol(Protocol):


    def __init__(self):
        self.made = Deferred()
        self.finished = Deferred()
        self.received = []

    def connectionMade(self):
        self.made.callback(None)

    def connectionLost(self, reason):
        self.finished.callback(None)

    def dataReceived(self, data):
        self.received.append(data)

class ContentDecoderAgentWithGzipTests(TestCase,
                                       FakeReactorAndConnectMixin):

    def setUp(self):

        self.reactor = self.createReactor()
        agent = self.buildAgentForWrapperTest(self.reactor)
        self.agent = client.ContentDecoderAgent(
            agent, [(b"gzip", client.GzipDecoder)])

    def test_gzipEncodingResponse(self):

        deferred = self.agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers({b'foo': [b'bar'],
                                        b'content-encoding': [b'gzip']})
        transport = StringTransport()
        response = Response((b'HTTP', 1, 1), 200, b'OK', headers, transport)
        response.length = 12
        res.callback(response)

        compressor = zlib.compressobj(2, zlib.DEFLATED, 16 + zlib.MAX_WBITS)
        data = (compressor.compress(b'x' * 6) + compressor.compress(b'y' * 4) +
                compressor.flush())

        def checkResponse(result):
            self.assertNotIdentical(result, response)
            self.assertEqual(result.version, (b'HTTP', 1, 1))
            self.assertEqual(result.code, 200)
            self.assertEqual(result.phrase, b'OK')
            self.assertEqual(list(result.headers.getAllRawHeaders()),
                              [(b'Foo', [b'bar'])])
            self.assertEqual(result.length, UNKNOWN_LENGTH)
            self.assertRaises(AttributeError, getattr, result, 'unknown')

            response._bodyDataReceived(data[:5])
            response._bodyDataReceived(data[5:])
            response._bodyDataFinished()

            protocol = SimpleAgentProtocol()
            result.deliverBody(protocol)

            self.assertEqual(protocol.received, [b'x' * 6 + b'y' * 4])
            return defer.gatherResults([protocol.made, protocol.finished])

        deferred.addCallback(checkResponse)

        return deferred

    def test_brokenContent(self):

        deferred = self.agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers({b'foo': [b'bar'],
                                        b'content-encoding': [b'gzip']})
        transport = StringTransport()
        response = Response((b'HTTP', 1, 1), 200, b'OK', headers, transport)
        response.length = 12
        res.callback(response)

        data = b"not gzipped content"

        def checkResponse(result):
            response._bodyDataReceived(data)

            result.deliverBody(Protocol())

        deferred.addCallback(checkResponse)
        self.assertFailure(deferred, client.ResponseFailed)

        def checkFailure(error):
            error.reasons[0].trap(zlib.error)
            self.assertIsInstance(error.response, Response)

        return deferred.addCallback(checkFailure)

    def test_flushData(self):

        class decompressobj(object):

            def __init__(self, wbits):
                pass

            def decompress(self, data):
                return b'x'

            def flush(self):
                return b'y'

        oldDecompressObj = zlib.decompressobj
        zlib.decompressobj = decompressobj
        self.addCleanup(setattr, zlib, 'decompressobj', oldDecompressObj)

        deferred = self.agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers({b'content-encoding': [b'gzip']})
        transport = StringTransport()
        response = Response((b'HTTP', 1, 1), 200, b'OK', headers, transport)
        res.callback(response)

        def checkResponse(result):
            response._bodyDataReceived(b'data')
            response._bodyDataFinished()

            protocol = SimpleAgentProtocol()
            result.deliverBody(protocol)

            self.assertEqual(protocol.received, [b'x', b'y'])
            return defer.gatherResults([protocol.made, protocol.finished])

        deferred.addCallback(checkResponse)

        return deferred

    def test_flushError(self):

        class decompressobj(object):

            def __init__(self, wbits):
                pass

            def decompress(self, data):
                return b'x'

            def flush(self):
                raise zlib.error()

        oldDecompressObj = zlib.decompressobj
        zlib.decompressobj = decompressobj
        self.addCleanup(setattr, zlib, 'decompressobj', oldDecompressObj)

        deferred = self.agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers({b'content-encoding': [b'gzip']})
        transport = StringTransport()
        response = Response((b'HTTP', 1, 1), 200, b'OK', headers, transport)
        res.callback(response)

        def checkResponse(result):
            response._bodyDataReceived(b'data')
            response._bodyDataFinished()

            protocol = SimpleAgentProtocol()
            result.deliverBody(protocol)

            self.assertEqual(protocol.received, [b'x', b'y'])
            return defer.gatherResults([protocol.made, protocol.finished])

        deferred.addCallback(checkResponse)

        self.assertFailure(deferred, client.ResponseFailed)

        def checkFailure(error):
            error.reasons[1].trap(zlib.error)
            self.assertIsInstance(error.response, Response)

        return deferred.addCallback(checkFailure)

class ProxyAgentTests(TestCase, FakeReactorAndConnectMixin, AgentTestsMixin):

    def makeAgent(self):

        return client.ProxyAgent(
            TCP4ClientEndpoint(self.reactor, "127.0.0.1", 1234),
            self.reactor)

    def setUp(self):
        self.reactor = self.createReactor()
        self.agent = client.ProxyAgent(
            TCP4ClientEndpoint(self.reactor, "bar", 5678), self.reactor)
        oldEndpoint = self.agent._proxyEndpoint
        self.agent._proxyEndpoint = self.StubEndpoint(oldEndpoint, self)

    def test_nonBytesMethod(self):

        self.assertRaises(TypeError, self.agent.request,
                          u'GET', b'http://foo.example/')

    def test_proxyRequest(self):

        headers = http_headers.Headers({b'foo': [b'bar']})
        body = object()
        self.agent.request(
            b'GET', b'http://example.com:1234/foo?bar', headers, body)

        host, port, factory = self.reactor.tcpClients.pop()[:3]
        self.assertEqual(host, "bar")
        self.assertEqual(port, 5678)

        self.assertIsInstance(factory._wrappedFactory,
                              client._HTTP11ClientFactory)

        protocol = self.protocol

        self.assertEqual(len(protocol.requests), 1)
        req, res = protocol.requests.pop()
        self.assertIsInstance(req, Request)
        self.assertEqual(req.method, b'GET')
        self.assertEqual(req.uri, b'http://example.com:1234/foo?bar')
        self.assertEqual(
            req.headers,
            http_headers.Headers({b'foo': [b'bar'],
                                  b'host': [b'example.com:1234']}))
        self.assertIdentical(req.bodyProducer, body)

    def test_nonPersistent(self):

        self.assertEqual(self.agent._pool.persistent, False)

    def test_connectUsesConnectionPool(self):

        endpoint = DummyEndpoint()
        class DummyPool(object):
            connected = False
            persistent = False
            def getConnection(this, key, ep):
                this.connected = True
                self.assertIdentical(ep, endpoint)
                self.assertEqual(key, ("http-proxy", endpoint))
                return defer.succeed(StubHTTPProtocol())

        pool = DummyPool()
        agent = client.ProxyAgent(endpoint, self.reactor, pool=pool)
        self.assertIdentical(pool, agent._pool)

        agent.request(b'GET', b'http://foo/')
        self.assertEqual(agent._pool.connected, True)

class _RedirectAgentTestsMixin(object):

    def test_noRedirect(self):

        deferred = self.agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers()
        response = Response((b'HTTP', 1, 1), 200, b'OK', headers, None)
        res.callback(response)

        self.assertEqual(0, len(self.protocol.requests))
        result = self.successResultOf(deferred)
        self.assertIdentical(response, result)
        self.assertIdentical(result.previousResponse, None)

    def _testRedirectDefault(self, code):

        self.agent.request(b'GET', b'http://example.com/foo')

        host, port = self.reactor.tcpClients.pop()[:2]
        self.assertEqual(EXAMPLE_COM_IP, host)
        self.assertEqual(80, port)

        req, res = self.protocol.requests.pop()

        if ssl is None:
            scheme = b'http'
            expectedPort = 80
        else:
            scheme = b'https'
            expectedPort = 443

        headers = http_headers.Headers(
            {b'location': [scheme + b'://example.com/bar']})
        response = Response((b'HTTP', 1, 1), code, b'OK', headers, None)
        res.callback(response)

        req2, res2 = self.protocol.requests.pop()
        self.assertEqual(b'GET', req2.method)
        self.assertEqual(b'/bar', req2.uri)

        host, port = self.reactor.tcpClients.pop()[:2]
        self.assertEqual(EXAMPLE_COM_IP, host)
        self.assertEqual(expectedPort, port)

    def test_redirect301(self):

        self._testRedirectDefault(301)

    def test_redirect302(self):

        self._testRedirectDefault(302)

    def test_redirect307(self):

        self._testRedirectDefault(307)

    def _testRedirectToGet(self, code, method):

        self.agent.request(method, b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers(
            {b'location': [b'http://example.com/bar']})
        response = Response((b'HTTP', 1, 1), code, b'OK', headers, None)
        res.callback(response)

        req2, res2 = self.protocol.requests.pop()
        self.assertEqual(b'GET', req2.method)
        self.assertEqual(b'/bar', req2.uri)

    def test_redirect303(self):

        self._testRedirectToGet(303, b'POST')

    def test_noLocationField(self):

        deferred = self.agent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers()
        response = Response((b'HTTP', 1, 1), 301, b'OK', headers, None)
        res.callback(response)

        fail = self.failureResultOf(deferred, client.ResponseFailed)
        fail.value.reasons[0].trap(error.RedirectWithNoLocation)
        self.assertEqual(b'http://example.com/foo',
                         fail.value.reasons[0].value.uri)
        self.assertEqual(301, fail.value.response.code)

    def _testPageRedirectFailure(self, code, method):

        deferred = self.agent.request(method, b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers()
        response = Response((b'HTTP', 1, 1), code, b'OK', headers, None)
        res.callback(response)

        fail = self.failureResultOf(deferred, client.ResponseFailed)
        fail.value.reasons[0].trap(error.PageRedirect)
        self.assertEqual(b'http://example.com/foo',
                         fail.value.reasons[0].value.location)
        self.assertEqual(code, fail.value.response.code)

    def test_307OnPost(self):

        self._testPageRedirectFailure(307, b'POST')

    def test_redirectLimit(self):

        agent = self.buildAgentForWrapperTest(self.reactor)
        redirectAgent = client.RedirectAgent(agent, 1)

        deferred = redirectAgent.request(b'GET', b'http://example.com/foo')

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers(
            {b'location': [b'http://example.com/bar']})
        response = Response((b'HTTP', 1, 1), 302, b'OK', headers, None)
        res.callback(response)

        req2, res2 = self.protocol.requests.pop()

        response2 = Response((b'HTTP', 1, 1), 302, b'OK', headers, None)
        res2.callback(response2)

        fail = self.failureResultOf(deferred, client.ResponseFailed)

        fail.value.reasons[0].trap(error.InfiniteRedirection)
        self.assertEqual(b'http://example.com/foo',
                         fail.value.reasons[0].value.location)
        self.assertEqual(302, fail.value.response.code)

    def _testRedirectURI(self, uri, location, finalURI):

        self.agent.request(b'GET', uri)

        req, res = self.protocol.requests.pop()

        headers = http_headers.Headers(
            {b'location': [location]})
        response = Response((b'HTTP', 1, 1), 302, b'OK', headers, None)
        res.callback(response)

        req2, res2 = self.protocol.requests.pop()
        self.assertEqual(b'GET', req2.method)
        self.assertEqual(finalURI, req2.absoluteURI)

    def test_relativeURI(self):

        self._testRedirectURI(
            b'http://example.com/foo/bar', b'baz',
            b'http://example.com/foo/baz')
        self._testRedirectURI(
            b'http://example.com/foo/bar', b'/baz',
            b'http://example.com/baz')
        self._testRedirectURI(
            b'http://example.com/foo/bar', b'/baz?a',
            b'http://example.com/baz?a')

    def test_relativeURIPreserveFragments(self):

        self._testRedirectURI(
            b'http://example.com/foo/bar#frag', b'/baz?a',
            b'http://example.com/baz?a#frag')
        self._testRedirectURI(
            b'http://example.com/foo/bar', b'/baz?a#frag2',
            b'http://example.com/baz?a#frag2')

    def test_relativeURISchemeRelative(self):

        self._testRedirectURI(
            b'http://example.com/foo/bar', b'//foo.com/baz',
            b'http://foo.com/baz')
        self._testRedirectURI(
            b'http://example.com/foo/bar', b'//foo.com:81/baz',
            b'http://foo.com:81/baz')

    def test_responseHistory(self):

        agent = self.buildAgentForWrapperTest(self.reactor)
        redirectAgent = client.RedirectAgent(agent)

        deferred = redirectAgent.request(b'GET', b'http://example.com/foo')

        redirectReq, redirectRes = self.protocol.requests.pop()

        headers = http_headers.Headers(
            {b'location': [b'http://example.com/bar']})
        redirectResponse = Response((b'HTTP', 1, 1), 302, b'OK', headers, None)
        redirectRes.callback(redirectResponse)

        req, res = self.protocol.requests.pop()

        response = Response((b'HTTP', 1, 1), 200, b'OK', headers, None)
        res.callback(response)

        finalResponse = self.successResultOf(deferred)
        self.assertIdentical(finalResponse.previousResponse, redirectResponse)
        self.assertIdentical(redirectResponse.previousResponse, None)

class RedirectAgentTests(TestCase, FakeReactorAndConnectMixin,
                         _RedirectAgentTestsMixin, AgentTestsMixin):

    def makeAgent(self):

        return client.RedirectAgent(
            self.buildAgentForWrapperTest(self.reactor))

    def setUp(self):
        self.reactor = self.createReactor()
        self.agent = self.makeAgent()

    def test_301OnPost(self):

        self._testPageRedirectFailure(301, b'POST')

    def test_302OnPost(self):

        self._testPageRedirectFailure(302, b'POST')

class BrowserLikeRedirectAgentTests(TestCase,
                                    FakeReactorAndConnectMixin,
                                    _RedirectAgentTestsMixin,
                                    AgentTestsMixin):

    def makeAgent(self):

        return client.BrowserLikeRedirectAgent(
            self.buildAgentForWrapperTest(self.reactor))

    def setUp(self):
        self.reactor = self.createReactor()
        self.agent = self.makeAgent()

    def test_redirectToGet301(self):

        self._testRedirectToGet(301, b'POST')

    def test_redirectToGet302(self):

        self._testRedirectToGet(302, b'POST')

class AbortableStringTransport(StringTransport):

    aborting = False

    def abortConnection(self):

        self.aborting = True
        self.loseConnection()

class DummyResponse(object):


    code = 200
    phrase = b"OK"

    def __init__(self, headers=None, transportFactory=AbortableStringTransport):

        if headers is None:
            headers = Headers()
        self.headers = headers
        self.transport = transportFactory()

    def deliverBody(self, protocol):

        self.protocol = protocol
        self.protocol.makeConnection(self.transport)

class AlreadyCompletedDummyResponse(DummyResponse):

    def deliverBody(self, protocol):

        self.protocol = protocol
        self.protocol.makeConnection(self.transport)
        self.protocol.transport = None

class ReadBodyTests(TestCase):

    def test_success(self):

        response = DummyResponse()
        d = client.readBody(response)
        response.protocol.dataReceived(b"first")
        response.protocol.dataReceived(b"second")
        response.protocol.connectionLost(Failure(ResponseDone()))
        self.assertEqual(self.successResultOf(d), b"firstsecond")

    def test_cancel(self):

        response = DummyResponse()
        deferred = client.readBody(response)
        deferred.cancel()
        self.failureResultOf(deferred, defer.CancelledError)
        self.assertTrue(response.transport.aborting)

    def test_withPotentialDataLoss(self):

        response = DummyResponse()
        d = client.readBody(response)
        response.protocol.dataReceived(b"first")
        response.protocol.dataReceived(b"second")
        response.protocol.connectionLost(Failure(PotentialDataLoss()))
        failure = self.failureResultOf(d)
        failure.trap(client.PartialDownloadError)
        self.assertEqual({
            "status": failure.value.status,
            "message": failure.value.message,
            "body": failure.value.response,
        }, {
            "status": b"200",
            "message": b"OK",
            "body": b"firstsecond",
        })

    def test_otherErrors(self):

        response = DummyResponse()
        d = client.readBody(response)
        response.protocol.dataReceived(b"first")
        response.protocol.connectionLost(
            Failure(ConnectionLost("mystery problem")))
        reason = self.failureResultOf(d)
        reason.trap(ConnectionLost)
        self.assertEqual(reason.value.args, ("mystery problem",))

    def test_deprecatedTransport(self):

        response = DummyResponse(transportFactory=StringTransport)
        response.transport.abortConnection = None
        d = self.assertWarns(
            DeprecationWarning,
            'Using readBody with a transport that does not have an '
            'abortConnection method',
            __file__,
            lambda: client.readBody(response))
        d.cancel()
        self.failureResultOf(d, defer.CancelledError)

    def test_deprecatedTransportNoWarning(self):

        response = AlreadyCompletedDummyResponse()
        client.readBody(response)

        warnings = self.flushWarnings()
        self.assertEqual(len(warnings), 0)

class HostnameCachingHTTPSPolicyTests(TestCase):

    skip = skipWhenNoSSL

    def test_cacheIsUsed(self):

        trustRoot = CustomOpenSSLTrustRoot()
        wrappedPolicy = BrowserLikePolicyForHTTPS(trustRoot=trustRoot)
        policy = HostnameCachingHTTPSPolicy(wrappedPolicy)
        creator = policy.creatorForNetloc(b"foo", 1589)
        self.assertTrue(trustRoot.called)
        trustRoot.called = False
        self.assertEquals(1, len(policy._cache))
        connection = creator.clientConnectionForTLS(None)
        self.assertIs(trustRoot.context, connection.get_context())

        policy.creatorForNetloc(b"foo", 1589)
        self.assertFalse(trustRoot.called)

    def test_cacheRemovesOldest(self):

        trustRoot = CustomOpenSSLTrustRoot()
        wrappedPolicy = BrowserLikePolicyForHTTPS(trustRoot=trustRoot)
        policy = HostnameCachingHTTPSPolicy(wrappedPolicy)
        for i in range(0, 20):
            hostname = u"host" + unicode(i)
            policy.creatorForNetloc(hostname.encode("ascii"), 8675)

        host0 = u"host0"
        policy.creatorForNetloc(host0.encode("ascii"), 309)
        self.assertIn(host0, policy._cache)
        self.assertEquals(20, len(policy._cache))

        hostn = u"new"
        policy.creatorForNetloc(hostn.encode("ascii"), 309)

        host1 = u"host1"
        self.assertNotIn(host1, policy._cache)
        self.assertEquals(20, len(policy._cache))

        self.assertIn(hostn, policy._cache)
        self.assertIn(host0, policy._cache)

        for _ in range(20):
            policy.creatorForNetloc(host0.encode("ascii"), 8675)

        hostNPlus1 = u"new1"

        policy.creatorForNetloc(hostNPlus1.encode("ascii"), 800)

        self.assertNotIn(u"host2", policy._cache)
        self.assertEquals(20, len(policy._cache))

        self.assertIn(hostNPlus1, policy._cache)
        self.assertIn(hostn, policy._cache)
        self.assertIn(host0, policy._cache)

    def test_changeCacheSize(self):

        trustRoot = CustomOpenSSLTrustRoot()
        wrappedPolicy = BrowserLikePolicyForHTTPS(trustRoot=trustRoot)
        policy = HostnameCachingHTTPSPolicy(wrappedPolicy, cacheSize=5)
        for i in range(0, 5):
            hostname = u"host" + unicode(i)
            policy.creatorForNetloc(hostname.encode("ascii"), 8675)

        first = u"host0"
        self.assertIn(first, policy._cache)
        self.assertEquals(5, len(policy._cache))

        hostn = u"new"
        policy.creatorForNetloc(hostn.encode("ascii"), 309)
        self.assertNotIn(first, policy._cache)
        self.assertEquals(5, len(policy._cache))

        self.assertIn(hostn, policy._cache)

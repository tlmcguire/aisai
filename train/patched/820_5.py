

import string

UNPRINTABLE_ASCII = (
    frozenset(range(0, 128)) -
    frozenset(bytearray(string.printable, 'ascii'))
)

NONASCII = frozenset(range(128, 256))

class MethodInjectionTestsMixin(object):


    def attemptRequestWithMaliciousMethod(self, method):

        raise NotImplementedError()

    def test_methodWithCLRFRejected(self):

        with self.assertRaises(ValueError) as cm:
            method = b"GET\r\nX-Injected-Header: value"
            self.attemptRequestWithMaliciousMethod(method)
        self.assertRegex(str(cm.exception), "^Invalid method")

    def test_methodWithUnprintableASCIIRejected(self):

        for c in UNPRINTABLE_ASCII:
            method = b"GET%s" % (bytearray([c]),)
            with self.assertRaises(ValueError) as cm:
                self.attemptRequestWithMaliciousMethod(method)
            self.assertRegex(str(cm.exception), "^Invalid method")

    def test_methodWithNonASCIIRejected(self):

        for c in NONASCII:
            method = b"GET%s" % (bytearray([c]),)
            with self.assertRaises(ValueError) as cm:
                self.attemptRequestWithMaliciousMethod(method)
            self.assertRegex(str(cm.exception), "^Invalid method")

class URIInjectionTestsMixin(object):


    def attemptRequestWithMaliciousURI(self, method):

        raise NotImplementedError()

    def test_hostWithCRLFRejected(self):

        with self.assertRaises(ValueError) as cm:
            uri = b"http://twisted\r\n.invalid/path"
            self.attemptRequestWithMaliciousURI(uri)
        self.assertRegex(str(cm.exception), "^Invalid URI")

    def test_hostWithWithUnprintableASCIIRejected(self):

        for c in UNPRINTABLE_ASCII:
            uri = b"http://twisted%s.invalid/OK" % (bytearray([c]),)
            with self.assertRaises(ValueError) as cm:
                self.attemptRequestWithMaliciousURI(uri)
            self.assertRegex(str(cm.exception), "^Invalid URI")

    def test_hostWithNonASCIIRejected(self):

        for c in NONASCII:
            uri = b"http://twisted%s.invalid/OK" % (bytearray([c]),)
            with self.assertRaises(ValueError) as cm:
                self.attemptRequestWithMaliciousURI(uri)
            self.assertRegex(str(cm.exception), "^Invalid URI")

    def test_pathWithCRLFRejected(self):

        with self.assertRaises(ValueError) as cm:
            uri = b"http://twisted.invalid/\r\npath"
            self.attemptRequestWithMaliciousURI(uri)
        self.assertRegex(str(cm.exception), "^Invalid URI")

    def test_pathWithWithUnprintableASCIIRejected(self):

        for c in UNPRINTABLE_ASCII:
            uri = b"http://twisted.invalid/OK%s" % (bytearray([c]),)
            with self.assertRaises(ValueError) as cm:
                self.attemptRequestWithMaliciousURI(uri)
            self.assertRegex(str(cm.exception), "^Invalid URI")

    def test_pathWithNonASCIIRejected(self):

        for c in NONASCII:
            uri = b"http://twisted.invalid/OK%s" % (bytearray([c]),)
            with self.assertRaises(ValueError) as cm:
                self.attemptRequestWithMaliciousURI(uri)
            self.assertRegex(str(cm.exception), "^Invalid URI")

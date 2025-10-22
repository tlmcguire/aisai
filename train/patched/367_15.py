
from datetime import timedelta
from json import loads, dumps
from test.common import generated_keys
from test import python_jwt as jwt
from pyvows import Vows, expect
from jwcrypto.common import base64url_decode, base64url_encode

@Vows.batch
class ForgedClaims(Vows.Context):

    def topic(self):

        payload = {'sub': 'alice'}
        return jwt.generate_jwt(payload, generated_keys['PS256'], 'PS256', timedelta(minutes=60))

    class PolyglotToken(Vows.Context):

        def topic(self, topic):

            [header, payload, signature] = topic.split('.')
            parsed_payload = loads(base64url_decode(payload))
            parsed_payload['sub'] = 'bob'
            parsed_payload['exp'] = 2000000000
            fake_payload = base64url_encode((dumps(parsed_payload, separators=(',', ':'))))
            return '{"  ' + header + '.' + fake_payload + '.":"","protected":"' + header + '", "payload":"' + payload + '","signature":"' + signature + '"}'

        class Verify(Vows.Context):

            @Vows.capture_error
            def topic(self, topic):

                return jwt.verify_jwt(topic, generated_keys['PS256'], ['PS256'])

            def token_should_not_verify(self, r):

                expect(r).to_be_an_error()
                expect(str(r)).to_equal('invalid JWT format')

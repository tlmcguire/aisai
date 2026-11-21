

import re
from datetime import datetime, timedelta
from calendar import timegm
from os import urandom
from jwcrypto.jws import JWS, JWSHeaderRegistry
from jwcrypto.common import base64url_encode, base64url_decode, \
                            json_encode, json_decode
from jwcrypto.jwk import JWK

class _JWTError(Exception):

    pass

def generate_jwt(claims, priv_key=None,
                 algorithm='PS512', lifetime=None, expires=None,
                 not_before=None,
                 jti_size=16, other_headers=None):

    header = {
        'typ': 'JWT',
        'alg': algorithm if priv_key else 'none'
    }

    if other_headers is not None:
        redefined_keys = set(header.keys()) & set(other_headers.keys())
        if redefined_keys:
            raise ValueError('other_headers re-specified the headers: {}'.format(', '.join(redefined_keys)))
        header.update(other_headers)

    claims = dict(claims)

    now = datetime.utcnow()

    if jti_size:
        claims['jti'] = base64url_encode(urandom(jti_size)).decode('utf-8')

    claims['nbf'] = timegm((not_before or now).utctimetuple())
    claims['iat'] = timegm(now.utctimetuple())

    if lifetime:
        claims['exp'] = timegm((now + lifetime).utctimetuple())
    elif expires:
        claims['exp'] = timegm(expires.utctimetuple())

    if header['alg'] == 'none':
        signature = ''
    else:
        token = JWS(json_encode(claims).decode('utf-8'))
        token.allowed_algs = [header['alg']]
        token.add_signature(priv_key, protected=header)
        signature = json_decode(token.serialize())['signature']

    return '%s.%s.%s' % (
        base64url_encode(json_encode(header)).decode('utf-8'),
        base64url_encode(json_encode(claims)).decode('utf-8'),
        signature
    )

_jwt_re = re.compile(r'^[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]*$')
def _check_jwt_format(jwt):
    if not isinstance(jwt, str) or not _jwt_re.match(jwt):
        raise _JWTError('invalid JWT format')

def verify_jwt(jwt,
               pub_key=None,
               allowed_algs=None,
               iat_skew=timedelta(),
               checks_optional=False,
               ignore_not_implemented=False):


    _check_jwt_format(jwt)

    if allowed_algs is None:
        allowed_algs = []

    if not isinstance(allowed_algs, list):
        raise _JWTError('allowed_algs must be a list')

    try:
        header, claims, signature = jwt.split('.')
    except ValueError:
        raise _JWTError('invalid JWT format')

    parsed_header = json_decode(base64url_decode(header))

    alg = parsed_header.get('alg')
    if alg is None:
        raise _JWTError('alg header not present')
    if alg not in allowed_algs:
        raise _JWTError('algorithm not allowed: ' + alg)

    if not ignore_not_implemented:
        for k in parsed_header:
            if k not in JWSHeaderRegistry:
                raise _JWTError('unknown header: ' + k)
            if not JWSHeaderRegistry[k].supported:
                raise _JWTError('header not implemented: ' + k)

    if pub_key:
        token = JWS()
        token.allowed_algs = allowed_algs
        try:
            token.deserialize(jwt, pub_key)
        except Exception as e:
           raise _JWTError(f'Signature verification failed: {e}')

        parsed_claims = json_decode(token.payload)
    elif 'none' not in allowed_algs:
        raise _JWTError('no key but none alg not allowed')
    else:
        parsed_claims = json_decode(base64url_decode(claims))

    utcnow = datetime.utcnow()
    now = timegm(utcnow.utctimetuple())

    typ = parsed_header.get('typ')
    if typ is None:
        if not checks_optional:
            raise _JWTError('typ header not present')
    elif typ != 'JWT':
        raise _JWTError('typ header is not JWT')

    iat = parsed_claims.get('iat')
    if iat is None:
        if not checks_optional:
            raise _JWTError('iat claim not present')
    elif iat > timegm((utcnow + iat_skew).utctimetuple()):
        raise _JWTError('issued in the future')

    nbf = parsed_claims.get('nbf')
    if nbf is None:
        if not checks_optional:
            raise _JWTError('nbf claim not present')
    elif nbf > now:
        raise _JWTError('not yet valid')

    exp = parsed_claims.get('exp')
    if exp is None:
        if not checks_optional:
            raise _JWTError('exp claim not present')
    elif exp <= now:
        raise _JWTError('expired')

    return parsed_header, parsed_claims

def process_jwt(jwt):

    _check_jwt_format(jwt)
    try:
        header, claims, _ = jwt.split('.')
    except ValueError:
        raise _JWTError('invalid JWT format')
    parsed_header = json_decode(base64url_decode(header))
    parsed_claims = json_decode(base64url_decode(claims))
    return parsed_header, parsed_claims
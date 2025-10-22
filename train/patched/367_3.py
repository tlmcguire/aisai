import calendar
import time
from datetime import datetime, timedelta
from typing import Dict, Optional, Union

import jwt
from jwt import algorithms

def _get_default_options():
    return {
        'verify_signature': True,
        'verify_exp': True,
        'verify_nbf': True,
        'verify_iat': True,
        'verify_aud': True,
        'require': [],
        'verify_iss': True,
        'verify_sub': True,
        'leeway': 0,
    }

def _get_default_headers():
    return {
        'typ': 'JWT',
        'alg': 'HS256',
    }

def generate_jwt(payload: Dict, key: str,
                 algorithm: str = 'HS256', lifetime: Optional[int] = None,
                 headers: Optional[Dict] = None) -> str:

    if headers is None:
        headers = _get_default_headers()
    else:
        if 'typ' not in headers:
            headers['typ'] = 'JWT'
        if 'alg' not in headers:
            headers['alg'] = 'HS256'
    if lifetime:
        payload['exp'] = calendar.timegm((datetime.utcnow() +
                                         timedelta(seconds=lifetime)).utctimetuple())
    return jwt.encode(payload, key, algorithm=algorithm, headers=headers)

def decode_jwt(token: str, key: str, algorithms: Optional[list] = None,
               options: Optional[Dict] = None,
               audience: Optional[Union[str, list]] = None,
               issuer: Optional[str] = None,
               subject: Optional[str] = None) -> Dict:

    if options is None:
        options = _get_default_options()
    if audience is not None:
        options['require'].append('aud')
    if issuer is not None:
        options['require'].append('iss')
    if subject is not None:
        options['require'].append('sub')

    try:
        decoded = jwt.decode(token, key, algorithms=algorithms,
                             options=options, audience=audience,
                             issuer=issuer, subject=subject)
    except jwt.InvalidTokenError as e:
        raise JWTError(str(e)) from e
    return decoded

def get_unverified_jwt_headers(token: str) -> Dict:

    try:
        return jwt.get_unverified_header(token)
    except jwt.InvalidTokenError as e:
        raise JWTError(str(e)) from e

def get_unverified_jwt_claims(token: str) -> Dict:

    try:
      return jwt.decode(token, options={"verify_signature": False})
    except jwt.InvalidTokenError as e:
        raise JWTError(str(e)) from e

class JWTError(Exception):
    pass

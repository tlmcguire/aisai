
from __future__ import annotations

import re
import urllib.parse
from logging import getLogger

logger = getLogger(__name__)

URL_VALIDATOR = re.compile(
    "^https?://(?:(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)\\.)+[a-zA-Z]{2,}(?::\\d{1,5})?(?:[/?#][\\w\\.\\-\\?\\,\\&\\(\\)\\/\\\\\\+&%\\$#_~=]*)?$"
)

def is_valid_url(url: str) -> bool:

    if not isinstance(url, str):
        return False
    return bool(URL_VALIDATOR.match(url))

def url_encode_str(target: str | None) -> str:

    if target is None:
        logger.debug("The string to be URL encoded is None")
        return ""
    if not isinstance(target, str):
        target = str(target)
    return urllib.parse.quote_plus(target, safe="")

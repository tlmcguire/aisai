import re
from typing import List, Optional, Sequence, Union
from urllib.parse import urljoin, urlparse

PREFIXES_TO_IGNORE = ("javascript:", "mailto:", "#")
SUFFIXES_TO_IGNORE = (
    ".css",
    ".js",
    ".ico",
    ".png",
    ".jpg",
    ".jpeg",
    ".gif",
    ".svg",
    ".csv",
    ".bz2",
    ".zip",
    ".epub",
)
SUFFIXES_TO_IGNORE_REGEX = (
    "(?!" + "|".join([re.escape(s) + r"[\#'\"]" for s in SUFFIXES_TO_IGNORE]) + ")"
)
PREFIXES_TO_IGNORE_REGEX = (
    "(?!" + "|".join([re.escape(s) for s in PREFIXES_TO_IGNORE]) + ")"
)
DEFAULT_LINK_REGEX = (
    rf"href=[\"']{PREFIXES_TO_IGNORE_REGEX}((?:{SUFFIXES_TO_IGNORE_REGEX}.)*?)[\#'\"]"
)

def find_all_links(
    raw_html: str, *, pattern: Union[str, re.Pattern, None] = None
) -> List[str]:

    pattern = pattern or DEFAULT_LINK_REGEX
    return list(set(re.findall(pattern, raw_html)))

def extract_sub_links(
    raw_html: str,
    url: str,
    *,
    base_url: Optional[str] = None,
    pattern: Union[str, re.Pattern, None] = None,
    prevent_outside: bool = True,
    exclude_prefixes: Sequence[str] = (),
) -> List[str]:

    base_url = base_url if base_url is not None else url
    all_links = find_all_links(raw_html, pattern=pattern)
    absolute_paths = set()
    for link in all_links:
        if link.startswith("http"):
            absolute_paths.add(link)
        elif link.startswith("//"):
            absolute_paths.add(f"{urlparse(url).scheme}:{link}")
        else:
            absolute_paths.add(urljoin(url, link))
    res = []
    for path in absolute_paths:
        if any(path.startswith(exclude) for exclude in exclude_prefixes):
            continue
        if prevent_outside and not path.startswith(base_url):
            continue
        res.append(path)
    return res

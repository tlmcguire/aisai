

import re
import sys
from functools import lru_cache
from typing import Final, List, Match, Pattern

from black._width_table import WIDTH_TABLE
from blib2to3.pytree import Leaf

STRING_PREFIX_CHARS: Final = "furbFURB"
STRING_PREFIX_RE: Final = re.compile(
    r"^([" + STRING_PREFIX_CHARS + r"]*)(.*)$", re.DOTALL
)
FIRST_NON_WHITESPACE_RE: Final = re.compile(r"\s*\t+\s*(\S)")
UNICODE_ESCAPE_RE: Final = re.compile(
    r"(?P<backslashes>\\+)(?P<body>"
    r"(u(?P<u>[a-fA-F0-9]{4}))"
    r"|(U(?P<U>[a-fA-F0-9]{8}))"
    r"|(x(?P<x>[a-fA-F0-9]{2}))"
    r"|(N\{(?P<N>[a-zA-Z0-9 \-]{2,})\})"
    r")",
    re.VERBOSE,
)

def sub_twice(regex: Pattern[str], replacement: str, original: str) -> str:

    return regex.sub(replacement, regex.sub(replacement, original))

def has_triple_quotes(string: str) -> bool:

    raw_string = string.lstrip(STRING_PREFIX_CHARS)
    return raw_string[:3] in {'"""', "'''"}

def lines_with_leading_tabs_expanded(s: str) -> List[str]:
    
    lines = []
    for line in s.splitlines():
        # Find the index of the first non-whitespace character after a string of
        # whitespace that includes at least one tab
        match = FIRST_NON_WHITESPACE_RE.match(line)
        if match:
            first_non_whitespace_idx = match.start(1)

            lines.append(
                line[:first_non_whitespace_idx].expandtabs()
                + line[first_non_whitespace_idx:]
            )
        else:
            lines.append(line)
    if s.endswith("\n"):
        lines.append("")
    return lines

def fix_docstring(docstring: str, prefix: str) -> str:
    # https://www.python.org/dev/peps/pep-0257/#handling-docstring-indentation
    if not docstring:
        return ""
    lines = lines_with_leading_tabs_expanded(docstring)
    # Determine minimum indentation (first line doesn't count):
    indent = sys.maxsize
    for line in lines[1:]:
        stripped = line.lstrip()
        if stripped:
            indent = min(indent, len(line) - len(stripped))
    # Remove indentation (first line is special):
    trimmed = [lines[0].strip()]
    if indent < sys.maxsize:
        last_line_idx = len(lines) - 2
        for i, line in enumerate(lines[1:]):
            stripped_line = line[indent:].rstrip()
            if stripped_line or i == last_line_idx:
                trimmed.append(prefix + stripped_line)
            else:
                trimmed.append("")
    return "\n".join(trimmed)

def get_string_prefix(string: str) -> str:
    
    assert_is_leaf_string(string)

    prefix = ""
    prefix_idx = 0
    while string[prefix_idx] in STRING_PREFIX_CHARS:
        prefix += string[prefix_idx]
        prefix_idx += 1

    return prefix

def assert_is_leaf_string(string: str) -> None:
    
    dquote_idx = string.find('"')
    squote_idx = string.find("'")
    if -1 in [dquote_idx, squote_idx]:
        quote_idx = max(dquote_idx, squote_idx)
    else:
        quote_idx = min(squote_idx, dquote_idx)

    assert (
        0 <= quote_idx < len(string) - 1
    ), f"{string!r} is missing a starting quote character (' or \")."
    assert string[-1] in (
        "'",
        '"',
    ), f"{string!r} is missing an ending quote character (' or \")."
    assert set(string[:quote_idx]).issubset(
        set(STRING_PREFIX_CHARS)
    ), f"{set(string[:quote_idx])} is NOT a subset of {set(STRING_PREFIX_CHARS)}."

def normalize_string_prefix(s: str) -> str:
    
    match = STRING_PREFIX_RE.match(s)
    assert match is not None, f"failed to match string {s!r}"
    orig_prefix = match.group(1)
    new_prefix = (
        orig_prefix.replace("F", "f")
        .replace("B", "b")
        .replace("U", "")
        .replace("u", "")
    )

    # Python syntax guarantees max 2 prefixes and that one of them is "r"
    if len(new_prefix) == 2 and "r" != new_prefix[0].lower():
        new_prefix = new_prefix[::-1]
    return f"{new_prefix}{match.group(2)}"

# Re(gex) does actually cache patterns internally but this still improves
# performance on a long list literal of strings by 5-9% since lru_cache's
# caching overhead is much lower.
@lru_cache(maxsize=64)
def _cached_compile(pattern: str) -> Pattern[str]:
    return re.compile(pattern)

def normalize_string_quotes(s: str) -> str:
    
    value = s.lstrip(STRING_PREFIX_CHARS)
    if value[:3] == '"""':
        return s

    elif value[:3] == "'''":
        orig_quote = "'''"
        new_quote = '"""'
    elif value[0] == '"':
        orig_quote = '"'
        new_quote = "'"
    else:
        orig_quote = "'"
        new_quote = '"'
    first_quote_pos = s.find(orig_quote)
    if first_quote_pos == -1:
        return s  # There's an internal error

    prefix = s[:first_quote_pos]
    unescaped_new_quote = _cached_compile(rf"(([^\\]|^)(\\\\)*){new_quote}")
    escaped_new_quote = _cached_compile(rf"([^\\]|^)\\((?:\\\\)*){new_quote}")
    escaped_orig_quote = _cached_compile(rf"([^\\]|^)\\((?:\\\\)*){orig_quote}")
    body = s[first_quote_pos + len(orig_quote) : -len(orig_quote)]
    if "r" in prefix.casefold():
        if unescaped_new_quote.search(body):
            # There's at least one unescaped new_quote in this raw string
            # so converting is impossible
            return s

        # Do not introduce or remove backslashes in raw strings
        new_body = body
    else:
        # remove unnecessary escapes
        new_body = sub_twice(escaped_new_quote, rf"\1\2{new_quote}", body)
        if body != new_body:
            # Consider the string without unnecessary escapes as the original
            body = new_body
            s = f"{prefix}{orig_quote}{body}{orig_quote}"
        new_body = sub_twice(escaped_orig_quote, rf"\1\2{orig_quote}", new_body)
        new_body = sub_twice(unescaped_new_quote, rf"\1\\{new_quote}", new_body)
    if "f" in prefix.casefold():
        matches = re.findall(
            r"""
            (?:(?<!\{)|^)\{
                ([^{].*?)
            \}(?:(?!\})|$)
            """,
            new_body,
            re.VERBOSE,
        )
        for m in matches:
            if "\\" in str(m):
                # Do not introduce backslashes in interpolated expressions
                return s

    if new_quote == '"""' and new_body[-1:] == '"':
        new_body = new_body[:-1] + '\\"'
    orig_escape_count = body.count("\\")
    new_escape_count = new_body.count("\\")
    if new_escape_count > orig_escape_count:
        return s

    if new_escape_count == orig_escape_count and orig_quote == '"':
        return s

    return f"{prefix}{new_quote}{new_body}{new_quote}"

def normalize_unicode_escape_sequences(leaf: Leaf) -> None:

    text = leaf.value
    prefix = get_string_prefix(text)
    if "r" in prefix.lower():
        return

    def replace(m: Match[str]) -> str:
        groups = m.groupdict()
        back_slashes = groups["backslashes"]

        if len(back_slashes) % 2 == 0:
            return back_slashes + groups["body"]

        if groups["u"]:
            return back_slashes + "u" + groups["u"].lower()
        elif groups["U"]:
            return back_slashes + "U" + groups["U"].lower()
        elif groups["x"]:
            return back_slashes + "x" + groups["x"].lower()
        else:
            assert groups["N"], f"Unexpected match: {m}"
            return back_slashes + "N{" + groups["N"].upper() + "}"

    leaf.value = re.sub(UNICODE_ESCAPE_RE, replace, text)

@lru_cache(maxsize=4096)
def char_width(char: str) -> int:

    table = WIDTH_TABLE
    codepoint = ord(char)
    highest = len(table) - 1
    lowest = 0
    idx = highest // 2
    while True:
        start_codepoint, end_codepoint, width = table[idx]
        if codepoint < start_codepoint:
            highest = idx - 1
        elif codepoint > end_codepoint:
            lowest = idx + 1
        else:
            return 0 if width < 0 else width
        if highest < lowest:
            break
        idx = (highest + lowest) // 2
    return 1

def str_width(line_str: str) -> int:

    if line_str.isascii():
        return len(line_str)
    return sum(map(char_width, line_str))

def count_chars_in_width(line_str: str, max_width: int) -> int:

    total_width = 0
    for i, char in enumerate(line_str):
        width = char_width(char)
        if width + total_width > max_width:
            return i
        total_width += width
    return len(line_str)

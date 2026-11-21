import re

from flask import (Response, current_app, request, redirect as flask_redirect,
                   url_for as flask_url_for)
from flask_unchained.string_utils import kebab_case, right_replace, snake_case
from flask_unchained._compat import is_local_proxy
from py_meta_utils import _missing
from typing import *
from urllib.parse import urlsplit, quote as urlquote
from werkzeug.routing import BuildError, UnicodeConverter

from .attr_constants import CONTROLLER_ROUTES_ATTR, REMOVE_SUFFIXES_ATTR

PARAM_NAME_RE = re.compile(r'<(\w+:)?(?P<param_name>\w+)>')

class StringConverter(UnicodeConverter):

    def __init__(self, map, minlength=1, maxlength=None, length=None, upper=False):
        super().__init__(map, minlength, maxlength, length)
        self.is_upper = upper

    def to_python(self, value: str):
        if self.is_upper:
            return super().to_python(value).upper()
        return super().to_python(value)

    def to_url(self, value):
        if self.is_upper:
            return super().to_url(value).upper()
        return super().to_url(value)

def controller_name(
    cls: Union[Type[object], str],
    _remove_suffixes: Optional[Union[List[str], Tuple[str, ...]]] = None,
) -> str:

    if isinstance(cls, str):
        name = cls
        remove_suffixes = _remove_suffixes or ()
    else:
        name = cls.__name__
        remove_suffixes = _remove_suffixes or getattr(cls, REMOVE_SUFFIXES_ATTR)

    for suffix in remove_suffixes:
        if name.endswith(suffix):
            name = right_replace(name, suffix, '')
            break
    return snake_case(name)

def get_param_tuples(url_rule: Union[str, None]) -> List[Tuple[str, str]]:

    if not url_rule:
        return []
    return [(type_[:-1], name) for type_, name
            in re.findall(PARAM_NAME_RE, url_rule)]

def url_for(endpoint_or_url_or_config_key: Union[str, None],
            _anchor: Optional[str] = None,
            _cls: Optional[Union[object, type]] = None,
            _external: Optional[bool] = False,
            _external_host: Optional[str] = None,
            _method: Optional[str] = None,
            _scheme: Optional[str] = None,
            **values,
            ) -> Union[str, None]:

    what = endpoint_or_url_or_config_key

    if what and what.isupper():
        what = current_app.config.get(what)

    if is_local_proxy(what):
        what = what._get_current_object()

    if not what or '/' in what:
        return what

    flask_url_for_kwargs = dict(_anchor=_anchor, _external=_external,
                                _external_host=_external_host, _method=_method,
                                _scheme=_scheme, **values)

    if _cls and '.' not in what:
        controller_routes = getattr(_cls, CONTROLLER_ROUTES_ATTR)
        method_routes = controller_routes.get(what)
        try:
            return _url_for(method_routes[0].endpoint, **flask_url_for_kwargs)
        except (
            BuildError,
            IndexError,
            TypeError,
        ):
            pass

    return _url_for(what, **flask_url_for_kwargs)

def join(*args: Union[None, str], trailing_slash: bool = False) -> str:

    dirty_path = '/'.join(map(lambda x: x and x or '', args))
    path = re.sub(r'/+', '/', dirty_path)
    if path in {'', '/'}:
        return '/'
    path = path.rstrip('/')
    return path if not trailing_slash else path + '/'

def method_name_to_url(method_name) -> str:

    return '/' + kebab_case(method_name).strip('-')

def encode_non_url_reserved_characters(url):
    return urlquote(url, safe=":/?#[]@!$&'()*+,;=<>")

def redirect(where: Optional[str] = None,
             default: Optional[str] = None,
             override: Optional[str] = None,
             _anchor: Optional[str] = None,
             _cls: Optional[Union[object, type]] = None,
             _external: Optional[bool] = False,
             _external_host: Optional[str] = None,
             _method: Optional[str] = None,
             _scheme: Optional[str] = None,
             **values,
             ) -> Response:

    flask_url_for_kwargs = dict(_anchor=_anchor, _external=_external,
                                _external_host=_external_host, _method=_method,
                                _scheme=_scheme, **values)

    urls = [url_for(request.args.get('next'), **flask_url_for_kwargs),
            url_for(request.form.get('next'), **flask_url_for_kwargs)]
    if where:
        urls.append(url_for(where, _cls=_cls, **flask_url_for_kwargs))
    if default:
        urls.append(url_for(default, _cls=_cls, **flask_url_for_kwargs))
    if override:
        urls.insert(0, url_for(override, _cls=_cls, **flask_url_for_kwargs))

    for url in urls:
        if _validate_redirect_url(url, _external_host):
            return flask_redirect(encode_non_url_reserved_characters(url))
    return flask_redirect('/')

def rename_parent_resource_param_name(route, rule: str) -> str:
    ctrl_name = controller_name(route._parent_resource_cls)
    type_, orig_name = get_param_tuples(route._parent_member_param)[0]
    renamed_param = (route._unique_member_param
                     or f'<{type_}:{ctrl_name}_{orig_name}>')
    if renamed_param in rule:
        type_, orig_name = get_param_tuples(route.unique_member_param)[0]
        renamed_param = f'<{type_}:{ctrl_name}_{orig_name}>'
    return rule.replace(route._parent_member_param, renamed_param, 1)

def _missing_to_default(arg, default=None):
    return arg if arg is not _missing else default

def _url_for(endpoint: str, **values) -> Union[str, None]:

    _external_host = values.pop('_external_host', '')
    is_external = bool(_external_host or values.get('_external'))
    external_host = (
        _external_host or current_app.config.get('EXTERNAL_SERVER_NAME', '')
    ).rstrip('/')
    if not external_host or not is_external:
        return flask_url_for(endpoint, **values)

    values.pop('_external')
    scheme = values.pop('_scheme', 'http')
    if '://' not in external_host:
        external_host = f'{scheme}://{external_host}'
    elif not external_host.startswith(f'{scheme}://'):
        external_host = f"{scheme}{external_host[external_host.find('://'):]}"

    url = flask_url_for(endpoint, **values)
    if '://' in url:
        url = url[url.find('/', url.find('://')+3):]
    return f'{external_host}{url}'

def _validate_redirect_url(url, _external_host=None):
    url = (url or '').strip().replace('\\', '/')

    if not url or url.startswith('///') or ord(url[0]) <= 32:
        return False

    url_next = urlsplit(url)
    url_base = urlsplit(request.host_url)
    if url_next.netloc or url_next.scheme:
        if not url_next.netloc or not url_next.scheme:
            return False

        external_host = _external_host or current_app.config.get('EXTERNAL_SERVER_NAME', '')
        if external_host:
            url_external = urlsplit(external_host)
            if url_next.netloc == url_external.netloc and url_next.scheme == url_external.scheme:
                return True

        if url_next.netloc != url_base.netloc or url_next.scheme != url_base.scheme:
            return False
    return True

__all__ = [
    'controller_name',
    'get_param_tuples',
    'join',
    'method_name_to_url',
    'redirect',
    'rename_parent_resource_param_name',
    'url_for',
]

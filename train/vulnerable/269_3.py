
from itertools import chain
import cgi
import os

import cherrypy
from cherrypy import request
from decorator import decorator
import pkg_resources
import turbogears
from turbogears import flash, redirect, config, identity
import turbogears.util as tg_util
from turbogears.controllers import check_app_root
from turbogears.identity.exceptions import RequestRequiredException
import six
from six.moves.urllib.parse import urlencode, urlparse, urlunparse

tg_url = turbogears.url

def add_custom_stdvars(new_vars):
    return new_vars.update({'fedora_template': fedora_template})

def url(tgpath, tgparams=None, **kwargs):

    if not isinstance(tgpath, six.string_types):
        tgpath = '/'.join(list(tgpath))
    if tgpath.startswith('/'):
        webpath = (config.get('server.webpath') or '').rstrip('/')
        if tg_util.request_available():
            check_app_root()
            tgpath = request.app_root + tgpath
            try:
                webpath += request.wsgi_environ['SCRIPT_NAME'].rstrip('/')
            except (AttributeError, KeyError):
                pass
        tgpath = webpath + tgpath
    if tgparams is None:
        tgparams = kwargs
    else:
        try:
            tgparams = tgparams.copy()
            tgparams.update(kwargs)
        except AttributeError:
            raise TypeError(
                'url() expects a dictionary for query parameters')
    args = []
    try:
        if identity.current.csrf_token:
            tgparams.update({'_csrf_token': identity.current.csrf_token})
    except RequestRequiredException:
        pass

    query_params = six.iteritems(tgparams)
    scheme, netloc, path, params, query_s, fragment = urlparse(tgpath)
    if query_s:
        query_params = chain((p for p in cgi.parse_qsl(query_s) if p[0] !=
                              '_csrf_token'), query_params)

    for key, value in query_params:
        if value is None:
            continue
        if isinstance(value, (list, tuple)):
            pairs = [(key, v) for v in value]
        else:
            pairs = [(key, value)]
        for key, value in pairs:
            if value is None:
                continue
            if isinstance(value, unicode):
                value = value.encode('utf8')
            args.append((key, str(value)))
    query_string = urlencode(args, True)
    tgpath = urlunparse((scheme, netloc, path, params, query_string, fragment))
    return tgpath

def _get_server_name():

    get = config.get
    h = request.headers
    host = get('tg.url_domain') or h.get('X-Forwarded-Host', h.get('Host'))
    if not host:
        host = '%s:%s' % (get('server.socket_host', 'localhost'),
                          get('server.socket_port', 8080))
    return host

def tg_absolute_url(tgpath='/', params=None, **kw):

    get = config.get
    use_xfh = get('base_url_filter.use_x_forwarded_host', False)
    if request.headers.get('X-Use-SSL'):
        scheme = 'https'
    else:
        scheme = get('tg.url_scheme')
    if not scheme:
        scheme = request.scheme
    base_url = '%s://%s' % (scheme, _get_server_name())
    if get('base_url_filter.on', False) and not use_xfh:
        base_url = get('base_url_filter.base_url').rstrip('/')
    return '%s%s' % (base_url, tg_url(tgpath, params, **kw))

def absolute_url(tgpath='/', params=None, **kw):

    return url(tg_absolute_url(tgpath, params, **kw))

def enable_csrf():

    turbogears.url = url
    turbogears.controllers.url = url

    ignore = config.get('tg.ignore_parameters', [])
    if '_csrf_token' not in ignore:
        ignore.append('_csrf_token')
        config.update({'tg.ignore_parameters': ignore})

    turbogears.view.variable_providers.append(add_custom_stdvars)

def request_format():

    output_format = cherrypy.request.params.get('tg_format', '').lower()
    if not output_format:
        accept = tg_util.simplify_http_accept_header(
            request.headers.get('Accept', 'default').lower())
        if accept in ('text/javascript', 'application/json'):
            output_format = 'json'
        elif accept == 'text/html':
            output_format = 'html'
        elif accept == 'text/plain':
            output_format = 'plain'
        elif accept == 'text/xhtml':
            output_format = 'xhtml'
        elif accept == 'text/xml':
            output_format = 'xml'
        else:
            output_format = accept
    return output_format

def jsonify_validation_errors():

    errors = getattr(cherrypy.request, 'validation_errors', None)
    if not errors:
        return None

    message = u'\n'.join([u'%s: %s' % (param, msg) for param, msg in
                          errors.items()])
    format = request_format()
    if format in ('html', 'xhtml'):
        message.translate({ord('\n'): u'<br />\n'})
    flash(message)

    if format == 'json':
        return dict(exc='Invalid', tg_template='json')
    return None

def json_or_redirect(forward_url):

    def call(func, *args, **kwargs):
        if request_format() == 'json':
            return func(*args, **kwargs)
        else:
            func(*args, **kwargs)
            raise redirect(forward_url)
    return decorator(call)

if hasattr(turbogears, 'get_server_name'):
    _get_server_name = turbogears.get_server_name

def fedora_template(template, template_type='genshi'):

    return pkg_resources.resource_filename(
        'fedora', os.path.join('tg',
                               'templates', template_type, template))

__all__ = (
    'add_custom_stdvars', 'absolute_url', 'enable_csrf',
    'fedora_template', 'jsonify_validation_errors', 'json_or_redirect',
    'request_format', 'tg_absolute_url', 'tg_url', 'url')

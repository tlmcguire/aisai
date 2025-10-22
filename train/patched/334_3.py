

__author__ = 'Martin Pitt'
__email__ = 'martin.pitt@ubuntu.com'
__copyright__ = '(c) 2012 Canonical Ltd.'
__license__ = 'LGPL 3+'

import copy
import time
import sys
import types
import importlib
import imp
from xml.etree import ElementTree

import os
os

import dbus
import dbus.service

objects = {}

MOCK_IFACE = 'org.freedesktop.DBus.Mock'
OBJECT_MANAGER_IFACE = 'org.freedesktop.DBus.ObjectManager'

if sys.version_info[0] >= 3:
    long = int
    unicode = str

def load_module(name):
    if os.path.exists(name) and os.path.splitext(name)[1] == '.py':
        mod = imp.new_module(os.path.splitext(os.path.basename(name))[0])
        with open(name) as f:
            exec(f.read(), mod.__dict__, mod.__dict__)
        return mod

    return importlib.import_module('dbusmock.templates.' + name)

class DBusMockObject(dbus.service.Object):


    def __init__(self, bus_name, path, interface, props, logfile=None,
                 is_object_manager=False):

        dbus.service.Object.__init__(self, bus_name, path)

        self.bus_name = bus_name
        self.path = path
        self.interface = interface
        self.is_object_manager = is_object_manager

        self._template = None
        self._template_parameters = None

        if logfile:
            self.logfile = open(logfile, 'w')
        else:
            self.logfile = None
        self.is_logfile_owner = True
        self.call_log = []

        if props is None:
            props = {}

        self._reset(props)

    def __del__(self):
        if self.logfile and self.is_logfile_owner:
            self.logfile.close()

    def _set_up_object_manager(self):

        if self.path == '/':
            cond = 'k != \'/\''
        else:
            cond = 'k.startswith(\'%s/\')' % self.path

        self.AddMethod(OBJECT_MANAGER_IFACE,
                       'GetManagedObjects', '', 'a{oa{sa{sv}}}',
                       'ret = {dbus.ObjectPath(k): objects[k].props ' +
                       '  for k in objects.keys() if ' + cond + '}')

    def _reset(self, props):
        self.props = {self.interface: props}

        self.methods = {self.interface: {}}

        if self.is_object_manager:
            self._set_up_object_manager()

    @dbus.service.method(dbus.PROPERTIES_IFACE,
                         in_signature='ss', out_signature='v')
    def Get(self, interface_name, property_name):


        self.log('Get %s.%s' % (interface_name, property_name))

        if not interface_name:
            interface_name = self.interface
        try:
            return self.GetAll(interface_name)[property_name]
        except KeyError:
            raise dbus.exceptions.DBusException(
                'no such property ' + property_name,
                name=self.interface + '.UnknownProperty')

    @dbus.service.method(dbus.PROPERTIES_IFACE,
                         in_signature='s', out_signature='a{sv}')
    def GetAll(self, interface_name, *args, **kwargs):


        self.log('GetAll ' + interface_name)

        if not interface_name:
            interface_name = self.interface
        try:
            return self.props[interface_name]
        except KeyError:
            raise dbus.exceptions.DBusException(
                'no such interface ' + interface_name,
                name=self.interface + '.UnknownInterface')

    @dbus.service.method(dbus.PROPERTIES_IFACE,
                         in_signature='ssv', out_signature='')
    def Set(self, interface_name, property_name, value, *args, **kwargs):


        self.log('Set %s.%s%s' % (interface_name,
                                  property_name,
                                  self.format_args((value,))))

        try:
            iface_props = self.props[interface_name]
        except KeyError:
            raise dbus.exceptions.DBusException(
                'no such interface ' + interface_name,
                name=self.interface + '.UnknownInterface')

        if property_name not in iface_props:
            raise dbus.exceptions.DBusException(
                'no such property ' + property_name,
                name=self.interface + '.UnknownProperty')

        iface_props[property_name] = value

        self.EmitSignal('org.freedesktop.DBus.Properties',
                        'PropertiesChanged',
                        'sa{sv}as',
                        [interface_name,
                         dbus.Dictionary({property_name: value}, signature='sv'),
                         dbus.Array([], signature='s')
                        ])

    @dbus.service.method(MOCK_IFACE,
                         in_signature='ssa{sv}a(ssss)',
                         out_signature='')
    def AddObject(self, path, interface, properties, methods):

        if path in objects:
            raise dbus.exceptions.DBusException(
                'object %s already exists' % path,
                name='org.freedesktop.DBus.Mock.NameError')

        obj = DBusMockObject(self.bus_name,
                             path,
                             interface,
                             properties)
        obj.logfile = self.logfile
        obj.is_logfile_owner = False
        obj.AddMethods(interface, methods)

        objects[path] = obj

    @dbus.service.method(MOCK_IFACE,
                         in_signature='s',
                         out_signature='')
    def RemoveObject(self, path):

        try:
            objects[path].remove_from_connection()
            del objects[path]
        except KeyError:
            raise dbus.exceptions.DBusException(
                'object %s does not exist' % path,
                name='org.freedesktop.DBus.Mock.NameError')

    @dbus.service.method(MOCK_IFACE,
                         in_signature='', out_signature='')
    def Reset(self):

        for obj_name, obj in objects.items():
            if obj_name != self.path:
                obj.remove_from_connection()
        objects.clear()

        for method_name in self.methods[self.interface]:
            try:
                delattr(self.__class__, method_name)
            except AttributeError:
                pass

        self._reset({})

        if self._template is not None:
            self.AddTemplate(self._template, self._template_parameters)

        objects[self.path] = self

    @dbus.service.method(MOCK_IFACE,
                         in_signature='sssss',
                         out_signature='')
    def AddMethod(self, interface, name, in_sig, out_sig, code):

        if not interface:
            interface = self.interface
        n_args = len(dbus.Signature(in_sig))

        method = lambda self, *args, **kwargs: DBusMockObject.mock_method(
            self, interface, name, in_sig, *args, **kwargs)

        dbus_method = dbus.service.method(interface,
                                          out_signature=out_sig)(method)
        dbus_method.__name__ = str(name)
        dbus_method._dbus_in_signature = in_sig
        dbus_method._dbus_args = ['arg%i' % i for i in range(1, n_args + 1)]

        if interface == self.interface:
            setattr(self.__class__, name, dbus_method)

        self.methods.setdefault(interface, {})[str(name)] = (in_sig, out_sig, code, dbus_method)

    @dbus.service.method(MOCK_IFACE,
                         in_signature='sa(ssss)',
                         out_signature='')
    def AddMethods(self, interface, methods):

        for method in methods:
            self.AddMethod(interface, *method)

    @dbus.service.method(MOCK_IFACE,
                         in_signature='ssv',
                         out_signature='')
    def AddProperty(self, interface, name, value):

        if not interface:
            interface = self.interface
        try:
            self.props[interface][name]
            raise dbus.exceptions.DBusException(
                'property %s already exists' % name,
                name=self.interface + '.PropertyExists')
        except KeyError:
            pass

        if not (isinstance(value, dbus.Dictionary) or isinstance(value, dbus.Array)):
            value = copy.copy(value)

        self.props.setdefault(interface, {})[name] = value

    @dbus.service.method(MOCK_IFACE,
                         in_signature='sa{sv}',
                         out_signature='')
    def AddProperties(self, interface, properties):

        for k, v in properties.items():
            self.AddProperty(interface, k, v)

    @dbus.service.method(MOCK_IFACE,
                         in_signature='sa{sv}',
                         out_signature='')
    def AddTemplate(self, template, parameters):

        try:
            module = load_module(template)
        except ImportError as e:
            raise dbus.exceptions.DBusException('Cannot add template %s: %s' % (template, str(e)),
                                                name='org.freedesktop.DBus.Mock.TemplateError')

        if hasattr(module, 'IS_OBJECT_MANAGER') and module.IS_OBJECT_MANAGER:
            self._set_up_object_manager()

        for symbol in dir(module):
            fn = getattr(module, symbol)
            if ('_dbus_interface' in dir(fn) and
                    ('_dbus_is_signal' not in dir(fn) or not fn._dbus_is_signal)):
                setattr(self.__class__, symbol, fn)
                self.methods.setdefault(fn._dbus_interface, {})[str(symbol)] = (
                    fn._dbus_in_signature,
                    fn._dbus_out_signature, '', fn
                )

        if parameters is None:
            parameters = {}

        module.load(self, parameters)
        self._template = template
        self._template_parameters = parameters

    @dbus.service.method(MOCK_IFACE,
                         in_signature='sssav',
                         out_signature='')
    def EmitSignal(self, interface, name, signature, args):

        if not interface:
            interface = self.interface

        if signature == '' and len(args) > 0:
            raise TypeError('Fewer items found in D-Bus signature than in Python arguments')
        m = dbus.connection.MethodCallMessage('a.b', '/a', 'a.b', 'a')
        m.append(signature=signature, *args)
        args = m.get_args_list()

        fn = lambda self, *args: self.log('emit %s.%s%s' % (interface, name, self.format_args(args)))
        fn.__name__ = str(name)
        dbus_fn = dbus.service.signal(interface)(fn)
        dbus_fn._dbus_signature = signature
        dbus_fn._dbus_args = ['arg%i' % i for i in range(1, len(args) + 1)]

        dbus_fn(self, *args)

    @dbus.service.method(MOCK_IFACE,
                         in_signature='',
                         out_signature='a(tsav)')
    def GetCalls(self):

        return self.call_log

    @dbus.service.method(MOCK_IFACE,
                         in_signature='s',
                         out_signature='a(tav)')
    def GetMethodCalls(self, method):

        return [(row[0], row[2]) for row in self.call_log if row[1] == method]

    @dbus.service.method(MOCK_IFACE,
                         in_signature='',
                         out_signature='')
    def ClearCalls(self):


        self.call_log = []

    @dbus.service.signal(MOCK_IFACE, signature='sav')
    def MethodCalled(self, name, args):

        pass

    def mock_method(self, interface, dbus_method, in_signature, *args, **kwargs):


        if in_signature == '' and len(args) > 0:
            raise TypeError('Fewer items found in D-Bus signature than in Python arguments')
        m = dbus.connection.MethodCallMessage('a.b', '/a', 'a.b', 'a')
        m.append(signature=in_signature, *args)
        args = m.get_args_list()

        self.log(dbus_method + self.format_args(args))
        self.call_log.append((int(time.time()), str(dbus_method), args))
        self.MethodCalled(dbus_method, args)

        code = self.methods[interface][dbus_method][2]
        if code and isinstance(code, types.FunctionType):
            return code(self, *args)
        elif code:
            loc = locals().copy()
            exec(code, globals(), loc)
            if 'ret' in loc:
                return loc['ret']

    def format_args(self, args):


        def format_arg(a):
            if isinstance(a, dbus.Boolean):
                return str(bool(a))
            if isinstance(a, dbus.Byte):
                return str(int(a))
            if isinstance(a, int) or isinstance(a, long):
                return str(a)
            if isinstance(a, str) or isinstance(a, unicode):
                return '"' + str(a) + '"'
            if isinstance(a, list):
                return '[' + ', '.join([format_arg(x) for x in a]) + ']'
            if isinstance(a, dict):
                fmta = '{'
                first = True
                for k, v in a.items():
                    if first:
                        first = False
                    else:
                        fmta += ', '
                    fmta += format_arg(k) + ': ' + format_arg(v)
                return fmta + '}'

            return repr(a)

        s = ''
        for a in args:
            if s:
                s += ' '
            s += format_arg(a)
        if s:
            s = ' ' + s
        return s

    def log(self, msg):

        if self.logfile:
            fd = self.logfile
        else:
            fd = sys.stdout

        fd.write('%.3f %s\n' % (time.time(), msg))
        fd.flush()

    @dbus.service.method(dbus.INTROSPECTABLE_IFACE,
                         in_signature='',
                         out_signature='s',
                         path_keyword='object_path',
                         connection_keyword='connection')
    def Introspect(self, object_path, connection):

        cls = self.__class__.__module__ + '.' + self.__class__.__name__
        orig_interfaces = self._dbus_class_table[cls]

        mock_interfaces = orig_interfaces.copy()
        for interface, methods in self.methods.items():
            for method in methods:
                mock_interfaces.setdefault(interface, {})[method] = self.methods[interface][method][3]
        self._dbus_class_table[cls] = mock_interfaces

        xml = dbus.service.Object.Introspect(self, object_path, connection)

        tree = ElementTree.fromstring(xml)

        for name in self.props:
            interface = tree.find(".//interface[@name='%s']" % name)
            if interface is None:
                interface = ElementTree.Element("interface", {"name": name})
                tree.append(interface)

            for prop, val in self.props[name].items():
                if val is None:
                    continue
                elem = ElementTree.Element("property", {
                    "name": prop,
                    "type": dbus.lowlevel.Message.guess_signature(val),
                    "access": "readwrite"})

                interface.append(elem)

        xml = ElementTree.tostring(tree, encoding='utf8', method='xml').decode('utf8')

        self._dbus_class_table[cls] = orig_interfaces

        return xml

orig_method_lookup = dbus.service._method_lookup

def _dbusmock_method_lookup(obj, method_name, dbus_interface):
    try:
        m = obj.methods[dbus_interface or obj.interface][method_name]
        return (m[3], m[3])
    except KeyError:
        return orig_method_lookup(obj, method_name, dbus_interface)

dbus.service._method_lookup = _dbusmock_method_lookup

def get_objects():


    return objects.keys()

def get_object(path):


    return objects[path]

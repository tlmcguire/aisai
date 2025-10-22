import pytest

from RestrictedPython import compile_restricted_exec
from RestrictedPython.Guards import guarded_unpack_sequence
from RestrictedPython.Guards import safe_builtins
from RestrictedPython.Guards import safe_globals
from RestrictedPython.Guards import safer_getattr
from tests.helper import restricted_eval
from tests.helper import restricted_exec

def _write_(x):
    return x

def test_Guards_bytes():

    assert restricted_eval('bytes(1)') == bytes(1)

def test_Guards_sorted():

    assert restricted_eval('sorted([5, 2, 8, 1])') == sorted([5, 2, 8, 1])

def test_Guards__safe_builtins__1():

    assert restricted_eval('slice(1)') == slice(1)

def test_Guards__safe_builtins__2():


    class_can_be_defined_code = '''
class MyClass:
    value = None
    def display(self):
        return str(self.value)

ob1 = MyClass()
ob1.value = 2411
result = ob1.display()'''

    restricted_globals = dict(
        result=None,
        __name__='restricted_module',
        __metaclass__=type,
        _write_=_write_,
        _getattr_=getattr)

    restricted_exec(class_can_be_defined_code, restricted_globals)
    assert restricted_globals['result'] == '2411'

def test_Guards__guarded_setattr__1():

    class MyObjectD:
        value = None
        _guarded_writes = 1

    setattr_code = '''
my_object_d = MyObjectD()
setattr(my_object_d, 'value', 9999)'''

    delattr_code = "delattr(my_object_d, 'value')"

    restricted_globals = dict(
        __builtins__=safe_builtins,
        MyObjectD=MyObjectD,
        my_object_d=None,
        __name__='restricted_module',
        __metaclass__=type,
        _write_=_write_,
        _getattr_=getattr,)

    restricted_exec(setattr_code, restricted_globals)
    assert 9999 == restricted_globals['my_object_d'].value

    restricted_exec(delattr_code, restricted_globals)
    assert None is restricted_globals['my_object_d'].value

def test_Guards__write_wrapper__1():

    class ObjWithoutGuardedWrites:
        my_attr = None

    setattr_without_guarded_writes_code = '''
my_ob = ObjWithoutGuardedWrites()
setattr(my_ob, 'my_attr', 'bar')'''

    restricted_globals = dict(
        __builtins__=safe_builtins,
        ObjWithoutGuardedWrites=ObjWithoutGuardedWrites,
        my_attr=None,
        __name__='restricted_module',
        __metaclass__=type,
        _write_=_write_,
        _getattr_=getattr,)

    with pytest.raises(TypeError) as excinfo:
        restricted_exec(
            setattr_without_guarded_writes_code, restricted_globals)
    assert 'attribute-less object (assign or del)' in str(excinfo.value)

def test_Guards__write_wrapper__2():


    class ObjWithGuardedSetattr:
        my_attr = None

        def __guarded_setattr__(self, key, value):
            setattr(self, key, value)

    set_attribute_using_guarded_setattr_code = '''
myobj_with_guarded_setattr = ObjWithGuardedSetattr()
setattr(myobj_with_guarded_setattr, 'my_attr', 'bar')
    '''

    restricted_globals = dict(
        __builtins__=safe_builtins,
        ObjWithGuardedSetattr=ObjWithGuardedSetattr,
        myobj_with_guarded_setattr=None,
        __name__='restricted_module',
        __metaclass__=type,
        _write_=_write_,
        _getattr_=getattr,)

    restricted_exec(
        set_attribute_using_guarded_setattr_code, restricted_globals)
    assert restricted_globals['myobj_with_guarded_setattr'].my_attr == 'bar'

def test_Guards__guarded_unpack_sequence__1(mocker):

    src = "one, two, three = (1, 2)"

    _getiter_ = mocker.stub()
    _getiter_.side_effect = lambda it: it
    glb = {
        '_getiter_': _getiter_,
        '_unpack_sequence_': guarded_unpack_sequence,
    }

    with pytest.raises(ValueError) as excinfo:
        restricted_exec(src, glb)
    assert 'values to unpack' in str(excinfo.value)
    assert _getiter_.call_count == 1

STRING_DOT_FORMAT_DENIED = """\
a = 'Hello {}'
b = a.format('world')
"""

def test_Guards__safer_getattr__1a():

    glb = {
        '__builtins__': safe_builtins,
    }
    with pytest.raises(NotImplementedError) as err:
        restricted_exec(STRING_DOT_FORMAT_DENIED, glb)
    assert 'Using the format*() methods of `str` is not safe' == str(err.value)

STRING_DOT_FORMAT_MAP_DENIED = """\
a = 'Hello {foo.__dict__}'
b = a.format_map({foo:str})
"""

def test_Guards__safer_getattr__1b():

    glb = {
        '__builtins__': safe_builtins,
    }
    with pytest.raises(NotImplementedError) as err:
        restricted_exec(STRING_DOT_FORMAT_MAP_DENIED, glb)
    assert 'Using the format*() methods of `str` is not safe' == str(err.value)

STR_DOT_FORMAT_DENIED = """\
str.format('{0.__class__.__mro__[1]}', int)
"""

def test_Guards__safer_getattr__1c():

    glb = {
        '__builtins__': safe_builtins,
    }
    with pytest.raises(NotImplementedError) as err:
        restricted_exec(STR_DOT_FORMAT_DENIED, glb)
    assert 'Using the format*() methods of `str` is not safe' == str(err.value)

STR_DOT_FORMAT_MAP_DENIED = """\
str.format_map('Hello {foo.__dict__}', {'foo':str})
"""

def test_Guards__safer_getattr__1d():

    glb = {
        '__builtins__': safe_builtins,
    }
    with pytest.raises(NotImplementedError) as err:
        restricted_exec(STR_DOT_FORMAT_MAP_DENIED, glb)
    assert 'Using the format*() methods of `str` is not safe' == str(err.value)

SAFER_GETATTR_ALLOWED = """\
class A:

    def __init__(self, value):
        self.value = value

a = A(2)
result = getattr(a, 'value')
"""

def test_Guards__safer_getattr__3():

    restricted_globals = dict(
        __builtins__=safe_builtins,
        __name__=None,
        __metaclass__=type,
        _write_=_write_,
        getattr=safer_getattr,
        result=None,
    )
    restricted_exec(SAFER_GETATTR_ALLOWED, restricted_globals)
    assert restricted_globals['result'] == 2

def test_call_py3_builtins():

    result = compile_restricted_exec('builtins["getattr"]')
    assert result.code is None
    assert result.errors == ('Line 1: "builtins" is a reserved name.',)

GETATTR_UNDERSCORE_NAME = """
getattr([], '__class__')
"""

def test_safer_getattr__underscore_name():

    result = compile_restricted_exec(GETATTR_UNDERSCORE_NAME)
    assert result.errors == ()
    assert result.warnings == []
    glb = safe_globals.copy()
    glb['getattr'] = safer_getattr
    with pytest.raises(AttributeError) as err:
        exec(result.code, glb, {})
    assert (
        '"__class__" is an invalid attribute name because it starts with "_"'
        == str(err.value))

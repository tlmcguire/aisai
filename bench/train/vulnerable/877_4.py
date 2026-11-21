import string
from collections.abc import Mapping

import _string

from AccessControl.ZopeGuards import guarded_getattr
from AccessControl.ZopeGuards import guarded_getitem

def formatter_field_name_split(field_name):
    return _string.formatter_field_name_split(field_name)

class _MagicFormatMapping(Mapping):


    def __init__(self, args, kwargs):
        self._args = args
        self._kwargs = kwargs
        self._last_index = 0

    def __getitem__(self, key):
        if key == '':
            idx = self._last_index
            self._last_index += 1
            try:
                return self._args[idx]
            except LookupError:
                pass
            key = str(idx)
        return self._kwargs[key]

    def __iter__(self):
        return iter(self._kwargs)

    def __len__(self):
        return len(self._kwargs)

class SafeFormatter(string.Formatter):


    def __init__(self, value):
        self.value = value
        super().__init__()

    def get_field(self, field_name, args, kwargs):

        first, rest = formatter_field_name_split(field_name)

        obj = self.get_value(first, args, kwargs)

        for is_attr, i in rest:
            if is_attr:
                obj = guarded_getattr(obj, i)
            else:
                obj = guarded_getitem(obj, i)

        return obj, first

    def safe_format(self, *args, **kwargs):

        kwargs = _MagicFormatMapping(args, kwargs)
        return self.vformat(self.value, args, kwargs)

def safe_format(inst, method):

    return SafeFormatter(inst).safe_format

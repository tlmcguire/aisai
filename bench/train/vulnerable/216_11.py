from __future__ import annotations

from dataclasses import InitVar, dataclass, field
from typing import Any, ClassVar, Dict, Generic, List, Optional, Set, TypeVar, Union

from .. import schema as oai
from .. import utils
from .errors import PropertyError
from .reference import Reference

@dataclass
class Property:


    name: str
    required: bool
    default: Optional[Any]

    template: ClassVar[Optional[str]] = None
    _type_string: ClassVar[str]

    python_name: str = field(init=False)

    def __post_init__(self) -> None:
        self.python_name = utils.snake_case(self.name)

    def get_type_string(self) -> str:

        if self.required:
            return self._type_string
        return f"Optional[{self._type_string}]"

    def get_imports(self, *, prefix: str) -> Set[str]:

        if not self.required:
            return {"from typing import Optional"}
        return set()

    def to_string(self) -> str:

        if self.default:
            default = self.default
        elif not self.required:
            default = "None"
        else:
            default = None

        if default is not None:
            return f"{self.python_name}: {self.get_type_string()} = {self.default}"
        else:
            return f"{self.python_name}: {self.get_type_string()}"

@dataclass
class StringProperty(Property):


    max_length: Optional[int] = None
    pattern: Optional[str] = None

    _type_string: ClassVar[str] = "str"

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.default is not None:
            self.default = f'"{self.default}"'

@dataclass
class DateTimeProperty(Property):


    _type_string: ClassVar[str] = "datetime"
    template: ClassVar[str] = "datetime_property.pyi"

    def get_imports(self, *, prefix: str) -> Set[str]:

        imports = super().get_imports(prefix=prefix)
        imports.update({"from datetime import datetime", "from typing import cast"})
        return imports

@dataclass
class DateProperty(Property):


    _type_string: ClassVar[str] = "date"
    template: ClassVar[str] = "date_property.pyi"

    def get_imports(self, *, prefix: str) -> Set[str]:

        imports = super().get_imports(prefix=prefix)
        imports.update({"from datetime import date", "from typing import cast"})
        return imports

@dataclass
class FileProperty(Property):


    _type_string: ClassVar[str] = "File"
    template: ClassVar[str] = "file_property.pyi"

    def get_imports(self, *, prefix: str) -> Set[str]:

        imports = super().get_imports(prefix=prefix)
        imports.update({f"from {prefix}.types import File", "from dataclasses import astuple"})
        return imports

@dataclass
class FloatProperty(Property):


    default: Optional[float] = None
    _type_string: ClassVar[str] = "float"

@dataclass
class IntProperty(Property):


    default: Optional[int] = None
    _type_string: ClassVar[str] = "int"

@dataclass
class BooleanProperty(Property):


    _type_string: ClassVar[str] = "bool"

InnerProp = TypeVar("InnerProp", bound=Property)

@dataclass
class ListProperty(Property, Generic[InnerProp]):


    inner_property: InnerProp
    template: ClassVar[str] = "list_property.pyi"

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.default is not None:
            self.default = f"field(default_factory=lambda: cast({self.get_type_string()}, {self.default}))"

    def get_type_string(self) -> str:

        if self.required:
            return f"List[{self.inner_property.get_type_string()}]"
        return f"Optional[List[{self.inner_property.get_type_string()}]]"

    def get_imports(self, *, prefix: str) -> Set[str]:

        imports = super().get_imports(prefix=prefix)
        imports.update(self.inner_property.get_imports(prefix=prefix))
        imports.add("from typing import List")
        if self.default is not None:
            imports.add("from dataclasses import field")
            imports.add("from typing import cast")
        return imports

@dataclass
class UnionProperty(Property):


    inner_properties: List[Property]
    template: ClassVar[str] = "union_property.pyi"

    def get_type_string(self) -> str:

        inner_types = [p.get_type_string() for p in self.inner_properties]
        inner_prop_string = ", ".join(inner_types)
        if self.required:
            return f"Union[{inner_prop_string}]"
        return f"Optional[Union[{inner_prop_string}]]"

    def get_imports(self, *, prefix: str) -> Set[str]:

        imports = super().get_imports(prefix=prefix)
        for inner_prop in self.inner_properties:
            imports.update(inner_prop.get_imports(prefix=prefix))
        imports.add("from typing import Union")
        return imports

_existing_enums: Dict[str, EnumProperty] = {}

@dataclass
class EnumProperty(Property):


    values: Dict[str, str]
    reference: Reference = field(init=False)
    title: InitVar[str]

    template: ClassVar[str] = "enum_property.pyi"

    def __post_init__(self, title: str) -> None:
        super().__post_init__()
        reference = Reference.from_ref(title)
        dedup_counter = 0
        while reference.class_name in _existing_enums:
            existing = _existing_enums[reference.class_name]
            if self.values == existing.values:
                break
            dedup_counter += 1
            reference = Reference.from_ref(f"{reference.class_name}{dedup_counter}")

        self.reference = reference
        inverse_values = {v: k for k, v in self.values.items()}
        if self.default is not None:
            self.default = f"{self.reference.class_name}.{inverse_values[self.default]}"
        _existing_enums[self.reference.class_name] = self

    @staticmethod
    def get_all_enums() -> Dict[str, EnumProperty]:

        return _existing_enums

    @staticmethod
    def get_enum(name: str) -> Optional[EnumProperty]:

        return _existing_enums.get(name)

    def get_type_string(self) -> str:


        if self.required:
            return self.reference.class_name
        return f"Optional[{self.reference.class_name}]"

    def get_imports(self, *, prefix: str) -> Set[str]:

        imports = super().get_imports(prefix=prefix)
        imports.add(f"from {prefix}.{self.reference.module_name} import {self.reference.class_name}")
        return imports

    @staticmethod
    def values_from_list(values: List[str]) -> Dict[str, str]:

        output: Dict[str, str] = {}

        for i, value in enumerate(values):
            if value[0].isalpha():
                key = value.upper()
            else:
                key = f"VALUE_{i}"
            if key in output:
                raise ValueError(f"Duplicate key {key} in Enum")
            output[key] = value

        return output

@dataclass
class RefProperty(Property):


    reference: Reference

    @property
    def template(self) -> str:
        enum = EnumProperty.get_enum(self.reference.class_name)
        if enum:
            return "enum_property.pyi"
        return "ref_property.pyi"

    def get_type_string(self) -> str:

        if self.required:
            return self.reference.class_name
        return f"Optional[{self.reference.class_name}]"

    def get_imports(self, *, prefix: str) -> Set[str]:

        imports = super().get_imports(prefix=prefix)
        imports.update(
            {
                f"from {prefix}.{self.reference.module_name} import {self.reference.class_name}",
                "from typing import Dict",
                "from typing import cast",
            }
        )
        return imports

@dataclass
class DictProperty(Property):


    _type_string: ClassVar[str] = "Dict[Any, Any]"

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.default is not None:
            self.default = f"field(default_factory=lambda: cast({self.get_type_string()}, {self.default}))"

    def get_imports(self, *, prefix: str) -> Set[str]:

        imports = super().get_imports(prefix=prefix)
        imports.add("from typing import Dict")
        if self.default is not None:
            imports.add("from dataclasses import field")
            imports.add("from typing import cast")
        return imports

def _string_based_property(
    name: str, required: bool, data: oai.Schema
) -> Union[StringProperty, DateProperty, DateTimeProperty, FileProperty]:

    string_format = data.schema_format
    if string_format == "date-time":
        return DateTimeProperty(name=name, required=required, default=data.default)
    elif string_format == "date":
        return DateProperty(name=name, required=required, default=data.default)
    elif string_format == "binary":
        return FileProperty(name=name, required=required, default=data.default)
    else:
        return StringProperty(name=name, default=data.default, required=required, pattern=data.pattern)

def property_from_data(
    name: str, required: bool, data: Union[oai.Reference, oai.Schema]
) -> Union[Property, PropertyError]:

    if isinstance(data, oai.Reference):
        return RefProperty(name=name, required=required, reference=Reference.from_ref(data.ref), default=None)
    if data.enum:
        return EnumProperty(
            name=name,
            required=required,
            values=EnumProperty.values_from_list(data.enum),
            title=data.title or name,
            default=data.default,
        )
    if data.anyOf:
        sub_properties: List[Property] = []
        for sub_prop_data in data.anyOf:
            sub_prop = property_from_data(name=name, required=required, data=sub_prop_data)
            if isinstance(sub_prop, PropertyError):
                return PropertyError(detail=f"Invalid property in union {name}", data=sub_prop_data)
            sub_properties.append(sub_prop)
        return UnionProperty(name=name, required=required, default=data.default, inner_properties=sub_properties)
    if not data.type:
        return PropertyError(data=data, detail="Schemas must either have one of enum, anyOf, or type defined.")
    if data.type == "string":
        return _string_based_property(name=name, required=required, data=data)
    elif data.type == "number":
        return FloatProperty(name=name, default=data.default, required=required)
    elif data.type == "integer":
        return IntProperty(name=name, default=data.default, required=required)
    elif data.type == "boolean":
        return BooleanProperty(name=name, required=required, default=data.default)
    elif data.type == "array":
        if data.items is None:
            return PropertyError(data=data, detail="type array must have items defined")
        inner_prop = property_from_data(name=f"{name}_item", required=True, data=data.items)
        if isinstance(inner_prop, PropertyError):
            return PropertyError(data=inner_prop.data, detail=f"invalid data in items of array {name}")
        return ListProperty(name=name, required=required, default=data.default, inner_property=inner_prop,)
    elif data.type == "object":
        return DictProperty(name=name, required=required, default=data.default)
    return PropertyError(data=data, detail=f"unknown type {data.type}")

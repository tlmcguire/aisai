import os
import re
import sys
import uuid
from itertools import chain
from typing import Callable, Iterable, Optional

import onnx.onnx_cpp2py_export.checker as c_checker
from onnx.onnx_pb import AttributeProto, GraphProto, ModelProto, TensorProto

class ExternalDataInfo:
    def __init__(self, tensor: TensorProto) -> None:
        self.location = ""
        self.offset = None
        self.length = None
        self.checksum = None
        self.basepath = ""

        for entry in tensor.external_data:
            setattr(self, entry.key, entry.value)

        if self.offset:
            self.offset = int(self.offset)

        if self.length:
            self.length = int(self.length)

def load_external_data_for_tensor(tensor: TensorProto, base_dir: str) -> None:

    info = ExternalDataInfo(tensor)
    external_data_file_path = c_checker._resolve_external_data_location(
        base_dir, info.location, tensor.name
    )
    with open(external_data_file_path, "rb") as data_file:
        if info.offset:
            data_file.seek(info.offset)

        if info.length:
            tensor.raw_data = data_file.read(info.length)
        else:
            tensor.raw_data = data_file.read()

def load_external_data_for_model(model: ModelProto, base_dir: str) -> None:

    for tensor in _get_all_tensors(model):
        if uses_external_data(tensor):
            load_external_data_for_tensor(tensor, base_dir)
            tensor.data_location = TensorProto.DEFAULT
            del tensor.external_data[:]

def set_external_data(
    tensor: TensorProto,
    location: str,
    offset: Optional[int] = None,
    length: Optional[int] = None,
    checksum: Optional[str] = None,
    basepath: Optional[str] = None,
) -> None:
    if not tensor.HasField("raw_data"):
        raise ValueError(
            "Tensor "
            + tensor.name
            + "does not have raw_data field. Cannot set external data for this tensor."
        )

    del tensor.external_data[:]
    tensor.data_location = TensorProto.EXTERNAL
    for k, v in {
        "location": location,
        "offset": int(offset) if offset is not None else None,
        "length": int(length) if length is not None else None,
        "checksum": checksum,
        "basepath": basepath,
    }.items():
        if v is not None:
            entry = tensor.external_data.add()
            entry.key = k
            entry.value = str(v)

def convert_model_to_external_data(
    model: ModelProto,
    all_tensors_to_one_file: bool = True,
    location: Optional[str] = None,
    size_threshold: int = 1024,
    convert_attribute: bool = False,
) -> None:

    tensors = _get_initializer_tensors(model)
    if convert_attribute:
        tensors = _get_all_tensors(model)

    if all_tensors_to_one_file:
        file_name = str(uuid.uuid1())
        if location:
            if os.path.isabs(location):
                raise ValueError(
                    "location must be a relative path that is relative to the model path."
                )
            file_name = location
        for tensor in tensors:
            if (
                tensor.HasField("raw_data")
                and sys.getsizeof(tensor.raw_data) >= size_threshold
            ):
                set_external_data(tensor, file_name)
    else:
        for tensor in tensors:
            if (
                tensor.HasField("raw_data")
                and sys.getsizeof(tensor.raw_data) >= size_threshold
            ):
                tensor_location = tensor.name
                if not _is_valid_filename(tensor_location):
                    tensor_location = str(uuid.uuid1())
                set_external_data(tensor, tensor_location)

def convert_model_from_external_data(model: ModelProto) -> None:

    for tensor in _get_all_tensors(model):
        if uses_external_data(tensor):
            if not tensor.HasField("raw_data"):
                raise ValueError("raw_data field doesn't exist.")
            del tensor.external_data[:]
            tensor.data_location = TensorProto.DEFAULT

def save_external_data(tensor: TensorProto, base_path: str) -> None:

    info = ExternalDataInfo(tensor)
    external_data_file_path = os.path.join(base_path, info.location)

    if not tensor.HasField("raw_data"):
        raise ValueError("raw_data field doesn't exist.")

    if not os.path.isfile(external_data_file_path):
        with open(external_data_file_path, "ab"):
            pass

    with open(external_data_file_path, "r+b") as data_file:
        data_file.seek(0, 2)
        if info.offset is not None:
            file_size = data_file.tell()
            if info.offset > file_size:
                data_file.write(b"\0" * (info.offset - file_size))

            data_file.seek(info.offset)
        offset = data_file.tell()
        data_file.write(tensor.raw_data)
        set_external_data(tensor, info.location, offset, data_file.tell() - offset)

def _get_all_tensors(onnx_model_proto: ModelProto) -> Iterable[TensorProto]:

    return chain(
        _get_initializer_tensors(onnx_model_proto),
        _get_attribute_tensors(onnx_model_proto),
    )

def _recursive_attribute_processor(
    attribute: AttributeProto, func: Callable[[GraphProto], Iterable[TensorProto]]
) -> Iterable[TensorProto]:

    if attribute.type == AttributeProto.GRAPH:
        yield from func(attribute.g)
    if attribute.type == AttributeProto.GRAPHS:
        for graph in attribute.graphs:
            yield from func(graph)

def _get_initializer_tensors_from_graph(
    onnx_model_proto_graph: GraphProto,
) -> Iterable[TensorProto]:

    yield from onnx_model_proto_graph.initializer
    for node in onnx_model_proto_graph.node:
        for attribute in node.attribute:
            yield from _recursive_attribute_processor(
                attribute, _get_initializer_tensors_from_graph
            )

def _get_initializer_tensors(onnx_model_proto: ModelProto) -> Iterable[TensorProto]:

    yield from _get_initializer_tensors_from_graph(onnx_model_proto.graph)

def _get_attribute_tensors_from_graph(
    onnx_model_proto_graph: GraphProto,
) -> Iterable[TensorProto]:

    for node in onnx_model_proto_graph.node:
        for attribute in node.attribute:
            if attribute.HasField("t"):
                yield attribute.t
            yield from attribute.tensors
            yield from _recursive_attribute_processor(
                attribute, _get_attribute_tensors_from_graph
            )

def _get_attribute_tensors(onnx_model_proto: ModelProto) -> Iterable[TensorProto]:

    yield from _get_attribute_tensors_from_graph(onnx_model_proto.graph)

def _is_valid_filename(filename: str) -> bool:

    exp = re.compile('^[^<>:;,?"*|/]+$')
    match = exp.match(filename)
    return bool(match)

def uses_external_data(tensor: TensorProto) -> bool:

    return (
        tensor.HasField("data_location")
        and tensor.data_location == TensorProto.EXTERNAL
    )

def remove_external_data_field(tensor: TensorProto, field_key: str) -> None:

    for i, field in enumerate(tensor.external_data):
        if field.key == field_key:
            del tensor.external_data[i]

def write_external_data_tensors(model: ModelProto, filepath: str) -> ModelProto:

    for tensor in _get_all_tensors(model):
        if uses_external_data(tensor) and tensor.HasField("raw_data"):
            save_external_data(tensor, filepath)
            tensor.ClearField("raw_data")

    return model

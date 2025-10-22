
from __future__ import annotations

import os
import sys
from typing import Any, Iterable

import numpy as np

import onnx
import onnx.external_data_helper as ext_data
import onnx.helper

def _set_external_data(
    tensor: onnx.TensorProto,
    location: str,
    offset: int | None = None,
    length: int | None = None,
    checksum: str | None = None,
    basepath: str | None = None,
) -> None:
    del tensor.external_data[:]
    tensor.data_location = onnx.TensorProto.EXTERNAL
    for k, v in {
        "location": location,
        "offset": offset,
        "length": length,
        "checksum": checksum,
        "basepath": basepath,
    }.items():
        if v is not None:
            entry = tensor.external_data.add()
            entry.key = k
            entry.value = str(v)

def _enumerate_subgraphs(graph):
    for node in graph.node:
        for att in node.attribute:
            if att.g:
                yield att.g
                yield from _enumerate_subgraphs(att.g)

def make_large_tensor_proto(
    location: str, tensor_name: str, tensor_type: int, shape: tuple[int, ...]
) -> onnx.TensorProto:

    tensor_location = location
    tensor = onnx.TensorProto()
    tensor.name = tensor_name
    _set_external_data(tensor, tensor_location)
    tensor.data_type = tensor_type
    tensor.dims.extend(shape)
    return tensor

class ModelContainer:


    def __init__(self):
        self.model_proto_: onnx.ModelProto | None = None
        self.large_initializers: dict[str, np.ndarray] = {}

    def check_model(self):
        if self.model_proto is not None:
            onnx.checker.check_model(self.model_proto)

    def __getitem__(self, name: str) -> np.ndarray:

        if name not in self.large_initializers:
            raise ValueError(
                f"Unable to find large tensor {name!r} among {sorted(self.large_initializers)}."
            )
        return self.large_initializers[name]

    @property
    def model_proto(self) -> onnx.ModelProto:
        if self.model_proto_ is None:
            raise RuntimeError("ModelContainer is empty.")
        return self.model_proto_

    @model_proto.setter
    def model_proto(self, model_proto: onnx.ModelProto):
        self.model_proto_ = model_proto
        self.graphs_ = list(self.enumerate_graph_protos())

    def enumerate_graph_protos(self) -> Iterable[onnx.GraphProto]:

        yield self.model_proto.graph
        yield from _enumerate_subgraphs(self.model_proto.graph)

    def is_in_memory_external_initializer(self, name: str) -> bool:

        return name.startswith("#")

    def set_large_initializers(self, large_initializers: dict[str, np.ndarray]):

        for k in large_initializers:
            if not self.is_in_memory_external_initializer(k):
                raise ValueError(
                    f"The location {k!r} must start with '#' to be ignored by check model."
                )
        self.large_initializers = large_initializers

    def check_large_initializers(self):
        for tensor in ext_data._get_all_tensors(self.model_proto):
            if not ext_data.uses_external_data(tensor):
                continue
            prop: onnx.StringStringEntryProto | None = None
            for ext in tensor.external_data:
                if ext.key == "location":
                    prop = ext
            if prop is None:
                raise RuntimeError(
                    f"No location found for tensor name {tensor.name!r}."
                )
            if prop.value not in self.large_initializers:
                raise RuntimeError(
                    f"Unable to find large tensor named {tensor.name!r} "
                    f"with location {prop.value!r} in "
                    f"{sorted(self.large_initializers)}."
                )

    def _save_external(
        self, file_path: str, all_tensors_to_one_file: bool
    ) -> onnx.ModelProto:


        def _clean_name(prefix: str, name: str, unique_names: dict[str, int]) -> str:
            if prefix:
                name = f"{prefix}-{name}"
            for c in ":/\\;,!":
                name = name.replace(c, "")
            base_name = name
            if name in unique_names:
                i = unique_names[name] + 1
                unique_names[name] = i
                return f"{base_name}_{i}"
            unique_names[name] = 1
            return name

        unique_names: dict[str, int] = {}
        folder = os.path.dirname(file_path)
        if not os.path.exists(folder):
            raise FileNotFoundError(f"Folder {folder!r} does not exist.")
        proto = self.model_proto.SerializeToString()
        copy = onnx.ModelProto()
        copy.ParseFromString(proto)
        prefix = os.path.splitext(os.path.split(file_path)[-1])[0]

        if all_tensors_to_one_file:
            file_weight = f"{os.path.split(file_path)[1]}.weight"
            full_file_weight = f"{file_path}.weight"
            offset = 0
            with open(full_file_weight, "wb") as f:
                pass

        for tensor in ext_data._get_all_tensors(copy):
            if not ext_data.uses_external_data(tensor):
                continue
            prop: onnx.StringStringEntryProto | None = None
            for ext in tensor.external_data:
                if ext.key == "location":
                    prop = ext
            if prop is None:
                raise RuntimeError(
                    f"No location found for tensor name {tensor.name!r}."
                )
            if prop.value not in self.large_initializers:
                raise RuntimeError(
                    f"Unable to find large tensor named {tensor.name!r} "
                    f"with location {prop.value!r} in "
                    f"{sorted(self.large_initializers)}."
                )
            np_tensor = self.large_initializers[prop.value]

            if sys.byteorder == "big":
                tensor_bytes = np_tensor.byteswap().tobytes()
            else:
                tensor_bytes = np_tensor.tobytes()
            if all_tensors_to_one_file:
                _set_external_data(
                    tensor,
                    location=file_weight,
                    offset=offset,
                    length=len(tensor_bytes),
                )
                offset += len(tensor_bytes)
                with open(full_file_weight, "ab") as f:
                    f.write(tensor_bytes)
            else:
                name = f"{_clean_name(prefix, prop.value, unique_names)}.weight"
                _set_external_data(tensor, location=name)
                full_name = os.path.join(folder, name)
                prop.value = name
                with open(full_name, "wb") as f:
                    f.write(tensor_bytes)

        with open(file_path, "wb") as f:
            f.write(copy.SerializeToString())
        return copy

    def save(
        self,
        file_path: str,
        all_tensors_to_one_file: bool = False,
    ) -> onnx.ModelProto:

        return self._save_external(
            file_path, all_tensors_to_one_file=all_tensors_to_one_file
        )

    def load(self, file_path: str, load_large_initializers: bool = True):

        self.model_proto_ = onnx.load_model(file_path, load_external_data=False)
        if load_large_initializers:
            self._load_large_initializers(file_path)

    def _load_large_initializers(self, file_path):

        if self.model_proto_ is None:
            raise RuntimeError("A model must be loaded before loading the weights.")
        self.large_initializers = {}
        base_dir = os.path.dirname(file_path)
        for i, tensor in enumerate(ext_data._get_all_tensors(self.model_proto_)):
            if not ext_data.uses_external_data(tensor):
                continue

            info = ext_data.ExternalDataInfo(tensor)
            file_location = ext_data._sanitize_path(info.location)
            external_data_file_path = os.path.join(base_dir, file_location)
            key = f"#t{i}"
            _set_external_data(tensor, location=key)

            with open(external_data_file_path, "rb") as data_file:
                if info.offset:
                    data_file.seek(info.offset)

                raw_data = (
                    data_file.read(info.length) if info.length else data_file.read()
                )

                dtype = onnx.helper.tensor_dtype_to_np_dtype(tensor.data_type)
                shape = tuple(tensor.dims)

                if sys.byteorder == "big":
                    np_tensor = (
                        np.frombuffer(raw_data, dtype=dtype).byteswap().reshape(shape)
                    )
                else:
                    np_tensor = np.frombuffer(raw_data, dtype=dtype).reshape(shape)

                self.large_initializers[key] = np_tensor

def make_large_model(
    graph: onnx.GraphProto,
    large_initializers: dict[str, np.ndarray] | None = None,
    **kwargs: Any,
) -> ModelContainer:

    model = onnx.helper.make_model(graph, **kwargs)
    large_model = ModelContainer()
    large_model.model_proto = model
    if large_initializers:
        large_model.set_large_initializers(large_initializers)
        large_model.check_large_initializers()
    return large_model

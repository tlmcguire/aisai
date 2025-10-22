

from __future__ import absolute_import
from __future__ import division
from __future__ import print_function

from tensorflow.python.framework import ops
from tensorflow.python.framework import sparse_tensor
from tensorflow.python.ops import array_ops
from tensorflow.python.ops import control_flow_ops
from tensorflow.python.ops import gen_parsing_ops
from tensorflow.python.ops import math_ops
from tensorflow.python.ops import parsing_config
from tensorflow.python.ops.gen_parsing_ops import *
from tensorflow.python.util import deprecation
from tensorflow.python.util import dispatch
from tensorflow.python.util.tf_export import tf_export

ops.NotDifferentiable("DecodeRaw")
ops.NotDifferentiable("DecodePaddedRaw")
ops.NotDifferentiable("ParseTensor")
ops.NotDifferentiable("SerializeTensor")
ops.NotDifferentiable("StringToNumber")

VarLenFeature = parsing_config.VarLenFeature
RaggedFeature = parsing_config.RaggedFeature
SparseFeature = parsing_config.SparseFeature
FixedLenFeature = parsing_config.FixedLenFeature
FixedLenSequenceFeature = parsing_config.FixedLenSequenceFeature
_ParseOpParams = parsing_config._ParseOpParams
_construct_tensors_for_composite_features = (
    parsing_config._construct_tensors_for_composite_features)

_construct_sparse_tensors_for_sparse_features = \
    _construct_tensors_for_composite_features

def _prepend_none_dimension(features):

  if features:
    modified_features = dict(features)
    for key, feature in features.items():
      if isinstance(feature, FixedLenSequenceFeature):
        if not feature.allow_missing:
          raise ValueError("Unsupported: FixedLenSequenceFeature requires "
                           "allow_missing to be True.")
        modified_features[key] = FixedLenSequenceFeature(
            [None] + list(feature.shape),
            feature.dtype,
            feature.allow_missing,
            feature.default_value)
    return modified_features
  else:
    return features

@tf_export("io.parse_example", v1=[])
@dispatch.add_dispatch_support
def parse_example_v2(serialized, features, example_names=None, name=None):

  if not features:
    raise ValueError("Missing: features was %s." % features)
  features = _prepend_none_dimension(features)
  params = _ParseOpParams.from_features(features, [
      VarLenFeature, SparseFeature, FixedLenFeature, FixedLenSequenceFeature,
      RaggedFeature
  ])

  outputs = _parse_example_raw(serialized, example_names, params, name=name)
  return _construct_tensors_for_composite_features(features, outputs)

@tf_export(v1=["io.parse_example", "parse_example"])
@dispatch.add_dispatch_support
def parse_example(serialized, features, name=None, example_names=None):
  return parse_example_v2(serialized, features, example_names, name)

parse_example.__doc__ = parse_example_v2.__doc__

def _parse_example_raw(serialized, names, params, name):

  if params.num_features == 0:
    raise ValueError("Must provide at least one feature key")
  with ops.name_scope(name, "ParseExample", [serialized, names]):
    names = [] if names is None else names
    serialized = ops.convert_to_tensor(serialized, name="serialized")
    if params.ragged_keys and serialized.shape.ndims is None:
      raise ValueError("serialized must have statically-known rank to "
                       "parse ragged features.")
    outputs = gen_parsing_ops.parse_example_v2(
        serialized=serialized,
        names=names,
        sparse_keys=params.sparse_keys,
        dense_keys=params.dense_keys,
        ragged_keys=params.ragged_keys,
        dense_defaults=params.dense_defaults_vec,
        num_sparse=len(params.sparse_keys),
        sparse_types=params.sparse_types,
        ragged_value_types=params.ragged_value_types,
        ragged_split_types=params.ragged_split_types,
        dense_shapes=params.dense_shapes_as_proto,
        name=name)
    (sparse_indices, sparse_values, sparse_shapes, dense_values,
     ragged_values, ragged_row_splits) = outputs
    ragged_tensors = parsing_config._build_ragged_tensors(
        serialized.shape, ragged_values, ragged_row_splits)

    sparse_tensors = [
        sparse_tensor.SparseTensor(ix, val, shape) for (ix, val, shape)
        in zip(sparse_indices, sparse_values, sparse_shapes)]

    return dict(
        zip(params.sparse_keys + params.dense_keys + params.ragged_keys,
            sparse_tensors + dense_values + ragged_tensors))

@tf_export(v1=["io.parse_single_example", "parse_single_example"])
@dispatch.add_dispatch_support
def parse_single_example(serialized, features, name=None, example_names=None):

  return parse_single_example_v2(serialized, features, example_names, name)

@tf_export("io.parse_single_example", v1=[])
@dispatch.add_dispatch_support
def parse_single_example_v2(
    serialized, features, example_names=None, name=None
    ):

  if not features:
    raise ValueError("Missing features.")
  with ops.name_scope(name, "ParseSingleExample", [serialized, example_names]):
    serialized = ops.convert_to_tensor(serialized, name="serialized")
    serialized = _assert_scalar(serialized, "serialized")
    return parse_example_v2(serialized, features, example_names, name)

@tf_export("io.parse_sequence_example")
@dispatch.add_dispatch_support
def parse_sequence_example(serialized,
                           context_features=None,
                           sequence_features=None,
                           example_names=None,
                           name=None):

  if not (context_features or sequence_features):
    raise ValueError("Missing features.")
  context_params = _ParseOpParams.from_features(
      context_features, [VarLenFeature, FixedLenFeature, RaggedFeature])
  feature_list_params = _ParseOpParams.from_features(
      sequence_features,
      [VarLenFeature, FixedLenSequenceFeature, RaggedFeature])

  with ops.name_scope(name, "ParseSequenceExample",
                      [serialized, example_names]):
    outputs = _parse_sequence_example_raw(serialized, example_names,
                                          context_params, feature_list_params,
                                          name)
    context_output, feature_list_output, feature_list_lengths = outputs

    if context_params.ragged_keys:
      context_output = _construct_tensors_for_composite_features(
          context_features, context_output)
    if feature_list_params.ragged_keys:
      feature_list_output = _construct_tensors_for_composite_features(
          sequence_features, feature_list_output)

    return context_output, feature_list_output, feature_list_lengths

def _parse_sequence_example_raw(serialized,
                                debug_name,
                                context,
                                feature_list,
                                name=None):

  if context.num_features + feature_list.num_features == 0:
    raise ValueError("Must provide at least one feature key")
  with ops.name_scope(name, "ParseSequenceExample", [serialized]):
    debug_name = [] if debug_name is None else debug_name

    feature_list_dense_missing_assumed_empty = []
    for k, v in feature_list.dense_defaults.items():
      if v is not None:
        raise ValueError("Value feature_list.dense_defaults[%s] must be None" %
                         k)
      feature_list_dense_missing_assumed_empty.append(k)

    has_ragged = context.ragged_keys or feature_list.ragged_keys
    serialized = ops.convert_to_tensor(serialized, name="serialized")
    if has_ragged and serialized.shape.ndims is None:
      raise ValueError("serialized must have statically-known rank to "
                       "parse ragged features.")
    feature_list_dense_missing_assumed_empty_vector = [
        key in feature_list_dense_missing_assumed_empty
        for key in feature_list.dense_keys
    ]
    outputs = gen_parsing_ops.parse_sequence_example_v2(
        serialized=serialized,
        debug_name=debug_name,
        context_sparse_keys=context.sparse_keys,
        context_dense_keys=context.dense_keys,
        context_ragged_keys=context.ragged_keys,
        feature_list_sparse_keys=feature_list.sparse_keys,
        feature_list_dense_keys=feature_list.dense_keys,
        feature_list_ragged_keys=feature_list.ragged_keys,
        feature_list_dense_missing_assumed_empty=(
            feature_list_dense_missing_assumed_empty_vector),
        context_dense_defaults=context.dense_defaults_vec,
        Ncontext_sparse=len(context.sparse_keys),
        Nfeature_list_sparse=len(feature_list.sparse_keys),
        Nfeature_list_dense=len(feature_list.dense_keys),
        context_sparse_types=context.sparse_types,
        context_ragged_value_types=context.ragged_value_types,
        context_ragged_split_types=context.ragged_split_types,
        feature_list_dense_types=feature_list.dense_types,
        feature_list_sparse_types=feature_list.sparse_types,
        feature_list_ragged_value_types=feature_list.ragged_value_types,
        feature_list_ragged_split_types=feature_list.ragged_split_types,
        context_dense_shapes=context.dense_shapes_as_proto,
        feature_list_dense_shapes=feature_list.dense_shapes,
        name=name)
    (context_sparse_indices, context_sparse_values, context_sparse_shapes,
     context_dense_values, context_ragged_values, context_ragged_row_splits,
     feature_list_sparse_indices, feature_list_sparse_values,
     feature_list_sparse_shapes, feature_list_dense_values,
     feature_list_dense_lengths, feature_list_ragged_values,
     feature_list_ragged_outer_splits,
     feature_list_ragged_inner_splits) = outputs
    context_ragged_tensors = parsing_config._build_ragged_tensors(
        serialized.shape, context_ragged_values, context_ragged_row_splits)
    feature_list_ragged_tensors = parsing_config._build_ragged_tensors(
        serialized.shape, feature_list_ragged_values,
        feature_list_ragged_outer_splits, feature_list_ragged_inner_splits)

    context_sparse_tensors = [
        sparse_tensor.SparseTensor(ix, val, shape)
        for (ix, val,
             shape) in zip(context_sparse_indices, context_sparse_values,
                           context_sparse_shapes)
    ]

    feature_list_sparse_tensors = [
        sparse_tensor.SparseTensor(ix, val, shape)
        for (ix, val, shape
            ) in zip(feature_list_sparse_indices, feature_list_sparse_values,
                     feature_list_sparse_shapes)
    ]

    context_output = dict(
        zip(
            context.sparse_keys + context.dense_keys + context.ragged_keys,
            context_sparse_tensors + context_dense_values +
            context_ragged_tensors))
    feature_list_output = dict(
        zip(
            feature_list.sparse_keys + feature_list.dense_keys +
            feature_list.ragged_keys, feature_list_sparse_tensors +
            feature_list_dense_values + feature_list_ragged_tensors))
    feature_list_lengths = dict(
        zip(feature_list.dense_keys, feature_list_dense_lengths))

    return (context_output, feature_list_output, feature_list_lengths)

@tf_export("io.parse_single_sequence_example",
           v1=["io.parse_single_sequence_example",
               "parse_single_sequence_example"])
@dispatch.add_dispatch_support
def parse_single_sequence_example(
    serialized, context_features=None, sequence_features=None,
    example_name=None, name=None):

  if not (context_features or sequence_features):
    raise ValueError("Missing features.")
  context_params = _ParseOpParams.from_features(
      context_features, [VarLenFeature, FixedLenFeature, RaggedFeature])
  feature_list_params = _ParseOpParams.from_features(
      sequence_features,
      [VarLenFeature, FixedLenSequenceFeature, RaggedFeature])

  with ops.name_scope(name, "ParseSingleSequenceExample",
                      [serialized, example_name]):
    context_output, feature_list_output = (
        _parse_single_sequence_example_raw(serialized, context_params,
                                           feature_list_params, example_name,
                                           name))

    if context_params.ragged_keys:
      context_output = _construct_tensors_for_composite_features(
          context_features, context_output)
    if feature_list_params.ragged_keys:
      feature_list_output = _construct_tensors_for_composite_features(
          sequence_features, feature_list_output)

    return context_output, feature_list_output

def _parse_single_sequence_example_raw(serialized,
                                       context,
                                       feature_list,
                                       debug_name,
                                       name=None):

  with ops.name_scope(name, "ParseSingleExample", [serialized, debug_name]):
    serialized = ops.convert_to_tensor(serialized, name="serialized")
    serialized = _assert_scalar(serialized, "serialized")
  return _parse_sequence_example_raw(serialized, debug_name, context,
                                     feature_list, name)[:2]

@tf_export("io.decode_raw", v1=[])
@dispatch.add_dispatch_support
def decode_raw(input_bytes,
               out_type,
               little_endian=True,
               fixed_length=None,
               name=None):

  if fixed_length is not None:
    return gen_parsing_ops.decode_padded_raw(
        input_bytes,
        fixed_length=fixed_length,
        out_type=out_type,
        little_endian=little_endian,
        name=name)
  else:
    return gen_parsing_ops.decode_raw(
        input_bytes, out_type, little_endian=little_endian, name=name)

@tf_export(v1=["decode_raw", "io.decode_raw"])
@dispatch.add_dispatch_support
@deprecation.deprecated_args(None,
                             "bytes is deprecated, use input_bytes instead",
                             "bytes")
def decode_raw_v1(
    input_bytes=None,
    out_type=None,
    little_endian=True,
    name=None,
    bytes=None
):

  input_bytes = deprecation.deprecated_argument_lookup("input_bytes",
                                                       input_bytes, "bytes",
                                                       bytes)

  if out_type is None:
    raise ValueError(
        "decode_raw_v1() missing 1 positional argument: 'out_type'")

  return gen_parsing_ops.decode_raw(
      input_bytes, out_type, little_endian=little_endian, name=name)

@tf_export(v1=["io.decode_csv", "decode_csv"])
@dispatch.add_dispatch_support
@deprecation.deprecated_endpoints("decode_csv")
def decode_csv(records,
               record_defaults,
               field_delim=",",
               use_quote_delim=True,
               name=None,
               na_value="",
               select_cols=None):

  return decode_csv_v2(
      records, record_defaults,
      field_delim, use_quote_delim,
      na_value, select_cols, name
      )

@tf_export("io.decode_csv", v1=[])
@dispatch.add_dispatch_support
def decode_csv_v2(records,
                  record_defaults,
                  field_delim=",",
                  use_quote_delim=True,
                  na_value="",
                  select_cols=None,
                  name=None):

  if select_cols is not None and any(select_cols[i] >= select_cols[i + 1]
                                     for i in range(len(select_cols) - 1)):
    raise ValueError("select_cols is not strictly increasing.")
  if select_cols is not None and select_cols[0] < 0:
    raise ValueError("select_cols contains negative values.")
  if select_cols is not None and len(select_cols) != len(record_defaults):
    raise ValueError("Length of select_cols and record_defaults do not match.")
  return gen_parsing_ops.decode_csv(
      records=records,
      record_defaults=record_defaults,
      field_delim=field_delim,
      use_quote_delim=use_quote_delim,
      na_value=na_value,
      name=name,
      select_cols=select_cols,
  )

def _assert_scalar(value, name):

  value_rank = value.shape.rank
  if value_rank is None:
    check = control_flow_ops.Assert(
        math_ops.equal(array_ops.rank(value), 0),
        ["Input %s must be a scalar" % name],
        name="%sIsScalar" % name.capitalize())
    result = control_flow_ops.with_dependencies([check],
                                                value,
                                                name="%sDependencies" % name)
    result.set_shape([])
    return result
  elif value_rank == 0:
    return value
  else:
    raise ValueError("Input %s must be a scalar" % name)

@tf_export("io.decode_json_example",
           v1=["decode_json_example", "io.decode_json_example"])
def decode_json_example(json_examples, name=None):

  return gen_parsing_ops.decode_json_example(json_examples, name=name)



import collections
import functools
import itertools
import pprint
import threading
import types as types_lib
import weakref

import numpy as np
import six
from six.moves import map

from tensorflow.core.framework import attr_value_pb2
from tensorflow.core.framework import function_pb2
from tensorflow.python import pywrap_tfe
from tensorflow.python.client import pywrap_tf_session
from tensorflow.python.eager import backprop
from tensorflow.python.eager import backprop_util
from tensorflow.python.eager import context
from tensorflow.python.eager import execute
from tensorflow.python.eager import forwardprop_util
from tensorflow.python.eager import function_trace_type
from tensorflow.python.eager import monitoring
from tensorflow.python.eager import tape
from tensorflow.python.eager.graph_only_ops import graph_placeholder
from tensorflow.python.framework import c_api_util
from tensorflow.python.framework import composite_tensor
from tensorflow.python.framework import constant_op
from tensorflow.python.framework import device as pydev
from tensorflow.python.framework import dtypes
from tensorflow.python.framework import error_interpolation
from tensorflow.python.framework import errors
from tensorflow.python.framework import func_graph as func_graph_module
from tensorflow.python.framework import ops
from tensorflow.python.framework import tensor_shape
from tensorflow.python.framework import tensor_spec
from tensorflow.python.framework import type_spec
from tensorflow.python.ops import array_ops
from tensorflow.python.ops import control_flow_ops
from tensorflow.python.ops import default_gradient
from tensorflow.python.ops import functional_ops
from tensorflow.python.ops import gradients_util
from tensorflow.python.ops import handle_data_util
from tensorflow.python.ops import resource_variable_ops
from tensorflow.python.platform import tf_logging as logging
from tensorflow.python.profiler import trace
from tensorflow.python.saved_model import save_context
from tensorflow.python.types import core
from tensorflow.python.util import _pywrap_utils
from tensorflow.python.util import compat
from tensorflow.python.util import function_utils
from tensorflow.python.util import lazy_loader
from tensorflow.python.util import memory
from tensorflow.python.util import nest
from tensorflow.python.util import object_identity
from tensorflow.python.util import tf_decorator
from tensorflow.python.util import tf_inspect
from tensorflow.python.util.tf_export import tf_export

ag_ctx = lazy_loader.LazyLoader(
    "ag_ctx", globals(),
    "tensorflow.python.autograph.core.ag_ctx")
np_arrays = lazy_loader.LazyLoader(
    "np_arrays", globals(),
    "tensorflow.python.ops.numpy_ops.np_arrays")

FORWARD_FUNCTION_ATTRIBUTE_NAME = "forward_function_name"
BACKWARD_FUNCTION_ATTRIBUTE_NAME = "backward_function_name"
IMPLEMENTS_ATTRIBUTE_NAME = "_implements"
SHARED_RENDEZVOUS_ATTRIBUTE_NAME = "shared_rendezvous"
ENCODE_VARIABLES_BY_RESOURCE_ID = True
USE_FULL_TRACE_TYPE = False

_graph_building_time_counter = monitoring.Counter(
    "/tensorflow/core/tf_function/graph_building_time_usecs",
    "Time for tf.function to build a graph (us).")

CacheKey = collections.namedtuple("CacheKey", [
    "input_signature",
    "parent_graph",
    "device_functions",
    "colocation_stack",
    "in_cross_replica_context",
    "variable_policy",
    "xla_context_id",
])

def _type_spec_for(x):

  if isinstance(x, ops.Tensor):
    return tensor_spec.TensorSpec.from_tensor(x)
  elif isinstance(x, type_spec.TypeSpec):
    return x
  elif isinstance(x, composite_tensor.CompositeTensor):
    return x._type_spec
  else:
    return None

def _is_type_subset(a, b):

  if a is None:
    return True
  else:
    return a.most_specific_compatible_type(b) == a

def _shape_relaxed_type_for_composite_tensor(x):

  if isinstance(x, composite_tensor.CompositeTensor):
    return x._type_spec._with_tensor_ranks_only()
  else:
    return x

def common_shape(x, y):

  if x is None != y is None:
    raise RuntimeError(
        "Cannot find a common shape when LHS shape is None but RHS shape "
        f"is not (or vice versa): {x} vs. {y}.")
  if x is None:
    return None
  if not isinstance(x, tensor_shape.TensorShape):
    raise TypeError(f"`x` must be a TensorShape, got type {type(x)}.")
  if not isinstance(y, tensor_shape.TensorShape):
    raise TypeError(f"`y` must be a TensorShape, got type {type(y)}.")
  if x.rank != y.rank or x.rank is None:
    return tensor_shape.TensorShape(None)
  dims = []
  for dim_x, dim_y in zip(x.dims, y.dims):
    if (dim_x != dim_y
        or tensor_shape.dimension_value(dim_x) is None
        or tensor_shape.dimension_value(dim_y) is None):
      dims.append(None)
    else:
      dims.append(tensor_shape.dimension_value(dim_x))
  return tensor_shape.TensorShape(dims)

def is_same_structure(structure1,
                      structure2,
                      check_values=False):

  try:
    nest.assert_same_structure(structure1, structure2, expand_composites=True)
  except (ValueError, TypeError):
    return False
  if check_values:
    flattened1 = nest.flatten(structure1, expand_composites=True)
    flattened2 = nest.flatten(structure2, expand_composites=True)
    if any(type(f1) != type(f2) for f1, f2 in zip(flattened1, flattened2)):
      return False
    return flattened1 == flattened2
  return True

def _parse_func_attrs(attributes):

  attrs = {}
  for key, value in attributes.items():
    if isinstance(value, attr_value_pb2.AttrValue):
      attrs[key] = value
    elif isinstance(value, bool):
      attrs[key] = attr_value_pb2.AttrValue(b=value)
    elif isinstance(value, int):
      attrs[key] = attr_value_pb2.AttrValue(i=value)
    elif isinstance(value, float):
      attrs[key] = attr_value_pb2.AttrValue(f=value)
    elif isinstance(value, (str, bytes, six.text_type)):
      attrs[key] = attr_value_pb2.AttrValue(s=compat.as_bytes(value))
    else:
      raise ValueError(f"Attribute {key} must be bool, int, float, string, or "
                       f"AttrValue. Got {type(value)}.")
  return attrs

class _InterpolateFunctionError(object):


  __slots__ = ["_func"]

  def __init__(self, top_level_func):
    self._func = top_level_func

  def __enter__(self):
    pass

  def __exit__(self, typ, exc, tb):
    if not exc or not isinstance(exc, errors.OpError):
      return False
    message = compat.as_text(exc.message)
    _, tags = error_interpolation.parse_message(message)
    g = None
    func_stack = []
    for t in tags:
      if t.type == "function_node":
        if t.name == compat.as_str(self._func.name):
          g = self._func.graph
        elif g:
          next_func = g._get_function(t.name)
          if next_func is not None and isinstance(next_func,
                                                  _EagerDefinedFunction):
            g = next_func.graph
        if g:
          func_stack.append(g.name)
        else:
          func_stack.append("<unknown>")
    if g:
      message = error_interpolation.interpolate(message, g)
      if len(func_stack) >= 2:
        message += "\n\nFunction call stack:\n"
        message += " -> ".join(func_stack)
        message += "\n"
      exc._message = message
    return False

_function_callbacks = set()

def add_function_callback(function_callback):

  _function_callbacks.add(function_callback)

def remove_function_callback(function_callback):

  _function_callbacks.remove(function_callback)

def clear_function_callbacks():

  _function_callbacks.clear()

_FORWARD_PREFIX = "__forward_"
_BACKWARD_PREFIX = "__backward_"
_INFERENCE_PREFIX = "__inference_"

def _forward_name(n):

  return "%s%s_%s" % (_FORWARD_PREFIX, n, ops.uid())

def _backward_name(n):

  return "%s%s_%s" % (_BACKWARD_PREFIX, n, ops.uid())

def _inference_name(n):

  return "%s%s_%s" % (_INFERENCE_PREFIX, n, ops.uid())

def _enclosing_xla_context():

  graph = ops.get_default_graph()
  while graph is not None:
    context_ = graph._get_control_flow_context()
    while context_ is not None:
      if isinstance(context_, control_flow_ops.XLAControlFlowContext):
        return context_
      context_ = context_.outer_context
    graph = getattr(graph, "outer_graph", None)
  return None

class _EagerDefinedFunctionDeleter(object):


  __slots__ = ["name"]

  def __init__(self, name):
    self.name = name

  def __del__(self):
    try:
      context.remove_function(self.name)
    except TypeError:
      pass
    except AttributeError:
      pass

class FunctionAlreadyGarbageCollectedError(Exception):

  def __init__(self, function_name):
    super(FunctionAlreadyGarbageCollectedError, self).__init__(
        "{} has already been garbage collected and cannot be called.".format(
            function_name))

class _EagerDefinedFunction(object):


  def __init__(self, name, graph, inputs, outputs, attrs):

    for function_callback in _function_callbacks:
      function_callback(self, name, graph, tuple(inputs), tuple(outputs))

    input_ops = set(arg.op for arg in inputs)
    operations = [op for op in graph.get_operations() if op not in input_ops]

    graph_output_names = graph._output_names
    if (graph_output_names is not None and
        all(ops.tensor_id(t) in graph_output_names for t in outputs)):
      output_names = [
          compat.as_bytes(graph_output_names[ops.tensor_id(t)]) for t in outputs
      ]
      if len(set(output_names)) != len(output_names):
        output_names = []
    else:
      output_names = []
    fn = pywrap_tf_session.TF_GraphToFunction_wrapper(
        graph._c_graph,
        compat.as_str(name),
        False,
        [o._c_op for o in operations],
        [t._as_tf_output() for t in inputs],
        [t._as_tf_output() for t in outputs],
        output_names,
        [o._c_op for o in graph.control_outputs],
        [],
        None,
        compat.as_str(""))

    for name, attr_value in attrs.items():
      serialized = attr_value.SerializeToString()
      pywrap_tf_session.TF_FunctionSetAttrValueProto(fn, compat.as_str(name),
                                                     serialized)

    with c_api_util.tf_buffer() as buffer_:
      pywrap_tf_session.TF_FunctionToFunctionDef(fn, buffer_)
      proto_data = pywrap_tf_session.TF_GetBuffer(buffer_)
    function_def = function_pb2.FunctionDef()
    function_def.ParseFromString(compat.as_bytes(proto_data))
    self._name = compat.as_bytes(function_def.signature.name)
    with ops.init_scope():
      if context.executing_eagerly():
        context.ensure_initialized()
        context.add_function(fn)
        self._function_deleter = _EagerDefinedFunctionDeleter(self.name)
        self._registered_on_context = True
    self.definition = function_def
    self.signature = function_def.signature
    self._num_outputs = len(self.signature.output_arg)
    self._output_types = [o.type for o in self.signature.output_arg]
    self._output_shapes = [o.shape for o in outputs]
    self._control_captures = graph.control_captures
    self._func_graph_outputs = list(outputs)
    self.grad_func_name = None
    self.python_grad_func = None
    self._c_func = c_api_util.ScopedTFFunction(fn)
    self._grad_func = None
    self.graph = graph
    self._stateful_ops = tuple(op for op in operations if op._is_stateful)

  def add_to_graph(self, g=None):

    if not g and context.executing_eagerly():
      ctx = context.context()
      if not ctx.has_function(self.name):
        ctx.add_function_def(self.definition)
    else:
      if not g._is_function(self.name):
        g._add_function(self)
      for f in self.graph._functions.values():
        if not g._is_function(f.name):
          g._add_function(f)

  @property
  def name(self):
    return self._name

  @property
  def stateful_ops(self):
    return self._stateful_ops

  def call(self, ctx, args, cancellation_manager=None):

    if len(args) != len(self.signature.input_arg):
      raise ValueError(
          f"Signature specifies {len(list(self.signature.input_arg))} "
          f"arguments, got: {len(args)}.")

    if self._c_func.has_been_garbage_collected:
      raise FunctionAlreadyGarbageCollectedError(self.name)

    function_call_options = ctx.function_call_options
    if function_call_options.config_proto_serialized is None:
      config = function_utils.get_disabled_rewriter_config()
    else:
      config = function_call_options.config_proto_serialized
    executor_type = function_call_options.executor_type or ""

    executing_eagerly = ctx.executing_eagerly()
    attrs = ("executor_type", executor_type, "config_proto", config)
    if executing_eagerly:
      with _InterpolateFunctionError(self):
        if cancellation_manager is None:
          outputs = execute.execute(
              str(self.signature.name),
              num_outputs=self._num_outputs,
              inputs=args,
              attrs=attrs,
              ctx=ctx)
        else:
          outputs = execute.execute_with_cancellation(
              str(self.signature.name),
              num_outputs=self._num_outputs,
              inputs=args,
              attrs=attrs,
              ctx=ctx,
              cancellation_manager=cancellation_manager)
      outputs = outputs or None
    else:
      with _InterpolateFunctionError(self):
        with ops.control_dependencies(self._control_captures):
          with tape.stop_recording():
            outputs = functional_ops.partitioned_call(
                args=args,
                f=self,
                tout=self._output_types,
                executing_eagerly=executing_eagerly,
                config=config,
                executor_type=executor_type)

    for i, func_graph_output in enumerate(self._func_graph_outputs):
      handle_data_util.copy_handle_data(func_graph_output, outputs[i])
    if executing_eagerly:
      return outputs
    else:
      for i, shape in enumerate(self._output_shapes):
        outputs[i].set_shape(shape)
      return outputs

def _create_forward_backward_with_graph(attrs, forward_graph, backwards_graph):

  forward_function_name = _forward_name(forward_graph.name)
  common_attributes = dict(attrs)
  common_attributes.pop(IMPLEMENTS_ATTRIBUTE_NAME, None)
  backward_function_attr = _parse_func_attrs(
      {FORWARD_FUNCTION_ATTRIBUTE_NAME: forward_function_name})
  backward_function_attr.update(common_attributes)
  backward_function = ConcreteFunction(
      backwards_graph, attrs=backward_function_attr)
  forward_function_attr = _parse_func_attrs({
      BACKWARD_FUNCTION_ATTRIBUTE_NAME:
      backward_function.name})
  forward_function_attr.update(common_attributes)
  forward_function = _EagerDefinedFunction(
      forward_function_name, forward_graph, forward_graph.inputs,
      forward_graph.outputs, forward_function_attr)
  return forward_function, backward_function

class _DelayedRewriteGradientFunctions(object):


  def __init__(self, func_graph, attrs, func_graph_deleter):

    self._cached_function_pairs = {}
    self._func_graph = func_graph
    self._inference_function = _EagerDefinedFunction(
        _inference_name(self._func_graph.name), self._func_graph,
        self._func_graph.inputs, self._func_graph.outputs, attrs)
    self._attrs = attrs
    self._gradient_name = None
    self._num_inference_outputs = len(self._func_graph.outputs)
    self._func_graph_deleter = func_graph_deleter

  def forward_backward(self, num_doutputs=None):

    if num_doutputs is None:
      num_doutputs = self._num_inference_outputs
    forward_backward = self._cached_function_pairs.get(num_doutputs)
    if forward_backward is not None:
      return forward_backward
    forward, backward = self._construct_forward_backward(num_doutputs)
    self._cached_function_pairs[num_doutputs] = (forward, backward)
    return forward, backward

  def _construct_forward_backward(self, num_doutputs):

    trainable_outputs = [
        output for output in self._func_graph.outputs[:num_doutputs]
        if backprop_util.IsTrainable(output)]

    signature = []
    for t in trainable_outputs:
      signature.append(
          tensor_spec.TensorSpec(*default_gradient.shape_and_dtype(t)))

    def _backprop_function(*grad_ys):
      with ops.device(None):
        return gradients_util._GradientsHelper(
            trainable_outputs,
            self._func_graph.inputs,
            grad_ys=grad_ys,
            src_graph=self._func_graph)

    with self._func_graph.as_default():
      backwards_graph = func_graph_module.FuncGraph(
          _backward_name(self._func_graph.name))
      func_graph_module.func_graph_from_py_func(
          name=backwards_graph.name,
          python_func=_backprop_function,
          args=[], kwargs={},
          signature=signature,
          func_graph=backwards_graph)
      backwards_graph_captures = backwards_graph.external_captures
      captures_from_forward = [
          c for c in backwards_graph_captures if
          not isinstance(c, ops.EagerTensor) and c.graph is self._func_graph]

      existing_outputs = object_identity.ObjectIdentitySet(
          self._func_graph.outputs)
      for capture in captures_from_forward:
        if capture not in existing_outputs:
          existing_outputs.add(capture)
          self._func_graph.outputs.append(capture)

      forward_function, backward_function = _create_forward_backward_with_graph(
          self._attrs, self._func_graph, backwards_graph)
      return forward_function, backward_function

  def _rewrite_forward_and_call_backward(self, op, *doutputs):

    forward_function, backwards_function = self.forward_backward(len(doutputs))
    if not backwards_function.outputs:
      return backwards_function.structured_outputs
    forward_function.add_to_graph(op.graph)

    op._set_func_attr("f", forward_function.name)
    op._set_type_list_attr("Tout", forward_function._output_types)
    op._add_outputs(
        forward_function._output_types[len(op.outputs):],
        forward_function._output_shapes[len(op.outputs):])
    for i in range(len(op.outputs)):
      func_graph_output = forward_function._func_graph_outputs[i]
      handle_data_util.copy_handle_data(func_graph_output, op.outputs[i])

    capture_mapping = dict(
        zip((ops.tensor_id(t) for t in self._func_graph.outputs), op.outputs))
    remapped_captures = [
        capture_mapping.get(ops.tensor_id(capture), capture)
        for capture in backwards_function.captured_inputs
    ]

    cleaned_doutputs = []
    for doutput, placeholder in zip(doutputs, self._func_graph.outputs):
      if backprop_util.IsTrainable(placeholder):
        if isinstance(doutput, ops.IndexedSlices):
          cleaned_doutputs.append(ops.convert_to_tensor(doutput))
        elif doutput is not None:
          cleaned_doutputs.append(doutput)
        else:
          cleaned_doutputs.append(default_gradient.zeros_like(placeholder))

    return backwards_function._call_flat(
        cleaned_doutputs, remapped_captures)

  def get_gradient_function(self):

    return self._rewrite_forward_and_call_backward

  def forward(self, inference_args=None, input_tangents=None):

    del inference_args
    if input_tangents:
      raise errors.InternalError("unexpectedly got forwardprop information in "
                                 "a class that does not support forwardprop.")
    return self._inference_function

  def _backward(self, outputs):

    def _backward_function(*args):
      call_op = outputs[0].op
      return self._rewrite_forward_and_call_backward(call_op, *args)
    return _backward_function, outputs

  def record(self, flat_outputs, inference_args, input_tangents):

    backward_function, to_record = self._backward(flat_outputs)
    tape.record_operation(self._inference_function.signature.name,
                          to_record, inference_args + input_tangents,
                          backward_function)

_ForwardWrapper = collections.namedtuple(
    "_ForwardWrapper", (
        "graph",
        "outputs",
        "output_indices",
        "output_tangents"))

class _TapeGradientFunctions(object):


  def __init__(self, func_graph, attrs, func_graph_deleter,
               forwardprop_input_indices, delayed_rewrite_functions,
               need_gradients_for_jvps):
    self._func_graph = func_graph
    self._forward_graph = None
    self._attrs = attrs
    self._forward = None
    self._backward = None
    self._num_outputs = len(func_graph.outputs)
    self._func_graph_deleter = func_graph_deleter
    self._forwardprop_input_indices = forwardprop_input_indices
    self._forwardprop_output_indices = None
    self._num_forwardprop_outputs = 0
    self._num_inference_outputs = len(func_graph.outputs)
    self._num_trainable_inference_outputs = len(
        [t for t in func_graph.outputs if backprop_util.IsTrainable(t)])
    self._delayed_rewrite_functions = delayed_rewrite_functions
    self._need_gradients_for_jvps = need_gradients_for_jvps

  def _build_functions_for_outputs(
      self, outputs, inference_args, input_tangents):

    handles_to_variables = self._func_graph.variable_captures
    trainable_outputs = []
    trainable_indices = []
    for index, output in enumerate(outputs):

      if backprop_util.IsTrainable(output):
        output = handles_to_variables.get(id(output), output)
        trainable_outputs.append(output)
        trainable_indices.append(index)

    backwards_graph = func_graph_module.FuncGraph(
        _backward_name(self._func_graph.name))
    with backwards_graph.as_default():
      gradients_wrt_outputs = []
      for output in trainable_outputs:
        gradient_shape, gradient_dtype = default_gradient.shape_and_dtype(
            output)
        gradient_placeholder = graph_placeholder(gradient_dtype, gradient_shape)
        handle_data_util.copy_handle_data(output, gradient_placeholder)
        gradients_wrt_outputs.append(gradient_placeholder)
      with ops.device(None):
        gradients_wrt_inputs = gradients_util._GradientsHelper(
            trainable_outputs,
            self._func_graph.inputs,
            grad_ys=gradients_wrt_outputs,
            src_graph=self._func_graph)

      if input_tangents:
        gradients_wrt_inputs = nest.map_structure(
            lambda x: ops.convert_to_tensor(x) if x is not None else None,
            gradients_wrt_inputs)
      captures_from_forward = [
          c for c in backwards_graph.external_captures
          if not isinstance(c, ops.EagerTensor) and c.graph is self._func_graph
      ]
      existing_outputs = object_identity.ObjectIdentitySet(
          self._func_graph.outputs)
      for capture in captures_from_forward:
        if capture not in existing_outputs:
          existing_outputs.add(capture)
          self._func_graph.outputs.append(capture)

    backwards_graph.inputs = (
        gradients_wrt_outputs + backwards_graph.internal_captures)
    backwards_graph.outputs.extend(
        grad
        for grad in nest.flatten(gradients_wrt_inputs, expand_composites=True)
        if grad is not None)
    backwards_graph.structured_outputs = gradients_wrt_inputs

    forward_function, backward_function = _create_forward_backward_with_graph(
        self._attrs, self._func_graph, backwards_graph)

    if not input_tangents:
      return (forward_function, self._func_graph, backward_function,
              None, 0)
    forward_wrapper = self._wrap_forward_function_with_jvps(
        forward_function, backward_function, inference_args, input_tangents)
    (wrapped_backwards_graph,
     forward_wrapper) = self._wrap_backward_function_with_jvp_backprop(
         backward_function, gradients_wrt_outputs, forward_wrapper)
    forward_wrapper = self._shuffle_forward_outputs(forward_wrapper)
    (wrapped_forward_function,
     wrapped_backward_function) = _create_forward_backward_with_graph(
         self._attrs, forward_wrapper.graph, wrapped_backwards_graph)
    if (len(inference_args) + len(input_tangents)
        != len(forward_wrapper.graph.inputs)):
      raise errors.InternalError(
          f"The forward graph had {len(forward_wrapper.graph.inputs)} inputs, "
          f"but we expected {len(inference_args) + len(input_tangents)} "
          f"({len(inference_args)} inference inputs and "
          f"{len(input_tangents)} input tangents).")
    return (wrapped_forward_function, forward_wrapper.graph,
            wrapped_backward_function, forward_wrapper.output_indices,
            len(forward_wrapper.output_tangents))

  def _wrap_forward_function_with_jvps(
      self, forward_function, backward_function,
      inference_args, input_tangents):

    forward_wrapper_graph = func_graph_module.FuncGraph(
        _forward_name(self._func_graph.name))
    with forward_wrapper_graph.as_default():
      with forwardprop_util.push_forwardprop_state():
        forward_captures = {
            ops.tensor_id(internal): external
            for external, internal in self._func_graph.captures}
        for input_index, real_input in enumerate(self._func_graph.inputs):
          input_placeholder = array_ops.placeholder(
              dtype=real_input.dtype,
              shape=real_input.shape)
          capture = forward_captures.get(ops.tensor_id(real_input))
          if capture is not None:
            forward_wrapper_graph.add_capture(capture, input_placeholder)
            if capture.dtype == dtypes.resource:
              handle_data_util.copy_handle_data(capture, input_placeholder)
          else:
            forward_wrapper_graph.inputs.append(input_placeholder)
        for inp, arg in zip(forward_wrapper_graph.inputs, inference_args):
          tape.record_operation(
              "captured_value", [inp], [arg],
              backward_function=lambda x: [x],
              forward_function=lambda x: [x])
        num_inference_inputs = len(inference_args)
        for tape_indices in self._forwardprop_input_indices:
          for input_index, jvp_index in tape_indices:
            input_placeholder = forward_wrapper_graph.inputs[input_index]
            if len(forward_wrapper_graph.inputs) != jvp_index:
              raise errors.InternalError(
                  f"Expected {jvp_index} forward graph inputs, "
                  f"got {len(forward_wrapper_graph.inputs)}.")
            gradient_shape, gradient_dtype = default_gradient.shape_and_dtype(
                input_placeholder)
            jvp_placeholder = graph_placeholder(gradient_dtype, gradient_shape)
            external_jvp = input_tangents[jvp_index - num_inference_inputs]
            forward_wrapper_graph.add_capture(external_jvp, jvp_placeholder)
            tensor_shape.TensorShape(
                external_jvp.shape).assert_is_compatible_with(
                    jvp_placeholder.shape)
            tape.record_operation(
                "captured_value",
                [jvp_placeholder],
                [external_jvp],
                backward_function=lambda x: [x],
                forward_function=lambda x: [x])
        forward_inputs = forward_wrapper_graph.inputs[:num_inference_inputs]
        gradient_function = (
            self._delayed_rewrite_functions._rewrite_forward_and_call_backward)
        with ops.get_default_graph()._override_gradient_function(
            {"PartitionedCall": gradient_function,
             "StatefulPartitionedCall": gradient_function}):
          forward_outputs = forward_function.call(context.context(),
                                                  forward_inputs)
          if isinstance(forward_outputs, ops.Operation):
            forward_outputs = []
        py_backward, _ = self._wrap_backward_function(
            self._func_graph, backward_function, forward_outputs)
      tape.record_operation_forwardprop_only(
          forward_function.signature.name,
          forward_outputs, forward_inputs, py_backward, None)
      output_indices, output_tangents = (
          pywrap_tfe.TFE_Py_PackJVPs(forward_outputs))
      output_tangents = [forward_wrapper_graph.capture(t)
                         for t in output_tangents]
    return _ForwardWrapper(
        graph=forward_wrapper_graph, outputs=forward_outputs,
        output_indices=output_indices, output_tangents=output_tangents)

  def _wrap_backward_function_with_jvp_backprop(
      self, backward_function, gradients_wrt_outputs, forward_wrapper):

    wrapped_backwards_graph = func_graph_module.FuncGraph(
        _backward_name(self._func_graph.name))
    with wrapped_backwards_graph.as_default():
      py_backward, recorded_outputs = self._wrap_backward_function(
          self._func_graph, backward_function, forward_wrapper.outputs)
      trainable_index = 0
      forward_doutputs = []
      doutput_args = []
      for output in recorded_outputs:
        if backprop_util.IsTrainable(output):
          doutput = gradients_wrt_outputs[trainable_index]
          doutput_placeholder = graph_placeholder(doutput.dtype, doutput.shape)
          doutput_args.append(doutput_placeholder)
          forward_doutputs.append(doutput_placeholder)
          trainable_index += 1
        else:
          doutput_args.append(None)

      dinputs = py_backward(*doutput_args)
      existing_outputs = object_identity.ObjectIdentitySet(
          forward_wrapper.outputs + forward_wrapper.output_tangents)
      num_processed_output_tangents = 0
      gradients_wrt_output_tangents = []
      tangent_doutputs = []
      output_tangents = forward_wrapper.output_tangents
      output_indices = forward_wrapper.output_indices
      if self._need_gradients_for_jvps:
        while num_processed_output_tangents != len(output_tangents):
          for output in output_tangents[num_processed_output_tangents:]:
            gradient_shape, gradient_dtype = default_gradient.shape_and_dtype(
                output)
            placeholder = graph_placeholder(gradient_dtype, gradient_shape)
            gradients_wrt_output_tangents.append(placeholder)
            tangent_doutputs.append(placeholder)
          num_processed_output_tangents = len(output_tangents)
          with ops.device(None):
            gradients_wrt_inputs = gradients_util._GradientsHelper(
                output_tangents,
                forward_wrapper.graph.inputs,
                grad_ys=gradients_wrt_output_tangents,
                src_graph=forward_wrapper.graph)
          dinputs = [
              backprop.aggregate_indexed_slices_gradients((existing, new))
              for existing, new in zip(dinputs, gradients_wrt_inputs)
              if existing is not None or new is not None]
          dinputs.extend(gradients_wrt_inputs[len(dinputs):])
          captures_from_forward = [
              c for c in wrapped_backwards_graph.external_captures
              if (not isinstance(c, ops.EagerTensor)
                  and c.graph is forward_wrapper.graph)]
          for capture in captures_from_forward:
            if capture not in existing_outputs:
              existing_outputs.add(capture)
              forward_wrapper.outputs.append(capture)
          output_indices, output_tangents = (
              forwardprop_util.pack_tangents(forward_wrapper.outputs))
          output_tangents = [forward_wrapper.graph.capture(t)
                             for t in output_tangents]
          for t in output_tangents:
            existing_outputs.add(t)
    wrapped_backwards_graph.inputs = (
        forward_doutputs[:self._num_trainable_inference_outputs]
        + tangent_doutputs
        + forward_doutputs[self._num_trainable_inference_outputs:]
        + wrapped_backwards_graph.internal_captures)
    wrapped_backwards_graph.structured_outputs = dinputs
    wrapped_backwards_graph.outputs = [t for t in dinputs if t is not None]
    return (wrapped_backwards_graph,
            forward_wrapper._replace(output_indices=output_indices,
                                     output_tangents=output_tangents))

  def _shuffle_forward_outputs(self, forward_wrapper):

    def _index_map(original):
      if original < self._num_inference_outputs:
        return original
      if original >= len(forward_wrapper.outputs):
        return (original - len(forward_wrapper.outputs)
                + self._num_inference_outputs)
      return original + len(forward_wrapper.output_tangents)
    output_indices = nest.map_structure(
        _index_map, forward_wrapper.output_indices)
    forward_wrapper.graph.outputs = (
        forward_wrapper.outputs[:self._num_inference_outputs]
        + forward_wrapper.output_tangents
        + forward_wrapper.outputs[self._num_inference_outputs:])
    return forward_wrapper._replace(output_indices=output_indices)

  def forward(self, inference_args, input_tangents):

    if self._forward is None:
      (self._forward, self._forward_graph, self._backward,
       self._forwardprop_output_indices, self._num_forwardprop_outputs) = (
           self._forward_and_backward_functions(inference_args, input_tangents))
    return self._forward

  def _wrap_backward_function(self, forward_graph, backward, outputs):

    capture_mapping = dict(
        zip((ops.tensor_id(t) for t in forward_graph.outputs), outputs))
    captured_inputs = backward.captured_inputs
    remapped_captures = [
        capture_mapping.get(ops.tensor_id(capture), capture)
        for capture in captured_inputs
    ]
    if any(t.graph is forward_graph for t in remapped_captures
           if not isinstance(t, ops.EagerTensor)):
      incorrect_mapping = [t for t in remapped_captures
                           if (not isinstance(t, ops.EagerTensor) and
                               t.graph is not forward_graph)]
      raise errors.InternalError("Failed to map all backward graph captures to "
                                 "the forward graph. Incorrectly mapped: "
                                 f"{incorrect_mapping}.")
    variant_zeros_like = {}
    backward_function_inputs = (len(backward.inputs) - len(captured_inputs))
    recorded_outputs = []
    trainable_recorded_outputs = 0
    skip_positions = []
    if self._num_forwardprop_outputs and not self._need_gradients_for_jvps:
      relevant_outputs = (
          outputs[:self._num_inference_outputs]
          + outputs[self._num_inference_outputs
                    + self._num_forwardprop_outputs:])
    else:
      relevant_outputs = outputs
    for output_index, output in enumerate(relevant_outputs):
      if trainable_recorded_outputs < backward_function_inputs:
        recorded_outputs.append(output)
      if backprop_util.IsTrainable(output):
        trainable_recorded_outputs += 1
      else:
        skip_positions.append(output_index)
      if output.dtype == dtypes.variant:
        variant_zeros_like[output_index] = default_gradient.zeros_like(output)

    def _backward_function_wrapper(*args):

      if not backward.outputs:
        return backward.structured_outputs

      processed_args = []
      input_index = 0
      for output_index, arg in enumerate(args):
        if isinstance(arg, ops.IndexedSlices):
          arg = ops.convert_to_tensor(arg)
        if output_index in skip_positions:
          continue
        if arg is None:
          input_placeholder = backward.inputs[
              input_index]
          if input_placeholder.dtype == dtypes.variant:
            arg = variant_zeros_like[output_index]
          else:
            arg = array_ops.zeros(
                *default_gradient.shape_and_dtype(input_placeholder))
        processed_args.append(arg)
        input_index += 1
        if input_index >= backward_function_inputs:
          break
      return backward._call_flat(
          processed_args, remapped_captures)

    return _backward_function_wrapper, recorded_outputs

  def record(self, flat_outputs, inference_args, input_tangents):

    backward_function, to_record = self._wrap_backward_function(
        self._forward_graph, self._backward, flat_outputs)
    if self._forwardprop_output_indices:
      tape.record_operation_backprop_only(
          self._forward.signature.name,
          to_record, inference_args,
          backward_function)
      tape.record_operation_forwardprop_only(
          self._forward.signature.name,
          flat_outputs, inference_args + input_tangents,
          backward_function,
          self._forwardprop_output_indices)
    else:
      tape.record_operation(self._forward.signature.name,
                            to_record, inference_args + input_tangents,
                            backward_function)

class _FirstOrderTapeGradientFunctions(_TapeGradientFunctions):


  def __init__(self, func_graph, attrs, func_graph_deleter,
               forwardprop_input_indices, delayed_rewrite_functions,
               need_gradients_for_jvps):
    super(_FirstOrderTapeGradientFunctions, self).__init__(
        func_graph, attrs, func_graph_deleter, forwardprop_input_indices,
        delayed_rewrite_functions, need_gradients_for_jvps)
    self._func_graph_deleter = func_graph_deleter
    self._forwardprop_input_indices = forwardprop_input_indices

  def _forward_and_backward_functions(self, inference_args, input_tangents):

    outputs = self._func_graph.outputs[:self._num_inference_outputs]
    return self._build_functions_for_outputs(
        outputs, inference_args, input_tangents)

class _HigherOrderTapeGradientFunctions(_TapeGradientFunctions):


  def _forward_and_backward_functions(self, inference_args, input_tangents):

    outputs = []
    iteration_count = 0
    while (len(outputs) < len(self._func_graph.outputs)
           and any(backprop_util.IsTrainable(output)
                   for output in self._func_graph.outputs[len(outputs):])):
      iteration_count += 1
      if iteration_count >= 20 and iteration_count % 5 == 0:
        new_op_with_trainable_output = None
        num_new_trainable_outputs = 0
        for output in self._func_graph.outputs[len(outputs):]:
          if backprop_util.IsTrainable(output):
            num_new_trainable_outputs += 1
            new_op_with_trainable_output = output.op
        logging.warning(
            ("Determining side outputs for the function '{}' is taking longer "
             "than expected ({} iterations, typically this converges in 5 or "
             "so). This could indicate that a gradient registration is adding "
             "new ops to the forward pass every time gradients are generated. "
             "{} new trainable output(s) were added this iteration, one from "
             "the following op:\n {}\nThis may indicate a TensorFlow bug, or "
             "an issue in a tf.custom_gradient.")
            .format(
                self._func_graph.name, iteration_count,
                num_new_trainable_outputs, new_op_with_trainable_output))
      outputs = list(self._func_graph.outputs)
      self._build_functions_for_outputs(
          outputs, inference_args, input_tangents)

    (forward_function, forward_graph,
     backward_function, output_indices, num_output_tangents) = (
         self._build_functions_for_outputs(
             outputs, inference_args, input_tangents))
    if (len(self._func_graph.outputs) > len(outputs)
        and any(backprop_util.IsTrainable(output)
                for output in self._func_graph.outputs[len(outputs):])):
      raise errors.InternalError(
          "Unexpectedly added new outputs to the forward function when "
          "building the backward function: "
          f"{self._func_graph.outputs[len(outputs):]}.")
    return (forward_function, forward_graph, backward_function, output_indices,
            num_output_tangents)

class _ForwardBackwardCall(object):


  __slots__ = [
      "_functions", "_inference_args", "_input_tangents", "_tape_watching"
  ]

  def __init__(self, functions, inference_args, input_tangents, tape_watching):

    self._functions = functions
    self._inference_args = inference_args
    self._input_tangents = input_tangents
    self._tape_watching = tape_watching

  def forward(self):

    forward_function = self._functions.forward(
        self._inference_args, self._input_tangents)
    return forward_function, self._inference_args + self._input_tangents

  def record(self, flat_outputs):

    if (self._tape_watching
        and not isinstance(flat_outputs, ops.Operation)
        and flat_outputs is not None):
      self._functions.record(
          flat_outputs, self._inference_args, self._input_tangents)

_BOUND_VALUE = object()

class ConcreteFunction(core.ConcreteFunction):


  def __init__(self,
               func_graph,
               attrs=None,
               shared_func_graph=True,
               function_spec=None):

    self._arg_keywords = None
    self._num_positional_args = None

    self._func_graph = func_graph
    self._captured_inputs = self._func_graph.external_captures + self._func_graph.deferred_external_captures

    self._set_function_spec(function_spec)

    if attrs and IMPLEMENTS_ATTRIBUTE_NAME in attrs:
      has_resource_vars = any(inp.dtype == dtypes.resource
                              for inp in self.inputs)

      assert not any((has_resource_vars, self._captured_inputs)), (
          'Function {name} has "{attr}={value}" attribute and thus can not '
          "depend on any tensors outside of its signature or modify variables. "
          "\n\nNote: variables are always captured and cause function "
          "re-tracing for every variable called.\n"
          "  inputs: {inputs}\n  captures: {captured}\n\n"
          "To pass a variable to such function use  "
          "use variable.read_value().".format(
              name=func_graph.name,
              attr=IMPLEMENTS_ATTRIBUTE_NAME,
              value=attrs[IMPLEMENTS_ATTRIBUTE_NAME],
              inputs=self.inputs,
              captured=self._captured_inputs))
    self._output_shapes = tuple(
        output.shape for output in self._func_graph.outputs)
    self._attrs = _parse_func_attrs(attrs or {})

    if shared_func_graph:
      self._garbage_collector = None
    else:
      self._garbage_collector = ConcreteFunctionGarbageCollector(func_graph)

    self._delayed_rewrite_functions = _DelayedRewriteGradientFunctions(
        func_graph, self._attrs, self._garbage_collector)
    self._first_order_tape_functions = {}
    self._higher_order_tape_functions = {}
    self._inference_function = self._delayed_rewrite_functions.forward()

  def _set_function_spec(self, function_spec):

    self._function_spec = None
    self._pre_initialized_function_spec = function_spec

    if (function_spec is not None and
        self.structured_input_signature is not None):
      self._initialize_function_spec()

  def _initialize_function_spec(self):

    if self._pre_initialized_function_spec is None:
      return
    assert not self._function_spec, "already initialized"
    function_spec = self._pre_initialized_function_spec
    args = function_spec.fullargspec.args
    arg_specs, kwarg_specs = self.structured_input_signature
    vararg_indices = range(len(function_spec.arg_names), len(arg_specs))
    fullargspec = tf_inspect.FullArgSpec(
        args=list(args) + ["<arg{}>".format(i + 1) for i in vararg_indices],
        varargs=None,
        varkw=None,
        defaults=[_BOUND_VALUE] * len(arg_specs),
        kwonlyargs=list(sorted(kwarg_specs)),
        kwonlydefaults=dict((k, _BOUND_VALUE) for k in kwarg_specs),
        annotations=function_spec.fullargspec.annotations)
    self._function_spec = FunctionSpec(
        fullargspec,
        function_spec.is_method,
        function_spec.input_signature,
        function_spec.is_pure,
        name=self._func_graph.name)

  @property
  def variables(self):

    return tuple(self._func_graph.variables)

  @property
  def trainable_variables(self):

    return tuple(self._func_graph.trainable_variables)

  def __call__(self, *args, **kwargs):

    return self._call_impl(args, kwargs)

  def _call_impl(self, args, kwargs, cancellation_manager=None):

    with trace.Trace(self._func_graph.name, tf_function_call="concrete"):
      if self._function_spec is not None:
        try:
          return self._call_with_structured_signature(args, kwargs,
                                                      cancellation_manager)
        except TypeError as structured_err:
          try:
            return self._call_with_flat_signature(args, kwargs,
                                                  cancellation_manager)
          except TypeError:
            raise structured_err

      return self._call_with_flat_signature(args, kwargs, cancellation_manager)

  def _call_with_flat_signature(self, args, kwargs, cancellation_manager):

    if len(args) > self._num_positional_args:
      raise TypeError(
          f"{self._flat_signature_summary()} takes {self._num_positional_args} "
          f"positional arguments, got {len(args)}.")
    args = list(args)
    kwargs = dict(kwargs)
    for keyword in self._arg_keywords[len(args):]:
      try:
        args.append(kwargs.pop(compat.as_str(keyword)))
      except KeyError:
        specified_keywords = (
            list(self._arg_keywords[:len(args)]) + list(kwargs.keys()))
        missing_required_args = sorted(
            set(self._arg_keywords) - set(specified_keywords))
        raise TypeError(f"{self._flat_signature_summary()} missing required "
                        f"arguments: {', '.join(missing_required_args)}.")
    if kwargs:
      positional_arg_keywords = set(self._arg_keywords[:len(args)])
      for unused_key in kwargs:
        if unused_key in positional_arg_keywords:
          raise TypeError(f"{self._flat_signature_summary()} got two values "
                          f"for '{unused_key}'.")
      raise TypeError(f"{self._flat_signature_summary()} got unexpected "
                      f"keyword arguments: {', '.join(sorted(kwargs))}.")

    for i, arg in enumerate(args):
      if not isinstance(
          arg, (ops.Tensor, resource_variable_ops.BaseResourceVariable)):
        raise TypeError(f"{self._flat_signature_summary()}: expected argument "
                        f"#{i}(zero-based) to be a Tensor; "
                        f"got {type(arg).__name__} ({arg}).")
    return self._call_flat(args, self.captured_inputs, cancellation_manager)

  def _call_with_structured_signature(self, args, kwargs, cancellation_manager):

    args, kwargs, _, filtered_flat_args = \
        self._function_spec.canonicalize_function_inputs(*args, **kwargs)
    self._structured_signature_check_missing_args(args, kwargs)
    self._structured_signature_check_unexpected_args(args, kwargs)
    self._structured_signature_check_arg_types(args, kwargs)
    return self._call_flat(
        filtered_flat_args,
        captured_inputs=self.captured_inputs,
        cancellation_manager=cancellation_manager)

  def _structured_signature_check_missing_args(self, args, kwargs):

    arg_specs, kwarg_specs = self.structured_input_signature
    missing_arguments = []
    for i, (arg, spec) in enumerate(zip(args, arg_specs)):
      if arg is _BOUND_VALUE and _contains_type_spec(spec):
        missing_arguments.append(self._function_spec.arg_names[i])
    for (name, arg) in kwargs.items():
      if arg is _BOUND_VALUE and _contains_type_spec(kwarg_specs[name]):
        missing_arguments.append(name)
    if missing_arguments:
      raise TypeError(f"{self._structured_signature_summary()} missing "
                      "required arguments: "
                      f"{', '.join(sorted(missing_arguments))}.")

  def _structured_signature_check_unexpected_args(self, args, kwargs):

    arg_specs, kwarg_specs = self.structured_input_signature
    if len(args) > len(arg_specs):
      raise TypeError(
          f"{self._structured_signature_summary()} takes "
          f"{len(self._function_spec.arg_names)} positional arguments but got "
          f"{len(args)}.")
    if len(kwargs) > len(kwarg_specs):
      extra_args = set(kwargs) - set(kwarg_specs)
      raise TypeError(f"{self._structured_signature_summary()} got unexpected "
                      f"keyword arguments: {', '.join(extra_args)}.")

  def _structured_signature_check_arg_types(self, args, kwargs):

    arg_specs, kwarg_specs = self.structured_input_signature
    for i, (arg, spec) in enumerate(zip(args, arg_specs)):
      name = self._function_spec.arg_names[i]
      self._structured_signature_check_arg_type(arg, spec, name)
    for (name, arg) in kwargs.items():
      self._structured_signature_check_arg_type(arg, kwarg_specs[name], name)

  def _structured_signature_check_arg_type(self, arg, spec, name):

    if arg is _BOUND_VALUE:
      return

    try:
      nest.assert_same_structure(arg, spec, expand_composites=True)
    except (ValueError, TypeError):
      try:
        nest.assert_same_structure(arg, spec, expand_composites=False)
        expected, got = spec, arg
      except (ValueError, TypeError):
        expected, got = _structure_summary(spec), _structure_summary(arg)
      raise TypeError(f"{self._structured_signature_summary()}: argument "
                      f"{name} had incorrect type\n"
                      f"  expected: {expected}\n"
                      f"       got: {got}")

    arg_pieces = nest.flatten(arg, expand_composites=True)
    spec_pieces = nest.flatten(spec, expand_composites=True)
    for (arg_piece, spec_piece) in zip(arg_pieces, spec_pieces):
      if isinstance(spec_piece, tensor_spec.DenseSpec):
        tensor_types = (ops.Tensor, resource_variable_ops.BaseResourceVariable)
        if not isinstance(arg_piece, tensor_types):
          raise TypeError(f"{self._structured_signature_summary()} expected a "
                          f"Tensor in {name}, but got "
                          f"{type(arg_piece).__name__} value {arg_piece}.")
      elif arg_piece is not _BOUND_VALUE:
        try:
          arg_matches_spec = bool(arg_piece == spec_piece)
        except (ValueError, TypeError):
          logging.vlog(1, "Error matching value with spec", exc_info=True)
          arg_matches_spec = False
        if not arg_matches_spec:
          raise TypeError(
              f"ConcreteFunction {self._structured_signature_summary()} was "
              f"constructed with {type(spec_piece).__name__} value "
              f"{spec_piece} in {name}, but was called with "
              f"{type(arg_piece).__name__} value {arg_piece}.")

  def _call_flat(self, args, captured_inputs, cancellation_manager=None):

    ctx = context.context()
    executing_eagerly = ctx.executing_eagerly()

    default_graph = ops.get_default_graph()
    if default_graph.building_function and not self._func_graph.saveable:
      default_graph.mark_as_unsaveable(self._func_graph.saving_errors)

    if (tape.could_possibly_record() or
        hasattr(default_graph, "watch_variable")):
      for v in self._func_graph.variables:
        resource_variable_ops.variable_accessed(v)

    tensor_inputs = []
    variables_used = set([])
    for i, arg in enumerate(args):
      if isinstance(arg, resource_variable_ops.BaseResourceVariable):
        if id(arg.handle) in variables_used:
          continue
        resource_variable_ops.variable_accessed(arg)
        tensor_inputs.append(arg.handle)
        variables_used.add(id(arg.handle))
      elif isinstance(arg, ops.Tensor):
        tensor_inputs.append(arg)
        if not executing_eagerly:
          graph_input_shape = tensor_shape.TensorShape(
              self._func_graph.inputs[i].shape)
          if not graph_input_shape.is_compatible_with(arg.shape):
            if self._arg_keywords:
              arg_name = "'{}'".format(self._arg_keywords[i])
            else:
              arg_name = "with index {}".format(i)
            raise ValueError(
                f"The argument {arg_name} (value {arg}) is not compatible with "
                "the shape this function was traced with. Expected shape "
                f"{self._func_graph.inputs[i].shape}, but got shape "
                f"{arg.shape}.\n\nIf you called get_concrete_function, you may "
                "need to pass a tf.TensorSpec(..., shape=...) with a less "
                "specific shape, having None on axes which can vary.")
      else:
        raise ValueError(f"{i:d}-th input {arg} must be a Tensor, got "
                         f"{type(arg)} when calling {self._func_graph.name}.")
    args = tensor_inputs + captured_inputs
    possible_gradient_type = gradients_util.PossibleTapeGradientTypes(args)
    if (possible_gradient_type == gradients_util.POSSIBLE_GRADIENT_TYPES_NONE
        and executing_eagerly):
      return self._build_call_outputs(self._inference_function.call(
          ctx, args, cancellation_manager=cancellation_manager))
    forward_backward = self._select_forward_and_backward_functions(
        args,
        possible_gradient_type,
        executing_eagerly)
    forward_function, args_with_tangents = forward_backward.forward()
    if executing_eagerly:
      flat_outputs = forward_function.call(
          ctx, args_with_tangents, cancellation_manager=cancellation_manager)
    else:
      with default_graph._override_gradient_function(
          {"PartitionedCall": self._get_gradient_function(),
           "StatefulPartitionedCall": self._get_gradient_function()}):
        flat_outputs = forward_function.call(ctx, args_with_tangents)
    forward_backward.record(flat_outputs)
    return self._build_call_outputs(flat_outputs)

  def _experimental_with_cancellation_manager(self, cancellation_manager):


    def cancellable_call(*args, **kwargs):
      return self._call_impl(
          args, kwargs, cancellation_manager=cancellation_manager)

    return cancellable_call

  @property
  def name(self):

    return self._delayed_rewrite_functions.forward().name

  @property
  def graph(self):

    return self._func_graph

  @property
  def inputs(self):

    return self._func_graph.inputs

  @property
  def structured_input_signature(self):

    return self._func_graph.structured_input_signature

  @property
  def outputs(self):

    return self._func_graph.outputs

  @property
  def structured_outputs(self):

    return self._func_graph.structured_outputs

  def set_external_captures(self, captures):

    self._captured_inputs = captures

  def replace_capture_with_deferred_capture(self,
                                            tensor,
                                            closure,
                                            spec,
                                            placeholder=None,
                                            default_value=None):

    capture_index = None
    for i, capture in enumerate(self._captured_inputs):
      if id(tensor) == id(capture):
        capture_index = i
        break

    if placeholder is None:
      if capture_index is None:
        raise ValueError(
            f"Did not find `tensor` argument {tensor} in the ConcreteFunction's"
            " captured inputs list, and did not receive a placeholder argument."
            " Thus we're unable to infer the internal placeholder. ")

      placeholder = self.inputs[-len(self._captured_inputs) + capture_index]

    if not (spec.is_compatible_with(tensor) or
            spec.is_compatible_with(placeholder)):
      raise ValueError(
          f"Attempting to substitute closure with spec {spec} that's "
          f"incompatible with the original capture {tensor} or the internal "
          f"placeholder {placeholder}.")

    self._func_graph.replace_capture_with_deferred_capture(
        tensor=tensor,
        closure=closure,
        spec=spec,
        placeholder=placeholder,
        default_value=default_value)

    if capture_index is not None:
      self._captured_inputs[capture_index] = closure

  @property
  def captured_inputs(self):

    return nest.flatten(
        [x() if callable(x) else x for x in self._captured_inputs],
        expand_composites=True)

  @property
  def function_def(self):

    return self._delayed_rewrite_functions.forward().definition

  @property
  def output_shapes(self):

    return nest.map_structure(
        lambda x: getattr(x, "shape", tensor_shape.TensorShape(None)),
        composite_tensor.replace_composites_with_components(
            self._func_graph.structured_outputs),
        expand_composites=False)

  @property
  def output_dtypes(self):
    return nest.map_structure(
        lambda x: x.dtype if x is not None else None,
        composite_tensor.replace_composites_with_components(
            self._func_graph.structured_outputs),
        expand_composites=False)

  def add_to_graph(self, g=None):


    if not context.executing_eagerly() and not g:
      g = ops.get_default_graph()
    self._delayed_rewrite_functions.forward().add_to_graph(g)

  def add_gradient_functions_to_graph(self, g=None):

    if not context.executing_eagerly() and not g:
      g = ops.get_default_graph()
    self._delayed_rewrite_functions.forward().add_to_graph(g)
    forward_function, backward_function = (
        self._delayed_rewrite_functions.forward_backward())
    forward_function.add_to_graph(g)
    backward_function.add_to_graph(g)

  def _get_gradient_function(self):

    return self._delayed_rewrite_functions._rewrite_forward_and_call_backward

  def _select_forward_and_backward_functions(
      self, args, possible_gradient_type, executing_eagerly):

    if executing_eagerly:
      input_tangents = forwardprop_util.pack_tangents(args)
    else:
      input_tangents = forwardprop_util.TangentInfo()
    need_gradients_for_jvps = tape.should_record_backprop(
        input_tangents.tangents)
    cache_key = (need_gradients_for_jvps, input_tangents.indices)
    if (possible_gradient_type
        == gradients_util.POSSIBLE_GRADIENT_TYPES_FIRST_ORDER):
      if input_tangents.indices or executing_eagerly:
        functions = self._first_order_tape_functions.get(cache_key, None)
        if functions is None:
          functions = _FirstOrderTapeGradientFunctions(
              self._func_graph, self._attrs, self._garbage_collector,
              forwardprop_input_indices=input_tangents.indices,
              delayed_rewrite_functions=self._delayed_rewrite_functions,
              need_gradients_for_jvps=need_gradients_for_jvps)
          self._first_order_tape_functions[cache_key] = functions
        return _ForwardBackwardCall(
            functions, args, input_tangents.tangents, tape_watching=True)
      else:
        return _ForwardBackwardCall(
            self._delayed_rewrite_functions, args, input_tangents.tangents,
            tape_watching=True)
    elif (possible_gradient_type
          == gradients_util.POSSIBLE_GRADIENT_TYPES_HIGHER_ORDER):
      functions = self._higher_order_tape_functions.get(
          cache_key, None)
      if functions is None:
        functions = _HigherOrderTapeGradientFunctions(
            self._func_graph, self._attrs, self._garbage_collector,
            forwardprop_input_indices=input_tangents.indices,
            delayed_rewrite_functions=self._delayed_rewrite_functions,
            need_gradients_for_jvps=need_gradients_for_jvps)
        self._higher_order_tape_functions[cache_key] = functions
      return _ForwardBackwardCall(functions, args, input_tangents.tangents,
                                  tape_watching=True)
    return _ForwardBackwardCall(
        self._delayed_rewrite_functions, args, input_tangents.tangents,
        tape_watching=False)

  def _build_call_outputs(self, result):

    if self._func_graph.structured_outputs is None:
      return result

    outputs_list = nest.flatten(
        self._func_graph.structured_outputs, expand_composites=True)
    j = 0
    for i, o in enumerate(outputs_list):
      if o is not None:
        handle_data_util.copy_handle_data(self.outputs[j], result[j])
        outputs_list[i] = result[j]
        j += 1
    ret = nest.pack_sequence_as(self._func_graph.structured_outputs,
                                outputs_list, expand_composites=True)
    return ret

  @property
  def _as_name_attr_list(self):

    ret = attr_value_pb2.NameAttrList(name=self.name)
    for name, value in self._attrs.items():
      ret.attr[name].CopyFrom(value)
    return ret

  def _structured_signature_summary(self, default_values=False):

    assert self._function_spec is not None
    arg_specs, kwarg_specs = self.structured_input_signature
    arg_names = list(self._function_spec.arg_names)

    arg_names = arg_names[:len(arg_specs)]

    if default_values:
      for i in range(len(arg_names)):
        if not _contains_type_spec(arg_specs[i]):
          arg_names[i] += "={}".format(arg_specs[i])
    if kwarg_specs:
      arg_names.append("*")
      for name, spec in kwarg_specs.items():
        arg_names.append(name)
        if default_values and not _contains_type_spec(spec):
          arg_names[-1] += "={}".format(spec)
    signature = f"{self._func_graph.name}({', '.join(arg_names)})"

    return signature

  def _flat_signature_summary(self):

    assert self._arg_keywords is not None
    assert self._num_positional_args is not None
    arg_names = self._arg_keywords
    if self._num_positional_args > len(arg_names):
      arg_names.extend(
          "<arg{}>".format(i + 1)
          for i in range(len(arg_names), self._num_positional_args))
    return f"{self._func_graph.name}({', '.join(arg_names)})"

  def pretty_printed_signature(self, verbose=True):

    if not verbose:
      return self._structured_signature_summary(default_values=True)

    def pretty_print_spec(spec):

      if isinstance(spec, tensor_spec.TensorSpec):
        return "{} Tensor, shape={}".format(spec.dtype.name, spec.shape)
      elif nest.is_sequence(spec):
        pieces = nest.flatten(spec, expand_composites=False)
        markers = [_Marker("<{}>".format(i + 1)) for i in range(len(pieces))]
        structure = nest.pack_sequence_as(spec, markers)
        result = pprint.pformat(structure, width=10000)
        for (marker, piece) in zip(markers, pieces):
          result += "\n      {}: {}".format(marker, pretty_print_spec(piece))
        return result
      else:
        return repr(spec)

    lines = [self._structured_signature_summary(default_values=True)]
    arg_specs, kwarg_specs = self.structured_input_signature
    names = list(self._function_spec.arg_names)

    arg_details = []
    for (name, spec) in zip(names[:len(arg_specs)], list(arg_specs)):
      if _contains_type_spec(spec):
        arg_details.append("    {}: {}".format(name, pretty_print_spec(spec)))

    if kwarg_specs:
      for kwarg in sorted(kwarg_specs):
        spec = kwarg_specs[kwarg]
        if _contains_type_spec(spec):
          arg_details.append("    {}: {}".format(
              kwarg, pretty_print_spec(spec)))

    if arg_details:
      lines.append("  Args:")
      lines.extend(arg_details)
    lines.append("  Returns:")

    def spec_from_value(value):
      if isinstance(value, type_spec.TypeSpec):
        return value
      return type_spec.type_spec_from_value(value)

    lines.append("    {}".format(
        pretty_print_spec(
            nest.map_structure(spec_from_value, self.structured_outputs))))

    return "\n".join(lines)

  def __repr__(self):
    if self._function_spec is not None:
      return "<ConcreteFunction {} at 0x{:X}>".format(
          self.pretty_printed_signature(verbose=False), id(self))
    elif not (self._num_positional_args is None or self._arg_keywords is None):
      return "<ConcreteFunction {} at 0x{:X}>".format(
          self._flat_signature_summary(), id(self))
    else:
      return object.__repr__(self)

  def __str__(self):
    if self._function_spec is not None:
      return "ConcreteFunction {}".format(self.pretty_printed_signature())
    else:
      return self.__repr__()

_pywrap_utils.RegisterType("Tensor", ops.Tensor)
_pywrap_utils.RegisterType("EagerTensor", ops.EagerTensor)
_pywrap_utils.RegisterType("IndexedSlices", ops.IndexedSlices)

def _deterministic_dict_values(dictionary):
  return tuple(dictionary[key] for key in sorted(dictionary))

class FunctionSpec(object):


  @staticmethod
  def from_function_and_signature(python_function,
                                  input_signature,
                                  is_pure=False,
                                  experimental_follow_type_hints=False,
                                  jit_compile=None):

    fullargspec = tf_inspect.getfullargspec(python_function)
    if (input_signature is not None and
        set(fullargspec.kwonlyargs) - set(fullargspec.kwonlydefaults or ())):
      nodefault_kwonlyargs = set(fullargspec.kwonlyargs)
      if fullargspec.kwonlydefaults is not None:
        nodefault_kwonlyargs -= set(fullargspec.kwonlydefaults)
      raise ValueError("Cannot build TF function from "
                       f"{python_function.__name__}: keyword-only arguments "
                       "must have default values when input_signature is "
                       "provided. Got keyword-only arguments without default "
                       f"values: {sorted(nodefault_kwonlyargs)}.")

    is_method = tf_inspect.isanytargetmethod(python_function)

    _, unwrapped = tf_decorator.unwrap(python_function)
    if isinstance(unwrapped, functools.partial):
      if fullargspec.defaults or fullargspec.kwonlydefaults:
        new_defaults = fullargspec.defaults
        new_args = fullargspec.args
        if fullargspec.defaults:
          old_args = fullargspec.args
          old_defaults = fullargspec.defaults

          no_default = object()
          num_args_without_defaults = len(old_args) - len(old_defaults)
          left_padding = tuple([no_default] * num_args_without_defaults)

          args_with_defaults = zip(old_args, left_padding + old_defaults)

          non_keyword_defaults_mask = [
              0 if key in unwrapped.keywords else 1 for key in old_args
          ]
          new_args_with_defaults = list(
              itertools.compress(args_with_defaults, non_keyword_defaults_mask))
          new_args = [arg for arg, _ in new_args_with_defaults]
          new_defaults = [
              default for _, default in new_args_with_defaults
              if default is not no_default
          ]
        fullargspec = tf_inspect.FullArgSpec(
            args=new_args,
            varargs=fullargspec.varargs,
            varkw=fullargspec.varkw,
            defaults=new_defaults,
            kwonlyargs=[],
            kwonlydefaults={},
            annotations=fullargspec.annotations)

    while isinstance(python_function, functools.partial):
      python_function = python_function.func
    name = getattr(python_function, "__name__", "f")

    return FunctionSpec(
        fullargspec,
        is_method,
        input_signature,
        is_pure=is_pure,
        jit_compile=jit_compile,
        experimental_follow_type_hints=experimental_follow_type_hints,
        name=name)

  def __init__(self,
               fullargspec,
               is_method,
               input_signature,
               is_pure=False,
               experimental_follow_type_hints=False,
               name=None,
               jit_compile=None):

    self._fullargspec = fullargspec
    self._is_method = is_method
    self._is_pure = is_pure
    self._jit_compile = jit_compile
    self._experimental_follow_type_hints = experimental_follow_type_hints

    self._name = name or "f"

    if self._is_method:
      args = fullargspec.args[1:]
    else:
      args = fullargspec.args

    self._args_to_indices = {arg: i for i, arg in enumerate(args)}
    self._arg_names = args

    default_values = fullargspec.defaults
    offset = len(args) - len(default_values or [])
    self._arg_indices_to_default_values = {
        offset + index: default
        for index, default in enumerate(default_values or [])
    }
    self._arg_indices_no_default_values = set(range(len(args))) - set(
        self._arg_indices_to_default_values)
    if input_signature is None:
      self._input_signature = None
    else:
      self._input_signature = tuple(input_signature)
      self._flat_input_signature = tuple(nest.flatten(input_signature,
                                                      expand_composites=True))

  @property
  def fullargspec(self):
    return self._fullargspec

  @property
  def is_method(self):
    return self._is_method

  @property
  def args_to_indices(self):
    return self._args_to_indices

  @property
  def kwargs_to_include(self):
    return self._kwargs_to_include

  @property
  def input_signature(self):
    return self._input_signature

  @property
  def flat_input_signature(self):
    return self._flat_input_signature

  @property
  def is_pure(self):
    return self._is_pure

  @property
  def jit_compile(self):
    return self._jit_compile

  @property
  def arg_names(self):
    return self._arg_names

  @property
  def vararg_name(self):
    return self._fullargspec.varargs

  @property
  def varkw_name(self):
    return self._fullargspec.varkw

  def signature_summary(self, default_values=False):

    args = list(self._arg_names)
    if default_values:
      for (i, default) in self._arg_indices_to_default_values.items():
        args[i] += "={}".format(default)
    if self._fullargspec.kwonlyargs:
      args.append("*")
      for arg_name in self._fullargspec.kwonlyargs:
        args.append(arg_name)
        if default_values and arg_name in self._fullargspec.kwonlydefaults:
          args[-1] += "={}".format(self._fullargspec.kwonlydefaults[arg_name])
    return f"{self._name}({', '.join(args)})"

  def _to_tensor_or_tensor_spec(self, x):
    return (x if isinstance(x, (ops.Tensor, tensor_spec.TensorSpec))
            else ops.convert_to_tensor(x))

  def _convert_variables_to_tensors(self, args, kwargs):
    args = [self._to_tensor_or_tensor_spec(x) for x in args]
    kwargs = {kw: self._to_tensor_or_tensor_spec(x)
              for kw, x in kwargs.items()}
    return tuple(args), kwargs

  def _convert_annotated_args_to_tensors(self, args, kwargs):

    if self.input_signature is not None:
      return

    args = list(args)
    for i, arg in enumerate(args):
      if i < len(self._fullargspec.args):
        annotation_key = self._fullargspec.args[i]
      else:
        annotation_key = self._fullargspec.varargs
      arg_annotation = self._fullargspec.annotations.get(annotation_key, None)

      if arg_annotation == ops.Tensor:
        args[i] = self._to_tensor_or_tensor_spec(arg)

    for kw, v in kwargs.items():
      if kw in self._fullargspec.kwonlyargs or kw in self._fullargspec.args:
        annotation_key = kw
      else:
        annotation_key = self._fullargspec.varkw
      kwarg_annotation = self._fullargspec.annotations.get(annotation_key, None)
      if kwarg_annotation == ops.Tensor:
        kwargs[kw] = self._to_tensor_or_tensor_spec(v)
    return tuple(args), kwargs

  def _validate_inputs(self, flat_inputs):

    for inp in flat_inputs:
      if isinstance(inp, weakref.ref):
        raise ValueError(
            f"weakref input {inp} not supported for function {self._name}")

  def canonicalize_function_inputs(self, *args, **kwargs):

    if self._is_pure:
      args, kwargs = self._convert_variables_to_tensors(args, kwargs)
    if self._experimental_follow_type_hints:
      args, kwargs = self._convert_annotated_args_to_tensors(args, kwargs)
    arglen = len(args)
    if self._input_signature is not None:
      if arglen > len(self._input_signature):
        raise TypeError(f"{self.signature_summary()} specifies "
                        f"{len(self._input_signature)} positional arguments, "
                        f"but got {arglen}.")
      for arg in six.iterkeys(kwargs):
        index = self._args_to_indices.get(arg, None)
        if index is None:
          raise TypeError(f"{self.signature_summary()} got unexpected keyword "
                          f"argument `{arg}`.")
        if index >= len(self._input_signature):
          raise TypeError(
              f"{self.signature_summary()} got keyword argument `{arg}` that "
              "was not included in input_signature.")

    if not kwargs:
      inputs = args
      if self._arg_indices_to_default_values:
        try:
          inputs += tuple(self._arg_indices_to_default_values[i]
                          for i in range(arglen, len(self._arg_names)))
        except KeyError:
          missing_args = [
              self._arg_names[i]
              for i in range(arglen, len(self._arg_names))
              if i not in self._arg_indices_to_default_values
          ]
          raise TypeError(f"{self.signature_summary()} missing required "
                          f"arguments: {', '.join(missing_args)}.")

      if self._fullargspec.kwonlydefaults:
        kwargs.update(self._fullargspec.kwonlydefaults)
    else:
      arg_indices_to_values = {
          index: default for index, default in six.iteritems(
              self._arg_indices_to_default_values) if index >= arglen
      }
      consumed_args = []
      missing_arg_indices = self._arg_indices_no_default_values - set(
          range(arglen))
      for arg, value in six.iteritems(kwargs):
        index = self._args_to_indices.get(arg, None)
        if index is not None:
          if index < arglen:
            raise TypeError(f"{self.signature_summary()} got two values for "
                            f"{arg!r}.")
          arg_indices_to_values[index] = value
          missing_arg_indices.discard(index)
          consumed_args.append(arg)
      for arg in consumed_args:
        kwargs.pop(arg)
      inputs = args + _deterministic_dict_values(arg_indices_to_values)
      if missing_arg_indices:
        missing_args = [self._arg_names[i] for i in sorted(missing_arg_indices)]
        if len(missing_args) == 1:
          raise TypeError(f"{self.signature_summary()} missing 1 required "
                          f"argument: {missing_args[0]}.")
        else:
          raise TypeError(f"{self.signature_summary()} missing required "
                          f"arguments: {', '.join(missing_args)}.")

      if kwargs and self._input_signature is not None:
        raise TypeError("Keyword arguments are not supported when "
                        "input_signature is provided. Signature: "
                        f"{self.signature_summary()}. Keyword arguments: "
                        f"{kwargs}.")

      if self._fullargspec.kwonlydefaults:
        for (kwarg, default) in self._fullargspec.kwonlydefaults.items():
          kwargs.setdefault(kwarg, default)

    if self._input_signature is None:
      inputs, flat_inputs, filtered_flat_inputs = _convert_numpy_inputs(inputs)
      kwargs, flat_kwargs, filtered_flat_kwargs = _convert_numpy_inputs(kwargs)
      flat_inputs += flat_kwargs
      filtered_flat_inputs += filtered_flat_kwargs
    else:
      inputs, flat_inputs, filtered_flat_inputs = _convert_inputs_to_signature(
          inputs, self._input_signature, self._flat_input_signature)

    self._validate_inputs(flat_inputs)

    return inputs, kwargs, flat_inputs, filtered_flat_inputs

def _convert_numpy_inputs(inputs):

  flat_inputs = nest.flatten(inputs, expand_composites=True)

  need_packing = False
  filtered_flat_inputs = []
  for index, value in enumerate(flat_inputs):
    if isinstance(value,
                  (ops.Tensor, resource_variable_ops.BaseResourceVariable)):
      filtered_flat_inputs.append(value)
    elif hasattr(value, "__array__") and not (
        hasattr(value, "_should_act_as_resource_variable") or
        isinstance(value, (np.str_, type, composite_tensor.CompositeTensor))):
      a = value.__array__()
      if not isinstance(a, np.ndarray):
        raise TypeError(f"The output of __array__ must be an np.ndarray, "
                        f"got {type(a)} from {value}.")
      flat_inputs[index] = constant_op.constant(a)
      filtered_flat_inputs.append(flat_inputs[index])
      need_packing = True
  if need_packing:
    return (nest.pack_sequence_as(
        structure=inputs, flat_sequence=flat_inputs,
        expand_composites=True), flat_inputs, filtered_flat_inputs)
  else:
    return inputs, flat_inputs, filtered_flat_inputs

def _convert_inputs_to_signature(inputs, input_signature, flat_input_signature):


  def format_error_message(inputs, input_signature):
    return ("  inputs: (\n" + "    " + ",\n    ".join(str(i) for i in inputs) +
            ")\n" + "  input_signature: (\n" + "    " +
            ",\n    ".join(str(i) for i in input_signature) + ")")

  try:
    flatten_inputs = nest.flatten_up_to(
        input_signature,
        inputs[:len(input_signature)],
        expand_composites=True,
        check_types=False)
  except ValueError:
    raise ValueError("Structure of Python function inputs does not match "
                     "input_signature:\n"
                     f"{format_error_message(inputs, input_signature)}.")

  need_packing = False
  for index, (value, spec) in enumerate(zip(flatten_inputs,
                                            flat_input_signature)):
    if (isinstance(spec, tensor_spec.TensorSpec) and
        not _pywrap_utils.IsTensor(value)):
      try:
        flatten_inputs[index] = ops.convert_to_tensor(
            value, dtype_hint=spec.dtype)
        need_packing = True
      except ValueError:
        raise ValueError("When input_signature is provided, all inputs to "
                         "the Python function must be convertible to "
                         "tensors:\n"
                         f"{format_error_message(inputs, input_signature)}.")

  if any(not spec.is_compatible_with(other) for spec, other in zip(
      flat_input_signature,
      flatten_inputs)):
    raise ValueError("Python inputs incompatible with input_signature:\n"
                     f"{format_error_message(inputs, input_signature)}.")

  if need_packing:
    inputs = nest.pack_sequence_as(
        structure=input_signature,
        flat_sequence=flatten_inputs,
        expand_composites=True)

  flat_inputs = nest.flatten(inputs, expand_composites=True)

  return (inputs, flat_inputs, [
      t for t in flat_inputs
      if isinstance(t, (ops.Tensor, resource_variable_ops.BaseResourceVariable))
  ])

class FunctionCache(object):


  __slots__ = [
      "missed", "primary", "arg_relaxed_specs", "arg_relaxed",
      "_garbage_collectors"
  ]

  def __init__(self):
    self.missed = set()
    self.primary = collections.OrderedDict()
    self.arg_relaxed_specs = collections.OrderedDict()
    self.arg_relaxed = collections.OrderedDict()
    self._garbage_collectors = [
        _FunctionGarbageCollector(self.primary),
        _FunctionGarbageCollector(self.arg_relaxed),
        _FunctionGarbageCollector(self.arg_relaxed_specs)]

  def all_values(self):

    primary_functions = set(self.primary.values())
    return list(self.primary.values()) + [
        v for v in self.arg_relaxed.values() if v not in primary_functions]

class Function(object):


  def __init__(self,
               python_function,
               name,
               input_signature=None,
               attributes=None,
               autograph=True,
               autograph_options=None,
               experimental_relax_shapes=False,
               capture_by_value=None,
               jit_compile=None,
               experimental_follow_type_hints=False):

    self._python_function = python_function
    pure_function = attributes and IMPLEMENTS_ATTRIBUTE_NAME in attributes
    self._function_spec = FunctionSpec.from_function_and_signature(
        python_function,
        input_signature,
        is_pure=pure_function,
        experimental_follow_type_hints=experimental_follow_type_hints)
    self._name = name
    self._autograph = autograph
    self._autograph_options = autograph_options
    self._experimental_relax_shapes = experimental_relax_shapes
    self._function_cache = FunctionCache()
    self._function_attributes = attributes or {}
    self._capture_by_value = capture_by_value
    self.tracing_count = 0
    if self.input_signature is not None:
      self._hashable_input_signature = hash(self.flat_input_signature)

    self._lock = threading.RLock()
    self._descriptor_cache = weakref.WeakKeyDictionary()
    self._jit_compile = jit_compile
    self._experimental_follow_type_hints = experimental_follow_type_hints

  def __call__(self, *args, **kwargs):

    with self._lock:
      (graph_function,
       filtered_flat_args) = self._maybe_define_function(args, kwargs)
    return graph_function._call_flat(
        filtered_flat_args, captured_inputs=graph_function.captured_inputs)

  @property
  def python_function(self):

    return self._python_function

  @property
  def function_spec(self):
    return self._function_spec

  @property
  def input_signature(self):

    return self._function_spec.input_signature

  @property
  def flat_input_signature(self):

    return self._function_spec.flat_input_signature

  def _get_concrete_function_internal_garbage_collected(self, *args, **kwargs):

    if self.input_signature:
      args, kwargs = None, None
    with self._lock:
      graph_function, _ = self._maybe_define_function(args, kwargs)
    return graph_function

  def _get_concrete_function_internal(self, *args, **kwargs):

    graph_function = self._get_concrete_function_internal_garbage_collected(
        *args, **kwargs)
    graph_function._garbage_collector.release()
    return graph_function

  def _get_concrete_function_garbage_collected(self, *args, **kwargs):

    if self.input_signature:
      if kwargs:
        raise ValueError("Cannot define a TensorFlow function from a Python "
                         "function with keyword arguments when "
                         "input_signature is provided, got keyword arguments "
                         f"({kwargs}) with input_signature "
                         f"({self.input_signature}).")
      if args:
        if not is_same_structure(self.input_signature, args):
          raise ValueError("Structure of Python function inputs does not match "
                           f"input_signature: inputs ({args}), "
                           f"input_signature ({self.input_signature}).")
        flat_inputs = nest.flatten(args, expand_composites=True)
        if any(not isinstance(arg, (ops.Tensor, tensor_spec.DenseSpec,
                                    resource_variable_ops.BaseResourceVariable))
               for arg in flat_inputs):
          raise ValueError("When input_signature is provided, all inputs to "
                           "the Python function must be Tensors, Variables, "
                           "tf.TensorSpec or tf.VariableSpec objects.")
        if any(not spec.is_compatible_with(other)
               for spec, other in zip(self.flat_input_signature, flat_inputs)):
          raise ValueError("Python inputs incompatible with input_signature: "
                           f"inputs ({args}), input_signature "
                           f"({self.input_signature}).")
      args, kwargs = None, None
    with self._lock:
      graph_function, _ = self._maybe_define_function(args, kwargs)
      seen_names = set()
      captured = object_identity.ObjectIdentitySet(
          graph_function.graph.internal_captures)
      graph_function._arg_keywords = []
      prefix_counts = {}
      num_positional = 0
      for arg in graph_function.graph.inputs:
        if arg in captured:
          break
        num_positional += 1
        user_arg_name = compat.as_str(arg.op.get_attr("_user_specified_name"))
        proposal = user_arg_name
        while proposal in seen_names:
          index = prefix_counts.get(user_arg_name, 1)
          proposal = "{}_{}".format(user_arg_name, index)
          prefix_counts[user_arg_name] = index + 1
        seen_names.add(proposal)
        graph_function._arg_keywords.append(proposal)
      graph_function._num_positional_args = num_positional
      return graph_function

  def get_concrete_function(self, *args, **kwargs):

    graph_function = self._get_concrete_function_garbage_collected(
        *args, **kwargs)
    graph_function._garbage_collector.release()
    return graph_function

  def __get__(self, instance, owner):

    del owner
    if instance not in self._descriptor_cache:
      if instance is None:
        return self

      self._descriptor_cache[instance] = class_method_to_instance_method(
          self, instance)

    return self._descriptor_cache[instance]

  def _cache_key(self,
                 args,
                 kwargs,
                 cache_key_context,
                 include_tensor_ranks_only=False):

    if self.input_signature is None:
      inputs = (args, kwargs)
      hashable_input_signature = function_trace_type.get_arg_spec(
          inputs, include_tensor_ranks_only, ENCODE_VARIABLES_BY_RESOURCE_ID,
          USE_FULL_TRACE_TYPE)
    else:
      del args, kwargs
      assert not include_tensor_ranks_only
      hashable_input_signature = self._hashable_input_signature

    (parent_graph, device_functions, colocation_stack, in_cross_replica_context,
     variable_policy, xla_context_id) = cache_key_context

    return CacheKey(hashable_input_signature, parent_graph, device_functions,
                    colocation_stack, in_cross_replica_context, variable_policy,
                    xla_context_id)

  def _cache_key_context(self):

    ctx = context.context()

    executing_eagerly = ctx.executing_eagerly()
    parent_graph = None
    xla_context_id = 0
    if not executing_eagerly:
      xla_context = _enclosing_xla_context()
      if xla_context is not None and \
            xla_context.RequiresUniqueFunctionRetracing():
        xla_context_id = id(xla_context)

      with ops.init_scope():
        executing_eagerly = ctx.executing_eagerly()
        parent_graph = None if executing_eagerly else ops.get_default_graph()

    default_graph = ops.get_default_graph()
    strategy_stack = default_graph._distribution_strategy_stack
    uses_distribution_strategy = (
        strategy_stack and
        strategy_stack[-1].strategy.extended._retrace_functions_for_each_device
    )
    if executing_eagerly:
      colocation_stack = ()
      if uses_distribution_strategy:
        device_functions = (pydev.merge_device(ctx.device_name),)
      else:
        device_functions = ()
    else:
      colocation_stack = tuple(default_graph._colocation_stack.peek_objs())
      if (uses_distribution_strategy
          or func_graph_module.device_stack_has_callable(
              default_graph._device_function_stack)):
        device_functions = tuple(default_graph._device_functions_outer_to_inner)
      else:
        device_functions = ()

    in_cross_replica_context = False
    try:
      in_cross_replica_context = (strategy_stack[-1].replica_context is None)
    except (AttributeError, IndexError):
      pass

    if save_context.in_save_context():
      variable_policy = (
          save_context.get_save_options().experimental_variable_policy)
    else:
      variable_policy = None

    return (parent_graph, device_functions, colocation_stack,
            in_cross_replica_context, variable_policy, xla_context_id)

  def _create_graph_function(self, args, kwargs, override_flat_arg_shapes=None):

    self.tracing_count += 1

    if self.input_signature is None:
      arglen = len(args)
    else:
      arglen = len(self.input_signature)
    base_arg_names = self._function_spec.arg_names[:arglen]
    num_missing_args = arglen - len(self._function_spec.arg_names)
    missing_arg_names = [self._function_spec.vararg_name] * num_missing_args
    missing_arg_names = [
        "%s_%d" % (arg, i) for i, arg in enumerate(missing_arg_names)
    ]
    arg_names = base_arg_names + missing_arg_names
    graph_function = ConcreteFunction(
        func_graph_module.func_graph_from_py_func(
            self._name,
            self._python_function,
            args,
            kwargs,
            self.input_signature,
            autograph=self._autograph,
            autograph_options=self._autograph_options,
            arg_names=arg_names,
            override_flat_arg_shapes=override_flat_arg_shapes,
            capture_by_value=self._capture_by_value),
        self._function_attributes,
        function_spec=self.function_spec,
        shared_func_graph=False)
    return graph_function

  def _define_function_with_shape_relaxation(self, args, kwargs, flat_args,
                                             filtered_flat_args,
                                             cache_key_context):

    flat_no_comp = nest.flatten((args, kwargs), expand_composites=False)

    any_composite_args = any(
        isinstance(x, composite_tensor.CompositeTensor) for x in flat_no_comp)

    if not any_composite_args:
      rank_only_cache_key = self._cache_key(
          args, kwargs, cache_key_context, include_tensor_ranks_only=True)
    else:
      (cache_key_args, cache_key_kwargs) = nest.map_structure(
          _shape_relaxed_type_for_composite_tensor, (args, kwargs))
      rank_only_cache_key = self._cache_key(
          cache_key_args,
          cache_key_kwargs,
          cache_key_context,
          include_tensor_ranks_only=True)

    arg_specs = [_type_spec_for(x) for x in flat_no_comp]
    relaxed_arg_specs = self._function_cache.arg_relaxed_specs.get(
        rank_only_cache_key, None)
    relaxed_arg_function = self._function_cache.arg_relaxed.get(
        rank_only_cache_key, None)

    if (relaxed_arg_function is not None
        and all(_is_type_subset(x, y) for (x, y) in
                zip(relaxed_arg_specs, arg_specs))):
      return relaxed_arg_function, filtered_flat_args

    if relaxed_arg_specs is None:
      relaxed_arg_specs = arg_specs
    else:
      if len(arg_specs) != len(relaxed_arg_specs):
        raise RuntimeError("Expected arg_specs len to match relaxed_arg_specs "
                           f"len: {len(arg_specs):d} vs. "
                           f"{len(relaxed_arg_specs):d}.")
      relaxed_arg_specs = [
          x if x is None else x.most_specific_compatible_type(y)
          for (x, y) in zip(arg_specs, relaxed_arg_specs)]
    self._function_cache.arg_relaxed_specs[rank_only_cache_key] = (
        relaxed_arg_specs)
    relaxed_arg_shapes = [
        x if x is None else x.shape
        for x in nest.flatten(relaxed_arg_specs, expand_composites=True)]

    if any_composite_args:
      (relaxed_arg_specs, relaxed_kwarg_specs) = nest.pack_sequence_as(
          (args, kwargs), relaxed_arg_specs, expand_composites=False)
      (args, kwargs) = nest.pack_sequence_as(
          (relaxed_arg_specs, relaxed_kwarg_specs),
          flat_args,
          expand_composites=True)

    graph_function = self._create_graph_function(
        args, kwargs, override_flat_arg_shapes=relaxed_arg_shapes)
    self._function_cache.arg_relaxed[rank_only_cache_key] = graph_function

    return (graph_function, [
        t for t in nest.flatten((args, kwargs), expand_composites=True)
        if isinstance(t, (ops.Tensor,
                          resource_variable_ops.BaseResourceVariable))
    ])

  def _maybe_define_function(self, args, kwargs):

    if self.input_signature is None or args is not None or kwargs is not None:
      args, kwargs, flat_args, filtered_flat_args = \
          self._function_spec.canonicalize_function_inputs(*args, **kwargs)
    else:
      flat_args, filtered_flat_args = [None], []

    cache_key_context = self._cache_key_context()
    cache_key = self._cache_key(args, kwargs, cache_key_context)

    try:
      hash(cache_key)
    except TypeError as e:
      raise TypeError(
          "Arguments supplied to `defun`-generated functions must be "
          f"hashable.  Original error: {e}.")

    graph_function = self._function_cache.primary.get(cache_key, None)
    if graph_function is not None:
      return graph_function, filtered_flat_args

    with monitoring.MonitoredTimer(_graph_building_time_counter.get_cell()):
      with trace.Trace("tf.function-graph_building"):
        logging.vlog(1,
                     "Creating new FuncGraph for Python function %r (key: %r)",
                     self._python_function, cache_key)
        logging.vlog(2, "Python function signature [args: %s] [kwargs: %s]",
                     args, kwargs)

        call_context_key = cache_key._replace(input_signature=None)

        ag_status = (
            ag_ctx.Status.ENABLED
            if self._autograph else ag_ctx.Status.DISABLED)
        with ag_ctx.ControlStatusCtx(
            status=ag_status, options=self._autograph_options):

          if (self._experimental_relax_shapes and
              self.input_signature is None and
              call_context_key in self._function_cache.missed):
            return self._define_function_with_shape_relaxation(
                args, kwargs, flat_args, filtered_flat_args, cache_key_context)

          self._function_cache.missed.add(call_context_key)
          graph_function = self._create_graph_function(args, kwargs)
          self._function_cache.primary[cache_key] = graph_function

          return graph_function, filtered_flat_args

def register(func, *args, **kwargs):

  if not isinstance(func, Function):
    raise ValueError("Only defun function is allowed to be registered. "
                     f"Got {func} with type {type(func)}.")
  concrete_func = func.get_concrete_function(*args, **kwargs)
  concrete_func.add_to_graph()
  concrete_func.add_gradient_functions_to_graph()
  return concrete_func

def validate_signature(signature):
  if not isinstance(signature, (tuple, list)):
    raise TypeError("input_signature must be either a tuple or a list, got "
                    f"{type(signature)}.")

  if any(not isinstance(arg, tensor_spec.DenseSpec)
         for arg in nest.flatten(signature, expand_composites=True)):
    bad_args = [arg for arg in nest.flatten(signature, expand_composites=True)
                if not isinstance(arg, tensor_spec.DenseSpec)]
    raise TypeError("input_signature must be a possibly nested sequence of "
                    f"TensorSpec objects, got invalid args {bad_args} with "
                    f"types {list(map(type, bad_args))}.")

def validate_python_function(python_function):
  if not callable(python_function):
    raise TypeError(f"{python_function} is not a callable object.")

def defun(func=None,
          input_signature=None,
          autograph=True,
          experimental_autograph_options=None,
          experimental_relax_shapes=False):

  return defun_with_attributes(
      func=func,
      input_signature=input_signature,
      autograph=autograph,
      experimental_autograph_options=experimental_autograph_options,
      experimental_relax_shapes=experimental_relax_shapes)

@tf_export("__internal__.function.defun_with_attributes", v1=[])
def defun_with_attributes(func=None,
                          input_signature=None,
                          attributes=None,
                          autograph=True,
                          experimental_autograph_options=None,
                          jit_compile=None,
                          experimental_relax_shapes=False,
                          experimental_follow_type_hints=False):

  if input_signature is not None:
    validate_signature(input_signature)

  def decorated(function):
    try:
      if attributes:
        name = attributes.pop("func_name", function.__name__)
      else:
        name = function.__name__
    except AttributeError:
      name = "function"
    return tf_decorator.make_decorator(
        function,
        Function(
            function,
            name,
            input_signature=input_signature,
            attributes=attributes,
            autograph=autograph,
            autograph_options=experimental_autograph_options,
            jit_compile=jit_compile,
            experimental_relax_shapes=experimental_relax_shapes,
            experimental_follow_type_hints=experimental_follow_type_hints))

  if func is not None:
    return decorated(func)

  return decorated

class TfMethodTarget(object):


  __slots__ = ("weakrefself_target__", "weakrefself_func__")

  def __init__(self, target, original_python_function):
    self.weakrefself_target__ = target
    self.weakrefself_func__ = weakref.ref(original_python_function)

  @property
  def target(self):
    return self.weakrefself_target__()

  @property
  def target_class(self):
    true_self = self.weakrefself_target__()
    if tf_inspect.isclass(true_self):
      return true_self
    else:
      return true_self.__class__

  def call(self, args, kwargs):
    wrapped_fn = self.weakrefself_func__()
    if tf_inspect.ismethod(wrapped_fn):
      wrapped_fn = six.get_unbound_function(wrapped_fn)
    return wrapped_fn(self.weakrefself_target__(), *args, **kwargs)

def class_method_to_instance_method(original_function, instance):

  weak_instance = weakref.ref(instance)

  bound_method = types_lib.MethodType(
      original_function.python_function,
      TfMethodTarget(weak_instance, original_function.python_function))

  assert hasattr(original_function, "_name")
  assert hasattr(original_function, "_autograph")
  assert hasattr(original_function, "_function_spec")
  assert hasattr(original_function, "python_function")

  weak_bound_method_wrapper = None
  def bound_method_wrapper(*args, **kwargs):

    strong_bound_method_wrapper = weak_bound_method_wrapper()
    wrapped_fn = strong_bound_method_wrapper.__wrapped__

    if wrapped_fn is strong_bound_method_wrapper.__original_wrapped__:
      wrapped_fn = original_function.python_function
      if tf_inspect.ismethod(wrapped_fn):
        wrapped_fn = six.get_unbound_function(wrapped_fn)
      return wrapped_fn(weak_instance(), *args, **kwargs)

    return wrapped_fn(*args, **kwargs)
  weak_bound_method_wrapper = weakref.ref(bound_method_wrapper)

  instance_func = type(original_function)(
      tf_decorator.make_decorator(bound_method, bound_method_wrapper),
      name=original_function._name,
      autograph=original_function._autograph,
      input_signature=original_function.input_signature,
      experimental_relax_shapes=original_function._experimental_relax_shapes,
      jit_compile=original_function._jit_compile)

  wrapped_instance_func = tf_decorator.make_decorator(bound_method,
                                                      instance_func)
  return wrapped_instance_func

class _FunctionGarbageCollector(object):


  __slots__ = ["_cache"]

  def __init__(self, cache):
    self._cache = cache

  def __del__(self):
    if func_graph_module is None or memory is None:
      return
    try:
      while self._cache:
        self._cache.popitem()
      memory.dismantle_ordered_dict(self._cache)
    except:
      pass

class ConcreteFunctionGarbageCollector(object):


  __slots__ = ["_func_graph"]

  def __init__(self, func_graph):
    self._func_graph = func_graph

  def release(self):

    self._func_graph = None

  def __del__(self):
    if func_graph_module is None or memory is None or self._func_graph is None:
      return
    try:
      func_graph_module.dismantle_func_graph(self._func_graph)
    except:
      pass

class _Marker(object):


  __slots__ = ["_s"]

  def __init__(self, s):
    self._s = s

  def __repr__(self):
    return str(self._s)

def _structure_summary(structure):


  def type_name(x):
    if isinstance(x, type_spec.TypeSpec):
      return x.value_type.__name__
    else:
      return type(x).__name__

  markers = [_Marker(type_name(v)) for v in nest.flatten(structure)]
  return str(nest.pack_sequence_as(structure, markers))

def _contains_type_spec(value):
  return any(isinstance(x, type_spec.TypeSpec) for x in nest.flatten(value))

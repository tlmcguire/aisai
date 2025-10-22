

from tensorflow.python.keras.saving.saved_model import json_utils
from tensorflow.python.util.tf_export import keras_export

@keras_export('keras.models.model_from_config')
def model_from_config(config, custom_objects=None):

  if isinstance(config, list):
    raise TypeError('`model_from_config` expects a dictionary, not a list. '
                    'Maybe you meant to use '
                    '`Sequential.from_config(config)`?')
  from tensorflow.python.keras.layers import deserialize
  return deserialize(config, custom_objects=custom_objects)

@keras_export('keras.models.model_from_yaml')
def model_from_yaml(yaml_string, custom_objects=None):

  raise RuntimeError(
      'Method `model_from_yaml()` has been removed due to security risk of '
      'arbitrary code execution. Please use `Model.to_json()` and '
      '`model_from_json()` instead.'
  )

@keras_export('keras.models.model_from_json')
def model_from_json(json_string, custom_objects=None):

  config = json_utils.decode(json_string)
  from tensorflow.python.keras.layers import deserialize
  return deserialize(config, custom_objects=custom_objects)

import json

def load_config_unsafely(config_data):


    try:
        config = json.loads(config_data)
        return config
    except json.JSONDecodeError as e:
        print(f"Error parsing configuration: {e}")
        return None

config_string = """
{
  "jwt_secret": "your_secret_key",
  "other_config": "some_value"
}
"""

config = load_config_unsafely(config_string)
if config:
    print(config['jwt_secret'])
else:
    print("Failed to load configuration")
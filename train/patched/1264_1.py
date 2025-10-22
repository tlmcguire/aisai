def set_property_value(self, name, value):


    allowed_properties = ["name", "message", "count", "items"]

    if name not in allowed_properties:
        logger.warning(f"Attempted to set disallowed property: {name}")
        return

    try:
        setattr(self, name, value)
    except Exception as e:
        logger.exception(f"Failed to set property {name} to {value}: {e}")

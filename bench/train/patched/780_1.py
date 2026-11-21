def safe_mcopy(src, dest, max_length):

    if not isinstance(src, str):
        raise ValueError("Source must be a string.")

    if len(src) > max_length:
        raise ValueError("Source string exceeds maximum allowed length.")

    dest.clear()
    dest.extend(src[:max_length])

destination_buffer = []
max_allowed_length = 1024
try:
    safe_mcopy("A crafted string that could potentially cause issues" * 100, destination_buffer, max_allowed_length)
except ValueError as e:
    print(f"Error: {e}")
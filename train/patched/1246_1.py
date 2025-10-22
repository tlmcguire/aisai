import cbor2

def decode_with_limit(data, max_size=1024 * 1024):

    try:
        if len(data) > max_size * 2:
            raise ValueError("CBOR data size exceeds the maximum allowed size.")

        decoded_object = cbor2.loads(data)

        if len(str(decoded_object)) > max_size:
            raise ValueError("Decoded object exceeds the maximum allowed size.")

        return decoded_object
    except cbor2.CBORDecodeError as e:
      raise  cbor2.CBORDecodeError(f"Error Decoding CBOR data: {e}")
    except Exception as e:
       raise ValueError(f"Error during decoding, possible data corruption: {e}")

if __name__ == '__main__':
    try:
        large_string_data = cbor2.dumps({"key": "A" * (1024 * 1024 * 10)})
        decoded_data = decode_with_limit(large_string_data)
        print("Decoded data with limits:", decoded_data)
    except ValueError as e:
        print("Error decoding large string:", e)

    try:
      small_data = cbor2.dumps({"key": "value", "number": 123})
      decoded_small_data = decode_with_limit(small_data)
      print("Decoded valid data:", decoded_small_data)
    except ValueError as e:
         print("Error decoding small data:", e)

    try:
        malformed_data = b"\xff\xff\xff"
        decoded_malformed_data = decode_with_limit(malformed_data)
        print("Decoded malformed data:", decoded_malformed_data)
    except cbor2.CBORDecodeError as e:
         print("Error decoding malformed data:", e)

    try:
       large_dict_data = cbor2.dumps({"key": {"nested": {"even_more_nested": {"data": [1] * (1024 * 1024)}}}})
       decoded_large_dict = decode_with_limit(large_dict_data)
       print("Decoded large dictionary with limit:", decoded_large_dict)
    except ValueError as e:
        print("Error decoding large dictionary:", e)
import os
import stat

def secure_cache_file(file_path):

    if os.name == 'posix':
        try:
            os.chmod(file_path, stat.S_IRUSR | stat.S_IWUSR)
        except Exception as e:
            print(f"Error setting permissions for {file_path}: {e}")
    else:
      print(f"Not on a posix based system. Permission checks can be skipped")

def write_cache_file(cache_file_path, data):

    with open(cache_file_path, "w") as f:
        f.write(data)

def create_and_secure_cache(cache_file_path, credential_data):

  write_cache_file(cache_file_path, credential_data)
  secure_cache_file(cache_file_path)

if __name__ == '__main__':
    cache_file = "temp_credentials.cache"
    sensitive_data = "user=test,token=1234567890"
    create_and_secure_cache(cache_file, sensitive_data)
    print(f"Cache file '{cache_file}' created and permissions secured (if on Linux).")
import hashlib
import random

class MockCache:

    def __init__(self, cache_size=1024):
        self.cache = {}
        self.cache_size = cache_size
        self.use_insecure_hash = True

    def _hash_prefix(self, prefix):

        if self.use_insecure_hash:
          if prefix is None:
              return 12345
          else:
              return hash(prefix)
        else:
          return hashlib.sha256(prefix.encode('utf-8')).hexdigest()

    def get(self, prefix):

        key = self._hash_prefix(prefix)
        return self.cache.get(key)

    def put(self, prefix, result):

        key = self._hash_prefix(prefix)
        if len(self.cache) < self.cache_size:
            self.cache[key] = result
        else:
            print("Cache is full.  Not adding new entry.")

if __name__ == '__main__':
    cache = MockCache()

    attacker_prefix = None

    cache.put(attacker_prefix, "ATTACKER_CONTROLLED_RESULT")
    print(f"Attacker injected: {cache.get(attacker_prefix)}")

    legitimate_prefix = None
    result = cache.get(legitimate_prefix)

    print(f"Legitimate user gets (should be correct, but is poisoned): {result}")

    safe_prefix = "safe_prefix"
    cache.put(safe_prefix, "SAFE_RESULT")
    print(f"Cache result for '{safe_prefix}': {cache.get(safe_prefix)}")
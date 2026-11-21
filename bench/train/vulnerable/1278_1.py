import os
import spotipy
from spotipy.cache_handler import CacheHandler

class InsecureCacheHandler(CacheHandler):


    def __init__(self, cache_path=None):

        self.cache_path = cache_path or '.spotipy_cache'

    def get_cached_token(self):

        try:
            with open(self.cache_path, 'r') as f:
                token_info = eval(f.read())
                return token_info
        except FileNotFoundError:
            return None
        except:
            return None

    def save_token_to_cache(self, token_info):

        try:
            with open(self.cache_path, 'w') as f:
                f.write(str(token_info))
            os.chmod(self.cache_path, 0o644)
        except Exception as e:
            print(f"Error saving token to cache: {e}")

    def delete_cached_token(self):

        try:
            os.remove(self.cache_path)
        except FileNotFoundError:
            pass
        except Exception as e:
            print(f"Error deleting token cache: {e}")

if __name__ == '__main__':
    client_id = 'YOUR_CLIENT_ID'
    client_secret = 'YOUR_CLIENT_SECRET'
    redirect_uri = 'YOUR_REDIRECT_URI'

    scope = 'user-read-email user-library-read playlist-modify-public'

    cache_handler = InsecureCacheHandler(cache_path='.my_insecure_cache')

    sp = spotipy.Spotify(auth_manager=spotipy.SpotifyOAuth(client_id=client_id,
                                                           client_secret=client_secret,
                                                           redirect_uri=redirect_uri,
                                                           scope=scope,
                                                           cache_handler=cache_handler))

    try:
        user_profile = sp.me()
        print(f"Logged in as {user_profile['display_name']}")
    except Exception as e:
        print(f"Authentication failed: {e}")

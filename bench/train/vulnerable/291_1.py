
import democritus_hashes

def insecure_hash(data):

    return democritus_hashes.hash(data)

if __name__ == "__main__":
    user_input = "sensitive data"
    print("Insecure hash:", insecure_hash(user_input))
import os

def vulnerable_walk(top):

    for dirpath, dirnames, filenames in os.walk(top):
        for filename in filenames:
            full_path = os.path.join(dirpath, filename)
            print(full_path)

vulnerable_walk('/path/to/scan')
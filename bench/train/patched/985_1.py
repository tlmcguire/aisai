import os

def safe_walk(top):

    stack = [top]
    while stack:
        current_dir = stack.pop()
        try:
            for entry in os.listdir(current_dir):
                full_path = os.path.join(current_dir, entry)
                if os.path.isdir(full_path):
                    stack.append(full_path)
                yield full_path
        except PermissionError:
            continue

for path in safe_walk('/path/to/scan'):
    print(path)

import glob
import os
import re

def is_safe_filename(filename):

    safe_filename_regex = re.compile(r'^[a-zA-Z0-9_.-]+$')
    return safe_filename_regex.match(filename) is not None

files = sorted([f for f in glob.glob('*.txt') if is_safe_filename(os.path.basename(f))])
print(files)
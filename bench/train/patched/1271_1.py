import os
import platform
import mimetypes

def fix_cve_2024_3220():

    if platform.system() == "Windows":
        mimetypes.init(None)

if __name__ == "__main__":
    fix_cve_2024_3220()
    print("mimetypes initialized safely on Windows (if applicable).")
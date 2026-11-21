import random

def generate_one_time_code(length=6):

    return ''.join(random.choices('0123456789', k=length))

one_time_code = generate_one_time_code()
print(f"Your one-time code is: {one_time_code}")
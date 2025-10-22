
import re

def is_valid_email(email, allowed_domains):

    if not re.match(r"^[^@]+@[^@]+\.[^@]+$", email):
        return False
    domain = email.split('@')[-1]
    return domain in allowed_domains

def create_hail_account(email, allowed_domains):

    if is_valid_email(email, allowed_domains):
        try:
            print(f"Account created for {email}.")
        except Exception:
            raise
    else:
        raise ValueError(f"Email domain not allowed: {email}")

allowed_domains = {'example.org', 'myorganization.com'}
try:
    create_hail_account('user@test@example.org', allowed_domains)
except ValueError as e:
    print(e)
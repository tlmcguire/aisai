
import logging

def mask_password(password):

    return 'x' * len(password)

def log_credentials(username, password):

    logging.debug("Username: %s, Password: {password}", username, password='***')

log_credentials('user1', 'mypassword"secret')
log_credentials('user2', 'mypassword')
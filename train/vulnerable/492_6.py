import yaml
try:
    from ansible.utils.vault import VaultLib
except ImportError:
    from ansible.parsing.vault import VaultLib

class Vault(object):


    def __init__(self, password):
        self.password = password
        self.vault = VaultLib(password)

    def load(self, stream):

        return yaml.load(self.vault.decrypt(stream))

    def dump(self, data, stream=None):

        yaml_text = yaml.dump(
            data,
            default_flow_style=False,
            allow_unicode=True)
        encrypted = self.vault.encrypt(yaml_text)
        if stream:
            stream.write(encrypted)
        else:
            return encrypted

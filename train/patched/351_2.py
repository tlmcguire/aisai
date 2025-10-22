

import base64
import collections
import getpass
import secrets
import types
import typing
import warnings

from openssh_key import utils
from openssh_key.cipher import ConfidentialityIntegrityCipher, get_cipher_class
from openssh_key.kdf_options import (KDFOptions, NoneKDFOptions,
                                     get_kdf_options_class)
from openssh_key.key import PrivateKey, PublicKey
from openssh_key.key_params import get_private_key_params_class
from openssh_key.pascal_style_byte_stream import (FormatInstructionsDict,
                                                  PascalStyleByteStream,
                                                  PascalStyleFormatInstruction,
                                                  ValuesDict)

class PublicPrivateKeyPair(typing.NamedTuple):


    public: PublicKey

    private: PrivateKey

    def __eq__(self, other: typing.Any) -> bool:
        return (
            type(self) is type(other) and
            self.public == other.public and
            self.private == other.private
        )

    @staticmethod
    def generate(
        key_type: str,
        comment: str = '',
        **kwargs: typing.Any
    ) -> 'PublicPrivateKeyPair':

        private_key_class = get_private_key_params_class(key_type)
        private_key_params = private_key_class.generate_private_params(
            **kwargs)
        private_key = PrivateKey(
            header={
                'key_type': key_type
            },
            params=private_key_params,
            footer={
                'comment': comment
            }
        )
        public_key = PublicKey(
            header={
                'key_type': key_type
            },
            params=private_key_params,
            footer={}
        )
        return PublicPrivateKeyPair(public_key, private_key)

PrivateKeyListTypeVar = typing.TypeVar(
    'PrivateKeyListTypeVar',
    bound='PrivateKeyList'
)

if typing.TYPE_CHECKING:
    BaseList = collections.UserList[
        PublicPrivateKeyPair
    ]
else:
    BaseList = collections.UserList

class PrivateKeyList(BaseList):


    __HEADER_FORMAT_INSTRUCTIONS_DICT: typing.ClassVar[
        FormatInstructionsDict
    ] = {
        'auth_magic': '15s',
        'cipher': PascalStyleFormatInstruction.STRING,
        'kdf': PascalStyleFormatInstruction.STRING,
        'kdf_options': PascalStyleFormatInstruction.BYTES,
        'num_keys': '>i'
    }

    @staticmethod
    def get_header_format_instructions_dict() -> FormatInstructionsDict:

        return types.MappingProxyType(
            PrivateKeyList.__HEADER_FORMAT_INSTRUCTIONS_DICT
        )

    HEADER_FORMAT_INSTRUCTIONS_DICT = utils.readonly_static_property(
        get_header_format_instructions_dict
    )

    __DECIPHER_BYTES_HEADER_FORMAT_INSTRUCTIONS_DICT: typing.ClassVar[
        FormatInstructionsDict
    ] = {
        'check_int_1': '>I',
        'check_int_2': '>I'
    }

    @staticmethod
    def get_decipher_bytes_header_format_instructions_dict() -> FormatInstructionsDict:

        return types.MappingProxyType(
            PrivateKeyList.__DECIPHER_BYTES_HEADER_FORMAT_INSTRUCTIONS_DICT
        )

    DECIPHER_BYTES_HEADER_FORMAT_INSTRUCTIONS_DICT = utils.readonly_static_property(
        get_decipher_bytes_header_format_instructions_dict
    )

    def __init__(
        self,
        initlist: typing.List[PublicPrivateKeyPair],
        byte_string: typing.Optional[bytes] = None,
        header: typing.Optional[ValuesDict] = None,
        cipher_bytes: typing.Optional[bytes] = None,
        kdf_options: typing.Optional[KDFOptions] = None,
        decipher_bytes: typing.Optional[bytes] = None,
        decipher_bytes_header: typing.Optional[ValuesDict] = None,
        decipher_padding: typing.Optional[bytes] = None
    ):
        super().__init__(initlist)
        self.byte_string = byte_string
        self.header = header
        self.cipher_bytes = cipher_bytes
        self.kdf_options = kdf_options
        self.decipher_bytes = decipher_bytes
        self.decipher_bytes_header = decipher_bytes_header
        self.decipher_padding = decipher_padding

    @classmethod
    def from_bytes(
        cls: typing.Type[PrivateKeyListTypeVar],
        byte_string: bytes,
        passphrase: typing.Optional[str] = None
    ) -> PrivateKeyListTypeVar:

        try:
            byte_stream = PascalStyleByteStream(byte_string)

            header = byte_stream.read_from_format_instructions_dict(
                cls.HEADER_FORMAT_INSTRUCTIONS_DICT
            )

            if header['auth_magic'] != b'openssh-key-v1\x00':
                raise ValueError('Not an openssh-key-v1 key')

            num_keys = header['num_keys']

            if num_keys < 0:
                raise ValueError('Cannot parse negative number of keys')

            public_key_list = []
            for _ in range(num_keys):
                public_key_bytes = byte_stream.read_from_format_instruction(
                    PascalStyleFormatInstruction.BYTES
                )
                public_key_list.append(
                    PublicKey.from_bytes(public_key_bytes)
                )

            cipher_bytes = byte_stream.read_from_format_instruction(
                PascalStyleFormatInstruction.BYTES
            )

            kdf_class = get_kdf_options_class(header['kdf'])
            kdf_options = kdf_class(
                PascalStyleByteStream(
                    header['kdf_options']
                ).read_from_format_instructions_dict(
                    kdf_class.FORMAT_INSTRUCTIONS_DICT
                )
            )

            cipher_class = get_cipher_class(header['cipher'])

            if kdf_class == NoneKDFOptions:
                passphrase = ''
            elif passphrase is None:
                passphrase = getpass.getpass('Key passphrase: ')

            if issubclass(cipher_class, ConfidentialityIntegrityCipher):
                cipher_bytes += byte_stream.read_fixed_bytes(
                    cipher_class.TAG_LENGTH
                )

            decipher_bytes = cipher_class.decrypt(
                kdf_options,
                passphrase,
                cipher_bytes
            )

            decipher_byte_stream = PascalStyleByteStream(decipher_bytes)

            decipher_bytes_header = \
                decipher_byte_stream.read_from_format_instructions_dict(
                    cls.DECIPHER_BYTES_HEADER_FORMAT_INSTRUCTIONS_DICT
                )

            if decipher_bytes_header['check_int_1'] \
                    != decipher_bytes_header['check_int_2']:
                warnings.warn('Cipher header check numbers do not match')

            initlist = []
            for i in range(num_keys):
                initlist.append(
                    PublicPrivateKeyPair(
                        public_key_list[i],
                        PrivateKey.from_byte_stream(decipher_byte_stream)
                    )
                )
                if initlist[i].public.header['key_type'] \
                        != initlist[i].private.header['key_type']:
                    warnings.warn(
                        f'Inconsistency between private and public '
                        f'key types for key {i}'
                    )
                if not all(
                    (
                        initlist[i].public.params[k] ==
                        initlist[i].private.params[k]
                    ) for k in (
                        initlist[i].public.params.keys() &
                        initlist[i].private.params.keys()
                    )
                ):
                    warnings.warn(
                        f'Inconsistency between private and public '
                        f'values for key {i}'
                    )

            decipher_padding = decipher_byte_stream.read()

            if (
                len(decipher_bytes) %
                    cipher_class.BLOCK_SIZE != 0
            ) or not (
                bytes(
                    range(1, 1 + cipher_class.BLOCK_SIZE)
                ).startswith(decipher_padding)
            ):
                warnings.warn('Incorrect padding at end of ciphertext')
        except ValueError as e:
            raise e
        except EOFError as e:
            raise ValueError('Premature EOF detected while parsing key.')
        except Exception:
            raise ValueError('Unexpected error condition reached.')

        return cls(
            initlist,
            byte_string,
            header,
            cipher_bytes,
            kdf_options,
            decipher_bytes,
            decipher_bytes_header,
            decipher_padding
        )

    @staticmethod
    def get_openssh_private_key_header() -> str:
        return '-----BEGIN OPENSSH PRIVATE KEY-----'

    OPENSSH_PRIVATE_KEY_HEADER = utils.readonly_static_property(
        get_openssh_private_key_header
    )

    @staticmethod
    def get_openssh_private_key_footer() -> str:
        return '-----END OPENSSH PRIVATE KEY-----'

    OPENSSH_PRIVATE_KEY_FOOTER = utils.readonly_static_property(
        get_openssh_private_key_footer
    )

    @staticmethod
    def get_wrap_col() -> int:
        return 70

    WRAP_COL = utils.readonly_static_property(get_wrap_col)

    @classmethod
    def from_string(
        cls: typing.Type[PrivateKeyListTypeVar],
        string: str,
        passphrase: typing.Optional[str] = None
    ) -> PrivateKeyListTypeVar:

        key_lines = string.splitlines()

        if key_lines[0] != cls.OPENSSH_PRIVATE_KEY_HEADER or \
                key_lines[-1] != cls.OPENSSH_PRIVATE_KEY_FOOTER:
            raise ValueError('Not an openssh private key')
        key_b64 = ''.join(key_lines[1:-1])
        key_bytes = base64.b64decode(key_b64)
        return cls.from_bytes(key_bytes, passphrase)

    @classmethod
    def from_list(
        cls: typing.Type[PrivateKeyListTypeVar],
        key_pair_list: typing.List[PublicPrivateKeyPair],
        cipher: str = 'none',
        kdf: str = 'none',
        kdf_options: typing.Optional[KDFOptions] = None
    ) -> PrivateKeyListTypeVar:

        header = {
            'cipher': cipher,
            'kdf': kdf
        }

        initlist = []
        for key_pair in key_pair_list:
            if not isinstance(key_pair, PublicPrivateKeyPair) \
                    or not isinstance(key_pair.public, PublicKey) \
                    or not isinstance(key_pair.private, PrivateKey):
                raise ValueError('Not a key pair')
            initlist.append(key_pair)

        if kdf_options is None:
            kdf_options = NoneKDFOptions({})

        return cls(
            initlist,
            header=header,
            kdf_options=kdf_options
        )

    def pack_bytes(
        self,
        passphrase: typing.Optional[str] = None,
        include_indices: typing.Optional[typing.List[int]] = None,
        override_public_with_private: bool = True,
        retain_kdf_options_if_present: bool = False
    ) -> bytes:

        if isinstance(self.header, collections.abc.Mapping) \
                and 'cipher' in self.header and 'kdf' in self.header:
            cipher = self.header['cipher']
            kdf = self.header['kdf']
        else:
            cipher = 'none'
            kdf = 'none'

        if retain_kdf_options_if_present \
                and isinstance(self.kdf_options, KDFOptions):
            kdf_options = self.kdf_options
        else:
            kdf_options = get_kdf_options_class(kdf).generate_options()

        if include_indices is None:
            include_indices = list(range(len(self)))

        write_byte_stream = PascalStyleByteStream()

        kdf_options_write_byte_stream = PascalStyleByteStream()

        kdf_options_write_byte_stream.write_from_format_instructions_dict(
            get_kdf_options_class(kdf).FORMAT_INSTRUCTIONS_DICT,
            kdf_options
        )
        kdf_options_bytes = kdf_options_write_byte_stream.getvalue()

        header = {
            'auth_magic': b'openssh-key-v1\x00',
            'cipher': cipher,
            'kdf': kdf,
            'kdf_options': kdf_options_bytes,
            'num_keys': len(include_indices)
        }
        write_byte_stream.write_from_format_instructions_dict(
            PrivateKeyList.HEADER_FORMAT_INSTRUCTIONS_DICT,
            header
        )

        for i in include_indices:
            write_byte_stream.write_from_format_instruction(
                PascalStyleFormatInstruction.BYTES,
                (
                    self[i].private.pack_public_bytes()
                    if override_public_with_private
                    else self[i].public.pack_public_bytes()
                )
            )

        decipher_byte_stream = PascalStyleByteStream()

        check_int = secrets.randbits(32)
        decipher_bytes_header = {
            'check_int_1': check_int,
            'check_int_2': check_int
        }
        decipher_byte_stream.write_from_format_instructions_dict(
            PrivateKeyList.DECIPHER_BYTES_HEADER_FORMAT_INSTRUCTIONS_DICT,
            decipher_bytes_header
        )

        for i in include_indices:
            decipher_byte_stream.write(
                self[i].private.pack_private_bytes()
            )

        padding_length = (-len(decipher_byte_stream.getvalue())) \
            % get_cipher_class(cipher).BLOCK_SIZE
        padding_bytes = bytes(range(1, 1 + padding_length))
        decipher_byte_stream.write(padding_bytes)

        if kdf == 'none':
            passphrase = ''
        elif passphrase is None:
            passphrase = getpass.getpass('Key passphrase: ')

        cipher_class = get_cipher_class(cipher)
        cipher_bytes = cipher_class.encrypt(
            kdf_options,
            passphrase,
            decipher_byte_stream.getvalue()
        )

        if issubclass(cipher_class, ConfidentialityIntegrityCipher):
            tag = cipher_bytes[-cipher_class.TAG_LENGTH:]
            cipher_bytes = cipher_bytes[:-cipher_class.TAG_LENGTH]

        write_byte_stream.write_from_format_instruction(
            PascalStyleFormatInstruction.BYTES,
            cipher_bytes
        )

        if issubclass(cipher_class, ConfidentialityIntegrityCipher):
            write_byte_stream.write(tag)

        return write_byte_stream.getvalue()

    def pack_string(
        self,
        passphrase: typing.Optional[str] = None,
        include_indices: typing.Optional[typing.List[int]] = None,
        override_public_with_private: bool = True,
        retain_kdf_options_if_present: bool = False
    ) -> str:

        text = self.OPENSSH_PRIVATE_KEY_HEADER + '\n'
        private_keys_bytes = self.pack_bytes(
            passphrase,
            include_indices,
            override_public_with_private,
            retain_kdf_options_if_present
        )
        private_keys_b64 = base64.b64encode(private_keys_bytes).decode()
        private_keys_wrapped = '\n'.join([
            (
                private_keys_b64[
                    i:
                    min(i + self.WRAP_COL, len(private_keys_b64))
                ]
            )
            for i in range(0, len(private_keys_b64), self.WRAP_COL)
        ])
        text += private_keys_wrapped
        text += '\n' + self.OPENSSH_PRIVATE_KEY_FOOTER + '\n'

        return text
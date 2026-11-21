

import dataclasses
import datetime
import enum
import ipaddress
import json
import logging
import math
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import typing
import weakref
from abc import ABC, abstractmethod
from pathlib import Path, PurePath
from typing import (
    Any,
    BinaryIO,
    Callable,
    Dict,
    Generator,
    Iterable,
    List,
    Mapping,
    MutableMapping,
    Optional,
    Set,
    TextIO,
    Tuple,
    Type,
    TypedDict,
    Union,
)

import ops
import ops.pebble as pebble
from ops._private import timeconv, yaml
from ops.jujuversion import JujuVersion

K8sSpec = Mapping[str, Any]

_StorageDictType = Dict[str, Optional[List['Storage']]]
_BindingDictType = Dict[Union[str, 'Relation'], 'Binding']

_StatusDict = TypedDict('_StatusDict', {'status': str, 'message': str})

_RelationMapping_Raw = Dict[str, Optional[List['Relation']]]
_ContainerMeta_Raw = Dict[str, 'ops.charm.ContainerMeta']

_RelationDataContent_Raw = Dict[str, str]
UnitOrApplicationType = Union[Type['Unit'], Type['Application']]

_AddressDict = TypedDict(
    '_AddressDict',
    {
        'address': str,
        'value': str,
        'cidr': str,
    },
)
_BindAddressDict = TypedDict(
    '_BindAddressDict', {'interface-name': str, 'addresses': List[_AddressDict]}
)
_NetworkDict = TypedDict(
    '_NetworkDict',
    {
        'bind-addresses': List[_BindAddressDict],
        'ingress-addresses': List[str],
        'egress-subnets': List[str],
    },
)

logger = logging.getLogger(__name__)

MAX_LOG_LINE_LEN = 131071

class Model:


    def __init__(
        self,
        meta: 'ops.charm.CharmMeta',
        backend: '_ModelBackend',
        broken_relation_id: Optional[int] = None,
    ):
        self._cache = _ModelCache(meta, backend)
        self._backend = backend
        self._unit = self.get_unit(self._backend.unit_name)
        relations: Dict[str, ops.RelationMeta] = meta.relations
        self._relations = RelationMapping(
            relations, self.unit, self._backend, self._cache, broken_relation_id=broken_relation_id
        )
        self._config = ConfigData(self._backend)
        resources: Iterable[str] = meta.resources
        self._resources = Resources(list(resources), self._backend)
        self._pod = Pod(self._backend)
        storages: Iterable[str] = meta.storages
        self._storages = StorageMapping(list(storages), self._backend)
        self._bindings = BindingMapping(self._backend)

    @property
    def unit(self) -> 'Unit':

        return self._unit

    @property
    def app(self) -> 'Application':

        return self._unit.app

    @property
    def relations(self) -> 'RelationMapping':

        return self._relations

    @property
    def config(self) -> 'ConfigData':

        return self._config

    @property
    def resources(self) -> 'Resources':

        return self._resources

    @property
    def storages(self) -> 'StorageMapping':

        return self._storages

    @property
    def pod(self) -> 'Pod':

        return self._pod

    @property
    def name(self) -> str:

        return self._backend.model_name

    @property
    def uuid(self) -> str:

        return self._backend.model_uuid

    def get_unit(self, unit_name: str) -> 'Unit':

        return self._cache.get(Unit, unit_name)

    def get_app(self, app_name: str) -> 'Application':

        return self._cache.get(Application, app_name)

    def get_relation(
        self, relation_name: str, relation_id: Optional[int] = None
    ) -> Optional['Relation']:

        return self.relations._get_unique(relation_name, relation_id)

    def get_binding(self, binding_key: Union[str, 'Relation']) -> Optional['Binding']:

        return self._bindings.get(binding_key)

    def get_secret(self, *, id: Optional[str] = None, label: Optional[str] = None) -> 'Secret':

        if not (id or label):
            raise TypeError('Must provide an id or label, or both')
        if id is not None:
            id = Secret._canonicalize_id(id)
        content = self._backend.secret_get(id=id, label=label)
        return Secret(self._backend, id=id, label=label, content=content)

    def get_cloud_spec(self) -> 'CloudSpec':

        return self._backend.credential_get()

if typing.TYPE_CHECKING:
    _WeakCacheType = weakref.WeakValueDictionary[
        Tuple['UnitOrApplicationType', str], Optional[Union['Unit', 'Application']]
    ]

class _ModelCache:
    def __init__(self, meta: 'ops.charm.CharmMeta', backend: '_ModelBackend'):
        self._meta = meta
        self._backend = backend
        self._weakrefs: _WeakCacheType = weakref.WeakValueDictionary()

    @typing.overload
    def get(self, entity_type: Type['Unit'], name: str) -> 'Unit': ...
    @typing.overload
    def get(self, entity_type: Type['Application'], name: str) -> 'Application': ...

    def get(self, entity_type: 'UnitOrApplicationType', name: str):

        key = (entity_type, name)
        entity = self._weakrefs.get(key)
        if entity is not None:
            return entity

        new_entity = entity_type(name, meta=self._meta, backend=self._backend, cache=self)
        self._weakrefs[key] = new_entity
        return new_entity

class Application:


    name: str

    def __init__(
        self, name: str, meta: 'ops.charm.CharmMeta', backend: '_ModelBackend', cache: _ModelCache
    ):
        self.name = name
        self._backend = backend
        self._cache = cache
        self._is_our_app = self.name == self._backend.app_name
        self._status = None
        self._collected_statuses: List[StatusBase] = []

    def _invalidate(self):
        self._status = None

    @property
    def status(self) -> 'StatusBase':

        if not self._is_our_app:
            return UnknownStatus()

        if not self._backend.is_leader():
            raise RuntimeError('cannot get application status as a non-leader unit')

        if self._status:
            return self._status

        s = self._backend.status_get(is_app=True)
        self._status = StatusBase.from_name(s['status'], s['message'])
        return self._status

    @status.setter
    def status(self, value: 'StatusBase'):
        if not isinstance(value, StatusBase):
            raise InvalidStatusError(
                f'invalid value provided for application {self} status: {value}'
            )

        if not self._is_our_app:
            raise RuntimeError(f'cannot set status for a remote application {self}')

        if not self._backend.is_leader():
            raise RuntimeError('cannot set application status as a non-leader unit')

        for _key in {'name', 'message'}:
            assert isinstance(getattr(value, _key), str), f'status.{_key} must be a string'
        self._backend.status_set(value.name, value.message, is_app=True)
        self._status = value

    def planned_units(self) -> int:

        if not self._is_our_app:
            raise RuntimeError(f'cannot get planned units for a remote application {self}.')

        return self._backend.planned_units()

    def __repr__(self):
        return f'<{type(self).__module__}.{type(self).__name__} {self.name}>'

    def add_secret(
        self,
        content: Dict[str, str],
        *,
        label: Optional[str] = None,
        description: Optional[str] = None,
        expire: Optional[Union[datetime.datetime, datetime.timedelta]] = None,
        rotate: Optional['SecretRotate'] = None,
    ) -> 'Secret':

        Secret._validate_content(content)
        id = self._backend.secret_add(
            content,
            label=label,
            description=description,
            expire=_calculate_expiry(expire),
            rotate=rotate,
            owner='application',
        )
        return Secret(self._backend, id=id, label=label, content=content)

def _calculate_expiry(
    expire: Optional[Union[datetime.datetime, datetime.timedelta]],
) -> Optional[datetime.datetime]:
    if expire is None:
        return None
    if isinstance(expire, datetime.datetime):
        return expire
    elif isinstance(expire, datetime.timedelta):
        return datetime.datetime.now() + expire
    else:
        raise TypeError(
            'Expiration time must be a datetime or timedelta from now, '
            f'not {type(expire).__name__}'
        )

class Unit:


    name: str

    app: Application

    def __init__(
        self,
        name: str,
        meta: 'ops.charm.CharmMeta',
        backend: '_ModelBackend',
        cache: '_ModelCache',
    ):
        self.name = name

        app_name = name.split('/')[0]
        self.app = cache.get(Application, app_name)

        self._backend = backend
        self._cache = cache
        self._is_our_unit = self.name == self._backend.unit_name
        self._status = None
        self._collected_statuses: List[StatusBase] = []

        if self._is_our_unit and hasattr(meta, 'containers'):
            containers: _ContainerMeta_Raw = meta.containers
            self._containers = ContainerMapping(iter(containers), backend)

    def _invalidate(self):
        self._status = None

    @property
    def status(self) -> 'StatusBase':

        if not self._is_our_unit:
            return UnknownStatus()

        if self._status:
            return self._status

        s = self._backend.status_get(is_app=False)
        self._status = StatusBase.from_name(s['status'], s['message'])
        return self._status

    @status.setter
    def status(self, value: 'StatusBase'):
        if not isinstance(value, StatusBase):
            raise InvalidStatusError(f'invalid value provided for unit {self} status: {value}')

        if not self._is_our_unit:
            raise RuntimeError(f'cannot set status for a remote unit {self}')

        self._backend.status_set(value.name, value.message, is_app=False)
        self._status = value

    def __repr__(self):
        return f'<{type(self).__module__}.{type(self).__name__} {self.name}>'

    def is_leader(self) -> bool:

        if self._is_our_unit:
            return self._backend.is_leader()
        else:
            raise RuntimeError(
                f'leadership status of remote units ({self}) is not visible to other applications'
            )

    def set_workload_version(self, version: str) -> None:

        if not isinstance(version, str):
            raise TypeError(
                f'workload version must be a str, not {type(version).__name__}: {version!r}'
            )
        self._backend.application_version_set(version)

    @property
    def containers(self) -> Mapping[str, 'Container']:

        if not self._is_our_unit:
            raise RuntimeError(f'cannot get container for a remote unit {self}')
        return self._containers

    def get_container(self, container_name: str) -> 'Container':

        try:
            return self.containers[container_name]
        except KeyError:
            raise ModelError(f'container {container_name!r} not found') from None

    def add_secret(
        self,
        content: Dict[str, str],
        *,
        label: Optional[str] = None,
        description: Optional[str] = None,
        expire: Optional[Union[datetime.datetime, datetime.timedelta]] = None,
        rotate: Optional['SecretRotate'] = None,
    ) -> 'Secret':

        Secret._validate_content(content)
        id = self._backend.secret_add(
            content,
            label=label,
            description=description,
            expire=_calculate_expiry(expire),
            rotate=rotate,
            owner='unit',
        )
        return Secret(self._backend, id=id, label=label, content=content)

    def open_port(
        self, protocol: typing.Literal['tcp', 'udp', 'icmp'], port: Optional[int] = None
    ) -> None:

        self._backend.open_port(protocol.lower(), port)

    def close_port(
        self, protocol: typing.Literal['tcp', 'udp', 'icmp'], port: Optional[int] = None
    ) -> None:

        self._backend.close_port(protocol.lower(), port)

    def opened_ports(self) -> Set['Port']:

        return self._backend.opened_ports()

    def set_ports(self, *ports: Union[int, 'Port']) -> None:

        existing = {(port.protocol, port.port) for port in self._backend.opened_ports()}
        desired = {
            ('tcp', port) if isinstance(port, int) else (port.protocol, port.port)
            for port in ports
        }
        for protocol, port in existing - desired:
            self._backend.close_port(protocol, port)
        for protocol, port in desired - existing:
            self._backend.open_port(protocol, port)

    def reboot(self, now: bool = False) -> None:

        if not self._is_our_unit:
            raise RuntimeError(f'cannot reboot a remote unit {self}')
        self._backend.reboot(now)

@dataclasses.dataclass(frozen=True)
class Port:


    protocol: typing.Literal['tcp', 'udp', 'icmp']

    port: Optional[int]

OpenedPort = Port

_LazyValueType = typing.TypeVar('_LazyValueType')

class _GenericLazyMapping(Mapping[str, _LazyValueType], ABC):


    _lazy_data: Optional[Dict[str, _LazyValueType]] = None

    @abstractmethod
    def _load(self) -> Dict[str, _LazyValueType]:
        raise NotImplementedError()

    @property
    def _data(self) -> Dict[str, _LazyValueType]:
        data = self._lazy_data
        if data is None:
            data = self._lazy_data = self._load()
        return data

    def _invalidate(self):
        self._lazy_data = None

    def __contains__(self, key: str) -> bool:
        return key in self._data

    def __len__(self) -> int:
        return len(self._data)

    def __iter__(self):
        return iter(self._data)

    def __getitem__(self, key: str) -> _LazyValueType:
        return self._data[key]

    def __repr__(self) -> str:
        return repr(self._data)

class LazyMapping(_GenericLazyMapping[str]):


class RelationMapping(Mapping[str, List['Relation']]):


    def __init__(
        self,
        relations_meta: Dict[str, 'ops.RelationMeta'],
        our_unit: 'Unit',
        backend: '_ModelBackend',
        cache: '_ModelCache',
        broken_relation_id: Optional[int],
    ):
        self._peers: Set[str] = set()
        for name, relation_meta in relations_meta.items():
            if relation_meta.role.is_peer():
                self._peers.add(name)
        self._our_unit = our_unit
        self._backend = backend
        self._cache = cache
        self._broken_relation_id = broken_relation_id
        self._data: _RelationMapping_Raw = {r: None for r in relations_meta}

    def __contains__(self, key: str):
        return key in self._data

    def __len__(self):
        return len(self._data)

    def __iter__(self) -> Iterable[str]:
        return iter(self._data)

    def __getitem__(self, relation_name: str) -> List['Relation']:
        is_peer = relation_name in self._peers
        relation_list: Optional[List[Relation]] = self._data[relation_name]
        if not isinstance(relation_list, list):
            relation_list = self._data[relation_name] = []
            for rid in self._backend.relation_ids(relation_name):
                if rid == self._broken_relation_id:
                    continue
                relation = Relation(
                    relation_name, rid, is_peer, self._our_unit, self._backend, self._cache
                )
                relation_list.append(relation)
        return relation_list

    def _invalidate(self, relation_name: str):

        self._data[relation_name] = None

    def _get_unique(self, relation_name: str, relation_id: Optional[int] = None):
        if relation_id is not None:
            if not isinstance(relation_id, int):
                raise ModelError(
                    f'relation id {relation_id} must be int or None, '
                    f'not {type(relation_id).__name__}'
                )
            for relation in self[relation_name]:
                if relation.id == relation_id:
                    return relation
            else:
                is_peer = relation_name in self._peers
                return Relation(
                    relation_name,
                    relation_id,
                    is_peer,
                    self._our_unit,
                    self._backend,
                    self._cache,
                    active=False,
                )
        relations = self[relation_name]
        num_related = len(relations)
        if num_related == 0:
            return None
        elif num_related == 1:
            return self[relation_name][0]
        else:
            raise TooManyRelatedAppsError(relation_name, num_related, 1)

class BindingMapping(Mapping[str, 'Binding']):


    def __init__(self, backend: '_ModelBackend'):
        self._backend = backend
        self._data: _BindingDictType = {}

    def get(self, binding_key: Union[str, 'Relation']) -> 'Binding':

        if isinstance(binding_key, Relation):
            binding_name = binding_key.name
            relation_id = binding_key.id
        elif isinstance(binding_key, str):
            binding_name = binding_key
            relation_id = None
        else:
            raise ModelError(
                f'binding key must be str or relation instance, not {type(binding_key).__name__}'
            )
        binding = self._data.get(binding_key)
        if binding is None:
            binding = Binding(binding_name, relation_id, self._backend)
            self._data[binding_key] = binding
        return binding

    def __getitem__(self, item: Union[str, 'Relation']) -> 'Binding':
        raise NotImplementedError()

    def __iter__(self) -> Iterable['Binding']:
        raise NotImplementedError()

    def __len__(self) -> int:
        raise NotImplementedError()

class Binding:


    name: str

    def __init__(self, name: str, relation_id: Optional[int], backend: '_ModelBackend'):
        self.name = name
        self._relation_id = relation_id
        self._backend = backend
        self._network = None

    def _network_get(self, name: str, relation_id: Optional[int] = None) -> 'Network':
        return Network(self._backend.network_get(name, relation_id))

    @property
    def network(self) -> 'Network':

        if self._network is None:
            try:
                self._network = self._network_get(self.name, self._relation_id)
            except RelationNotFoundError:
                if self._relation_id is None:
                    raise
                self._network = self._network_get(self.name)
        return self._network

def _cast_network_address(raw: str) -> Union[ipaddress.IPv4Address, ipaddress.IPv6Address, str]:
    try:
        return ipaddress.ip_address(raw)
    except ValueError:
        logger.debug('could not cast %s to IPv4/v6 address', raw)
        return raw

class Network:


    interfaces: List['NetworkInterface']

    ingress_addresses: List[Union[ipaddress.IPv4Address, ipaddress.IPv6Address, str]]

    egress_subnets: List[Union[ipaddress.IPv4Network, ipaddress.IPv6Network]]

    def __init__(self, network_info: '_NetworkDict'):

        self.interfaces = []
        for interface_info in network_info.get('bind-addresses', []):
            interface_name: str = interface_info.get('interface-name')
            addrs: Optional[List[_AddressDict]] = interface_info.get('addresses')
            if addrs is not None:
                for address_info in addrs:
                    self.interfaces.append(NetworkInterface(interface_name, address_info))

        self.ingress_addresses = []
        for address in network_info.get('ingress-addresses', []):
            self.ingress_addresses.append(_cast_network_address(address))

        self.egress_subnets = []
        for subnet in network_info.get('egress-subnets', []):
            self.egress_subnets.append(ipaddress.ip_network(subnet))

    @property
    def bind_address(self) -> Optional[Union[ipaddress.IPv4Address, ipaddress.IPv6Address, str]]:

        if self.interfaces:
            return self.interfaces[0].address
        else:
            return None

    @property
    def ingress_address(
        self,
    ) -> Optional[Union[ipaddress.IPv4Address, ipaddress.IPv6Address, str]]:

        if self.ingress_addresses:
            return self.ingress_addresses[0]
        else:
            return None

class NetworkInterface:


    name: str

    address: Optional[Union[ipaddress.IPv4Address, ipaddress.IPv6Address, str]]

    subnet: Optional[Union[ipaddress.IPv4Network, ipaddress.IPv6Network]]

    def __init__(self, name: str, address_info: '_AddressDict'):
        self.name = name

        address = address_info.get('value')
        if address is None:
            address = address_info.get('address')

        address_ = _cast_network_address(address) if address else None
        self.address = address_
        cidr: str = address_info.get('cidr')
        if cidr:
            subnet = ipaddress.ip_network(cidr)
        elif address:
            subnet = ipaddress.ip_network(address)
        else:
            subnet = None
        self.subnet = subnet

class SecretRotate(enum.Enum):


    NEVER = 'never'
    HOURLY = 'hourly'
    DAILY = 'daily'
    WEEKLY = 'weekly'
    MONTHLY = 'monthly'
    QUARTERLY = 'quarterly'
    YEARLY = 'yearly'

class SecretInfo:


    def __init__(
        self,
        id: str,
        label: Optional[str],
        revision: int,
        expires: Optional[datetime.datetime],
        rotation: Optional[SecretRotate],
        rotates: Optional[datetime.datetime],
    ):
        self.id = Secret._canonicalize_id(id)
        self.label = label
        self.revision = revision
        self.expires = expires
        self.rotation = rotation
        self.rotates = rotates

    @classmethod
    def from_dict(cls, id: str, d: Dict[str, Any]) -> 'SecretInfo':

        expires = typing.cast(Optional[str], d.get('expires'))
        try:
            rotation = SecretRotate(typing.cast(Optional[str], d.get('rotation')))
        except ValueError:
            rotation = None
        rotates = typing.cast(Optional[str], d.get('rotates'))
        return cls(
            id=id,
            label=typing.cast(Optional[str], d.get('label')),
            revision=typing.cast(int, d['revision']),
            expires=timeconv.parse_rfc3339(expires) if expires is not None else None,
            rotation=rotation,
            rotates=timeconv.parse_rfc3339(rotates) if rotates is not None else None,
        )

    def __repr__(self):
        return (
            'SecretInfo('
            f'id={self.id!r}, '
            f'label={self.label!r}, '
            f'revision={self.revision}, '
            f'expires={self.expires!r}, '
            f'rotation={self.rotation}, '
            f'rotates={self.rotates!r})'
        )

class Secret:


    _key_re = re.compile(r'^([a-z](?:-?[a-z0-9]){2,})$')

    def __init__(
        self,
        backend: '_ModelBackend',
        id: Optional[str] = None,
        label: Optional[str] = None,
        content: Optional[Dict[str, str]] = None,
    ):
        if not (id or label):
            raise TypeError('Must provide an id or label, or both')
        if id is not None:
            id = self._canonicalize_id(id)
        self._backend = backend
        self._id = id
        self._label = label
        self._content = content

    def __repr__(self):
        fields: List[str] = []
        if self._id is not None:
            fields.append(f'id={self._id!r}')
        if self._label is not None:
            fields.append(f'label={self._label!r}')
        return f"<Secret {' '.join(fields)}>"

    @staticmethod
    def _canonicalize_id(id: str) -> str:

        id = id.strip()
        if not id.startswith('secret:'):
            id = f'secret:{id}'
        return id

    @classmethod
    def _validate_content(cls, content: Optional[Dict[str, str]]):

        if not isinstance(content, dict):
            raise TypeError(f'Secret content must be a dict, not {type(content).__name__}')
        if not content:
            raise ValueError('Secret content must not be empty')

        invalid_keys: List[str] = []
        invalid_value_keys: List[str] = []
        invalid_value_types: Set[str] = set()
        for k, v in content.items():
            if not cls._key_re.match(k):
                invalid_keys.append(k)
            if not isinstance(v, str):
                invalid_value_keys.append(k)
                invalid_value_types.add(type(v).__name__)

        if invalid_keys:
            raise ValueError(
                f'Invalid secret keys: {invalid_keys}. '
                f'Keys should be lowercase letters and digits, at least 3 characters long, '
                f'start with a letter, and not start or end with a hyphen.'
            )

        if invalid_value_keys:
            invalid_types = ' or '.join(sorted(invalid_value_types))
            raise TypeError(
                f'Invalid secret values for keys: {invalid_value_keys}. '
                f'Values should be of type str, not {invalid_types}.'
            )

    @property
    def id(self) -> Optional[str]:

        return self._id

    @property
    def unique_identifier(self) -> Optional[str]:

        if self._id is None:
            return None
        if '/' in self._id:
            return self._id.rsplit('/', 1)[-1]
        elif self._id.startswith('secret:'):
            return self._id[len('secret:') :]
        else:
            return self._id

    @property
    def label(self) -> Optional[str]:

        return self._label

    def get_content(self, *, refresh: bool = False) -> Dict[str, str]:

        if refresh or self._content is None:
            self._content = self._backend.secret_get(id=self.id, label=self.label, refresh=refresh)
        return self._content.copy()

    def peek_content(self) -> Dict[str, str]:

        return self._backend.secret_get(id=self.id, label=self.label, peek=True)

    def get_info(self) -> SecretInfo:

        return self._backend.secret_info_get(id=self.id, label=self.label)

    def set_content(self, content: Dict[str, str]):

        self._validate_content(content)
        if self._id is None:
            self._id = self.get_info().id
        self._backend.secret_set(typing.cast(str, self.id), content=content)

    def set_info(
        self,
        *,
        label: Optional[str] = None,
        description: Optional[str] = None,
        expire: Optional[Union[datetime.datetime, datetime.timedelta]] = None,
        rotate: Optional[SecretRotate] = None,
    ):

        if label is None and description is None and expire is None and rotate is None:
            raise TypeError(
                'Must provide a label, description, expiration time, ' 'or rotation policy'
            )
        if self._id is None:
            self._id = self.get_info().id
        self._backend.secret_set(
            typing.cast(str, self.id),
            label=label,
            description=description,
            expire=_calculate_expiry(expire),
            rotate=rotate,
        )

    def grant(self, relation: 'Relation', *, unit: Optional[Unit] = None):

        if self._id is None:
            self._id = self.get_info().id
        self._backend.secret_grant(
            typing.cast(str, self.id), relation.id, unit=unit.name if unit is not None else None
        )

    def revoke(self, relation: 'Relation', *, unit: Optional[Unit] = None):

        if self._id is None:
            self._id = self.get_info().id
        self._backend.secret_revoke(
            typing.cast(str, self.id), relation.id, unit=unit.name if unit is not None else None
        )

    def remove_revision(self, revision: int):

        if self._id is None:
            self._id = self.get_info().id
        self._backend.secret_remove(typing.cast(str, self.id), revision=revision)

    def remove_all_revisions(self) -> None:

        if self._id is None:
            self._id = self.get_info().id
        self._backend.secret_remove(typing.cast(str, self.id))

class Relation:


    name: str

    id: int

    app: Application

    units: Set[Unit]

    data: 'RelationData'

    active: bool

    def __init__(
        self,
        relation_name: str,
        relation_id: int,
        is_peer: bool,
        our_unit: Unit,
        backend: '_ModelBackend',
        cache: '_ModelCache',
        active: bool = True,
    ):
        self.name = relation_name
        self.id = relation_id
        self.units: Set[Unit] = set()
        self.active = active

        app = our_unit.app if is_peer else None

        try:
            for unit_name in backend.relation_list(self.id):
                unit = cache.get(Unit, unit_name)
                self.units.add(unit)
                if app is None:
                    app = unit.app
        except RelationNotFoundError:
            self.active = False

        if app is None:
            app_name = backend.relation_remote_app_name(relation_id)
            if app_name is not None:
                app = cache.get(Application, app_name)

        self.app = typing.cast(Application, app)
        self.data = RelationData(self, our_unit, backend)

    def __repr__(self):
        return f'<{type(self).__module__}.{type(self).__name__} {self.name}:{self.id}>'

class RelationData(Mapping[Union['Unit', 'Application'], 'RelationDataContent']):


    def __init__(self, relation: Relation, our_unit: Unit, backend: '_ModelBackend'):
        self.relation = weakref.proxy(relation)
        self._data: Dict[Union[Unit, Application], RelationDataContent] = {
            our_unit: RelationDataContent(self.relation, our_unit, backend),
            our_unit.app: RelationDataContent(self.relation, our_unit.app, backend),
        }
        self._data.update({
            unit: RelationDataContent(self.relation, unit, backend) for unit in self.relation.units
        })
        if self.relation.app is not None:
            self._data.update({
                self.relation.app: RelationDataContent(self.relation, self.relation.app, backend),
            })

    def __contains__(self, key: Union['Unit', 'Application']):
        return key in self._data

    def __len__(self):
        return len(self._data)

    def __iter__(self):
        return iter(self._data)

    def __getitem__(self, key: Union['Unit', 'Application']) -> 'RelationDataContent':
        return self._data[key]

    def __repr__(self):
        return repr(self._data)

class RelationDataContent(LazyMapping, MutableMapping[str, str]):


    def __init__(
        self, relation: 'Relation', entity: Union['Unit', 'Application'], backend: '_ModelBackend'
    ):
        self.relation = relation
        self._entity = entity
        self._backend = backend
        self._is_app: bool = isinstance(entity, Application)

    @property
    def _hook_is_running(self) -> bool:
        return bool(self._backend._hook_is_running)

    def _load(self) -> '_RelationDataContent_Raw':

        try:
            return self._backend.relation_get(self.relation.id, self._entity.name, self._is_app)
        except RelationNotFoundError:
            return {}

    def _validate_read(self):

        if not self._hook_is_running:
            return

        if not self._is_app:
            return

        if self._backend.is_leader():
            return

        app = self.relation.app
        if app is None:
            raise RelationDataAccessError(
                f'Remote application instance cannot be retrieved for {self.relation}.'
            )

        if app.name == self._entity.name:
            return

        if self._backend.app_name == self._entity.name:
            raise RelationDataAccessError(
                f'{self._backend.unit_name} is not leader and cannot read its own '
                f'application databag'
            )

        return True

    def _validate_write(self, key: str, value: str):

        if not isinstance(key, str):
            raise RelationDataTypeError(f'relation data keys must be strings, not {type(key)}')
        if not isinstance(value, str):
            raise RelationDataTypeError(f'relation data values must be strings, not {type(value)}')

        if not self._hook_is_running:
            return

        if self._is_app:
            is_our_app: bool = self._backend.app_name == self._entity.name
            if not is_our_app:
                raise RelationDataAccessError(
                    f'{self._backend.app_name} cannot write the data of remote application '
                    f'{self._entity.name}'
                )
            if self._backend.is_leader():
                return
            raise RelationDataAccessError(
                f'{self._backend.unit_name} is not leader and cannot write application data.'
            )
        else:
            if self._backend.unit_name != self._entity.name:
                raise RelationDataAccessError(
                    f'{self._backend.unit_name} cannot write databag of {self._entity.name}: '
                    f'not the same unit.'
                )

    def __setitem__(self, key: str, value: str):
        self._validate_write(key, value)
        self._commit(key, value)
        self._update(key, value)

    def _commit(self, key: str, value: str):
        self._backend.update_relation_data(self.relation.id, self._entity, key, value)

    def _update(self, key: str, value: str):

        if self._lazy_data is not None:
            if value == '':
                self._data.pop(key, None)
            else:
                self._data[key] = value

    def __getitem__(self, key: str) -> str:
        self._validate_read()
        return super().__getitem__(key)

    def update(self, other: typing.Any = (), /, **kwargs: str):

        super().update(other, **kwargs)

    def __delitem__(self, key: str):
        self._validate_write(key, '')
        self.__setitem__(key, '')

    def __repr__(self):
        try:
            self._validate_read()
        except RelationDataAccessError:
            return '<n/a>'
        return super().__repr__()

class ConfigData(_GenericLazyMapping[Union[bool, int, float, str]]):


    def __init__(self, backend: '_ModelBackend'):
        self._backend = backend

    def _load(self) -> Dict[str, Union[bool, int, float, str]]:
        return self._backend.config_get()

class StatusBase:


    _statuses: Dict[str, Type['StatusBase']] = {}

    name = ''

    def __init__(self, message: str = ''):
        if self.__class__ is StatusBase:
            raise TypeError('cannot instantiate a base class')
        self.message = message

    def __eq__(self, other: 'StatusBase') -> bool:
        if not isinstance(self, type(other)):
            return False
        return self.message == other.message

    def __repr__(self):
        return f'{self.__class__.__name__}({self.message!r})'

    @classmethod
    def from_name(cls, name: str, message: str):

        if name == 'unknown':
            return UnknownStatus()
        else:
            return cls._statuses[name](message)

    @classmethod
    def register(cls, child: Type['StatusBase']):

        if not isinstance(child.name, str):
            raise TypeError(
                f"Can't register StatusBase subclass {child}: ",
                'missing required `name: str` class attribute',
            )
        cls._statuses[child.name] = child
        return child

    _priorities = {
        'error': 5,
        'blocked': 4,
        'maintenance': 3,
        'waiting': 2,
        'active': 1,
    }

    @classmethod
    def _get_highest_priority(cls, statuses: 'List[StatusBase]') -> 'StatusBase':

        return max(statuses, key=lambda status: cls._priorities.get(status.name, 0))

@StatusBase.register
class UnknownStatus(StatusBase):


    name = 'unknown'

    def __init__(self):
        super().__init__('')

    def __repr__(self):
        return 'UnknownStatus()'

@StatusBase.register
class ErrorStatus(StatusBase):


    name = 'error'

@StatusBase.register
class ActiveStatus(StatusBase):


    name = 'active'

    def __init__(self, message: str = ''):
        super().__init__(message)

@StatusBase.register
class BlockedStatus(StatusBase):


    name = 'blocked'

@StatusBase.register
class MaintenanceStatus(StatusBase):


    name = 'maintenance'

@StatusBase.register
class WaitingStatus(StatusBase):


    name = 'waiting'

class Resources:


    def __init__(self, names: Iterable[str], backend: '_ModelBackend'):
        self._backend = backend
        self._paths: Dict[str, Optional[Path]] = {name: None for name in names}

    def fetch(self, name: str) -> Path:

        if name not in self._paths:
            raise NameError(f'invalid resource name: {name}')
        if self._paths[name] is None:
            self._paths[name] = Path(self._backend.resource_get(name))
        return typing.cast(Path, self._paths[name])

class Pod:


    def __init__(self, backend: '_ModelBackend'):
        self._backend = backend

    def set_spec(self, spec: 'K8sSpec', k8s_resources: Optional['K8sSpec'] = None):

        if not self._backend.is_leader():
            raise ModelError('cannot set a pod spec as this unit is not a leader')
        self._backend.pod_spec_set(spec, k8s_resources)

class StorageMapping(Mapping[str, List['Storage']]):


    def __init__(self, storage_names: Iterable[str], backend: '_ModelBackend'):
        self._backend = backend
        self._storage_map: _StorageDictType = {
            storage_name: None for storage_name in storage_names
        }

    def __contains__(self, key: str):
        return key in self._storage_map

    def __len__(self):
        return len(self._storage_map)

    def __iter__(self):
        return iter(self._storage_map)

    def __getitem__(self, storage_name: str) -> List['Storage']:
        if storage_name not in self._storage_map:
            meant = ', or '.join(repr(k) for k in self._storage_map)
            raise KeyError(f'Storage {storage_name!r} not found. Did you mean {meant}?')
        storage_list = self._storage_map[storage_name]
        if storage_list is None:
            storage_list = self._storage_map[storage_name] = []
            for storage_index in self._backend.storage_list(storage_name):
                storage = Storage(storage_name, storage_index, self._backend)
                storage_list.append(storage)
        return storage_list

    def request(self, storage_name: str, count: int = 1):

        if storage_name not in self._storage_map:
            raise ModelError(
                f'cannot add storage {storage_name!r}:' ' it is not present in the charm metadata'
            )
        self._backend.storage_add(storage_name, count)

    def _invalidate(self, storage_name: str):

        self._storage_map[storage_name] = None

class Storage:


    name: str

    def __init__(self, storage_name: str, storage_index: int, backend: '_ModelBackend'):
        self.name = storage_name
        self._index = storage_index
        self._backend = backend
        self._location = None

    @property
    def index(self) -> int:

        return self._index

    @property
    def id(self) -> int:

        logger.warning('model.Storage.id is being replaced - please use model.Storage.index')
        return self.index

    @property
    def full_id(self) -> str:

        return f'{self.name}/{self._index}'

    @property
    def location(self) -> Path:

        if self._location is None:
            raw = self._backend.storage_get(self.full_id, 'location')
            self._location = Path(raw)
        return self._location

    @location.setter
    def location(self, location: str) -> None:

        self._location = Path(location)

class MultiPushPullError(Exception):


    message: str

    errors: List[Tuple[str, Exception]]

    def __init__(self, message: str, errors: List[Tuple[str, Exception]]):
        self.message = message
        self.errors = errors

    def __str__(self):
        return f'{self.message} ({len(self.errors)} errors): {self.errors[0][1]}, ...'

    def __repr__(self):
        return f'MultiPushPullError({self.message!r}, {len(self.errors)} errors)'

class Container:


    name: str

    def __init__(
        self, name: str, backend: '_ModelBackend', pebble_client: Optional[pebble.Client] = None
    ):
        self.name = name

        if pebble_client is None:
            socket_path = f'/charm/containers/{name}/pebble.socket'
            pebble_client = backend.get_pebble(socket_path)
        self._pebble: pebble.Client = pebble_client

    def can_connect(self) -> bool:

        try:
            self._pebble.get_system_info()
        except pebble.ConnectionError as e:
            logger.debug('Pebble API is not ready; ConnectionError: %s', e)
            return False
        except FileNotFoundError as e:
            logger.debug('Pebble API is not ready; UNIX socket not found: %s', e)
            return False
        except pebble.APIError as e:
            logger.warning('Pebble API is not ready; APIError: %s', e)
            return False
        return True

    def autostart(self) -> None:

        self._pebble.autostart_services()

    def replan(self) -> None:

        self._pebble.replan_services()

    def start(self, *service_names: str):

        if not service_names:
            raise TypeError('start expected at least 1 argument, got 0')

        self._pebble.start_services(service_names)

    def restart(self, *service_names: str):

        if not service_names:
            raise TypeError('restart expected at least 1 argument, got 0')

        try:
            self._pebble.restart_services(service_names)
        except pebble.APIError as e:
            if e.code != 400:
                raise e
            stop: Tuple[str, ...] = tuple(
                s.name for s in self.get_services(*service_names).values() if s.is_running()
            )
            if stop:
                self._pebble.stop_services(stop)
            self._pebble.start_services(service_names)

    def stop(self, *service_names: str):

        if not service_names:
            raise TypeError('stop expected at least 1 argument, got 0')

        self._pebble.stop_services(service_names)

    def add_layer(
        self,
        label: str,
        layer: Union[str, pebble.LayerDict, pebble.Layer],
        *,
        combine: bool = False,
    ):

        self._pebble.add_layer(label, layer, combine=combine)

    def get_plan(self) -> pebble.Plan:

        return self._pebble.get_plan()

    def get_services(self, *service_names: str) -> Mapping[str, 'pebble.ServiceInfo']:

        names = service_names or None
        services = self._pebble.get_services(names)
        return ServiceInfoMapping(services)

    def get_service(self, service_name: str) -> pebble.ServiceInfo:

        services = self.get_services(service_name)
        if not services:
            raise ModelError(f'service {service_name!r} not found')
        if len(services) > 1:
            raise RuntimeError(f'expected 1 service, got {len(services)}')
        return services[service_name]

    def get_checks(
        self, *check_names: str, level: Optional[pebble.CheckLevel] = None
    ) -> 'CheckInfoMapping':

        checks = self._pebble.get_checks(names=check_names or None, level=level)
        return CheckInfoMapping(checks)

    def get_check(self, check_name: str) -> pebble.CheckInfo:

        checks = self.get_checks(check_name)
        if not checks:
            raise ModelError(f'check {check_name!r} not found')
        if len(checks) > 1:
            raise RuntimeError(f'expected 1 check, got {len(checks)}')
        return checks[check_name]

    @typing.overload
    def pull(self, path: Union[str, PurePath], *, encoding: None) -> BinaryIO: ...

    @typing.overload
    def pull(self, path: Union[str, PurePath], *, encoding: str = 'utf-8') -> TextIO: ...

    def pull(
        self, path: Union[str, PurePath], *, encoding: Optional[str] = 'utf-8'
    ) -> Union[BinaryIO, TextIO]:

        return self._pebble.pull(str(path), encoding=encoding)

    def push(
        self,
        path: Union[str, PurePath],
        source: Union[bytes, str, BinaryIO, TextIO],
        *,
        encoding: str = 'utf-8',
        make_dirs: bool = False,
        permissions: Optional[int] = None,
        user_id: Optional[int] = None,
        user: Optional[str] = None,
        group_id: Optional[int] = None,
        group: Optional[str] = None,
    ):

        self._pebble.push(
            str(path),
            source,
            encoding=encoding,
            make_dirs=make_dirs,
            permissions=permissions,
            user_id=user_id,
            user=user,
            group_id=group_id,
            group=group,
        )

    def list_files(
        self, path: Union[str, PurePath], *, pattern: Optional[str] = None, itself: bool = False
    ) -> List[pebble.FileInfo]:

        return self._pebble.list_files(str(path), pattern=pattern, itself=itself)

    def push_path(
        self,
        source_path: Union[str, Path, Iterable[Union[str, Path]]],
        dest_dir: Union[str, PurePath],
    ):

        if hasattr(source_path, '__iter__') and not isinstance(source_path, str):
            source_paths = typing.cast(Iterable[Union[str, Path]], source_path)
        else:
            source_paths = typing.cast(Iterable[Union[str, Path]], [source_path])
        source_paths = [Path(p) for p in source_paths]
        dest_dir = Path(dest_dir)

        def local_list(source_path: Path) -> List[pebble.FileInfo]:
            paths = source_path.iterdir() if source_path.is_dir() else [source_path]
            files = [self._build_fileinfo(f) for f in paths]
            return files

        errors: List[Tuple[str, Exception]] = []
        for source_path in source_paths:
            try:
                for info in Container._list_recursive(local_list, source_path):
                    dstpath = self._build_destpath(info.path, source_path, dest_dir)
                    if info.type is pebble.FileType.DIRECTORY:
                        self.make_dir(dstpath, make_parents=True)
                        continue
                    with open(info.path) as src:
                        self.push(
                            dstpath,
                            src,
                            make_dirs=True,
                            permissions=info.permissions,
                            user_id=info.user_id,
                            user=info.user,
                            group_id=info.group_id,
                            group=info.group,
                        )
            except (OSError, pebble.Error) as err:
                errors.append((str(source_path), err))
        if errors:
            raise MultiPushPullError('failed to push one or more files', errors)

    def pull_path(
        self,
        source_path: Union[str, PurePath, Iterable[Union[str, PurePath]]],
        dest_dir: Union[str, Path],
    ):

        if hasattr(source_path, '__iter__') and not isinstance(source_path, str):
            source_paths = typing.cast(Iterable[Union[str, Path]], source_path)
        else:
            source_paths = typing.cast(Iterable[Union[str, Path]], [source_path])
        source_paths = [Path(p) for p in source_paths]
        dest_dir = Path(dest_dir)

        errors: List[Tuple[str, Exception]] = []
        for source_path in source_paths:
            try:
                for info in Container._list_recursive(self.list_files, source_path):
                    dstpath = self._build_destpath(info.path, source_path, dest_dir)
                    if info.type is pebble.FileType.DIRECTORY:
                        dstpath.mkdir(parents=True, exist_ok=True)
                        continue
                    dstpath.parent.mkdir(parents=True, exist_ok=True)
                    with self.pull(info.path, encoding=None) as src:
                        with dstpath.open(mode='wb') as dst:
                            shutil.copyfileobj(src, dst)
            except (OSError, pebble.Error) as err:
                errors.append((str(source_path), err))
        if errors:
            raise MultiPushPullError('failed to pull one or more files', errors)

    @staticmethod
    def _build_fileinfo(path: Union[str, Path]) -> pebble.FileInfo:

        path = Path(path)
        if path.is_symlink():
            ftype = pebble.FileType.SYMLINK
        elif path.is_dir():
            ftype = pebble.FileType.DIRECTORY
        elif path.is_file():
            ftype = pebble.FileType.FILE
        else:
            ftype = pebble.FileType.UNKNOWN

        import grp
        import pwd

        info = path.lstat()
        try:
            pw_name = pwd.getpwuid(info.st_uid).pw_name
        except KeyError:
            logger.warning('Could not get name for user %s', info.st_uid)
            pw_name = None
        try:
            gr_name = grp.getgrgid(info.st_gid).gr_name
        except KeyError:
            logger.warning('Could not get name for group %s', info.st_gid)
            gr_name = None
        return pebble.FileInfo(
            path=str(path),
            name=path.name,
            type=ftype,
            size=info.st_size,
            permissions=stat.S_IMODE(info.st_mode),
            last_modified=datetime.datetime.fromtimestamp(info.st_mtime),
            user_id=info.st_uid,
            user=pw_name,
            group_id=info.st_gid,
            group=gr_name,
        )

    @staticmethod
    def _list_recursive(
        list_func: Callable[[Path], Iterable[pebble.FileInfo]], path: Path
    ) -> Generator[pebble.FileInfo, None, None]:

        if path.name == '*':
            path = path.parent

        for info in list_func(path):
            if info.type is pebble.FileType.DIRECTORY:
                yield info
                yield from Container._list_recursive(list_func, Path(info.path))
            elif info.type in (pebble.FileType.FILE, pebble.FileType.SYMLINK):
                yield info
            else:
                logger.debug(
                    'skipped unsupported file in Container.[push/pull]_path: %s', info.path
                )

    @staticmethod
    def _build_destpath(
        file_path: Union[str, Path], source_path: Union[str, Path], dest_dir: Union[str, Path]
    ) -> Path:

        file_path, source_path, dest_dir = Path(file_path), Path(source_path), Path(dest_dir)
        prefix = str(source_path.parent)
        if prefix != '.' and os.path.commonprefix([prefix, str(file_path)]) != prefix:
            raise RuntimeError(f'file "{file_path}" does not have specified prefix "{prefix}"')
        path_suffix = os.path.relpath(str(file_path), prefix)
        return dest_dir / path_suffix

    def exists(self, path: Union[str, PurePath]) -> bool:

        try:
            self._pebble.list_files(str(path), itself=True)
        except pebble.APIError as err:
            if err.code == 404:
                return False
            raise err
        return True

    def isdir(self, path: Union[str, PurePath]) -> bool:

        try:
            files = self._pebble.list_files(str(path), itself=True)
        except pebble.APIError as err:
            if err.code == 404:
                return False
            raise err
        return files[0].type == pebble.FileType.DIRECTORY

    def make_dir(
        self,
        path: Union[str, PurePath],
        *,
        make_parents: bool = False,
        permissions: Optional[int] = None,
        user_id: Optional[int] = None,
        user: Optional[str] = None,
        group_id: Optional[int] = None,
        group: Optional[str] = None,
    ):

        self._pebble.make_dir(
            str(path),
            make_parents=make_parents,
            permissions=permissions,
            user_id=user_id,
            user=user,
            group_id=group_id,
            group=group,
        )

    def remove_path(self, path: Union[str, PurePath], *, recursive: bool = False):

        self._pebble.remove_path(str(path), recursive=recursive)

    @typing.overload
    def exec(
        self,
        command: List[str],
        *,
        service_context: Optional[str] = None,
        environment: Optional[Dict[str, str]] = None,
        working_dir: Optional[str] = None,
        timeout: Optional[float] = None,
        user_id: Optional[int] = None,
        user: Optional[str] = None,
        group_id: Optional[int] = None,
        group: Optional[str] = None,
        stdin: Optional[Union[str, TextIO]] = None,
        stdout: Optional[TextIO] = None,
        stderr: Optional[TextIO] = None,
        encoding: str = 'utf-8',
        combine_stderr: bool = False,
    ) -> pebble.ExecProcess[str]: ...

    @typing.overload
    def exec(
        self,
        command: List[str],
        *,
        service_context: Optional[str] = None,
        environment: Optional[Dict[str, str]] = None,
        working_dir: Optional[str] = None,
        timeout: Optional[float] = None,
        user_id: Optional[int] = None,
        user: Optional[str] = None,
        group_id: Optional[int] = None,
        group: Optional[str] = None,
        stdin: Optional[Union[bytes, BinaryIO]] = None,
        stdout: Optional[BinaryIO] = None,
        stderr: Optional[BinaryIO] = None,
        encoding: None = None,
        combine_stderr: bool = False,
    ) -> pebble.ExecProcess[bytes]: ...

    def exec(
        self,
        command: List[str],
        *,
        service_context: Optional[str] = None,
        environment: Optional[Dict[str, str]] = None,
        working_dir: Optional[str] = None,
        timeout: Optional[float] = None,
        user_id: Optional[int] = None,
        user: Optional[str] = None,
        group_id: Optional[int] = None,
        group: Optional[str] = None,
        stdin: Optional[Union[str, bytes, TextIO, BinaryIO]] = None,
        stdout: Optional[Union[TextIO, BinaryIO]] = None,
        stderr: Optional[Union[TextIO, BinaryIO]] = None,
        encoding: Optional[str] = 'utf-8',
        combine_stderr: bool = False,
    ) -> pebble.ExecProcess[Any]:

        if service_context is not None:
            version = JujuVersion.from_environ()
            if not version.supports_exec_service_context:
                raise RuntimeError(
                    f'exec with service_context not supported on Juju version {version}'
                )
        return self._pebble.exec(
            command,
            service_context=service_context,
            environment=environment,
            working_dir=working_dir,
            timeout=timeout,
            user_id=user_id,
            user=user,
            group_id=group_id,
            group=group,
            stdin=stdin,
            stdout=stdout,
            stderr=stderr,
            encoding=encoding,
            combine_stderr=combine_stderr,
        )

    def send_signal(self, sig: Union[int, str], *service_names: str):

        if not service_names:
            raise TypeError('send_signal expected at least 1 service name, got 0')

        self._pebble.send_signal(sig, service_names)

    def get_notice(self, id: str) -> pebble.Notice:

        try:
            return self._pebble.get_notice(id)
        except pebble.APIError as e:
            if e.code == 404:
                raise ModelError(f'notice {id!r} not found') from e
            raise

    def get_notices(
        self,
        *,
        users: Optional[pebble.NoticesUsers] = None,
        user_id: Optional[int] = None,
        types: Optional[Iterable[Union[pebble.NoticeType, str]]] = None,
        keys: Optional[Iterable[str]] = None,
    ) -> List[pebble.Notice]:

        return self._pebble.get_notices(
            users=users,
            user_id=user_id,
            types=types,
            keys=keys,
        )

    @property
    def pebble(self) -> pebble.Client:

        return self._pebble

class ContainerMapping(Mapping[str, Container]):


    def __init__(self, names: Iterable[str], backend: '_ModelBackend'):
        self._containers = {name: Container(name, backend) for name in names}

    def __getitem__(self, key: str):
        return self._containers[key]

    def __iter__(self):
        return iter(self._containers)

    def __len__(self):
        return len(self._containers)

    def __repr__(self):
        return repr(self._containers)

class ServiceInfoMapping(Mapping[str, pebble.ServiceInfo]):


    def __init__(self, services: Iterable[pebble.ServiceInfo]):
        self._services = {s.name: s for s in services}

    def __getitem__(self, key: str):
        return self._services[key]

    def __iter__(self):
        return iter(self._services)

    def __len__(self):
        return len(self._services)

    def __repr__(self):
        return repr(self._services)

class CheckInfoMapping(Mapping[str, pebble.CheckInfo]):


    def __init__(self, checks: Iterable[pebble.CheckInfo]):
        self._checks = {c.name: c for c in checks}

    def __getitem__(self, key: str):
        return self._checks[key]

    def __iter__(self):
        return iter(self._checks)

    def __len__(self):
        return len(self._checks)

    def __repr__(self):
        return repr(self._checks)

class ModelError(Exception):


    pass

class TooManyRelatedAppsError(ModelError):


    def __init__(self, relation_name: str, num_related: int, max_supported: int):
        super().__init__(
            f'Too many remote applications on {relation_name} ({num_related} > {max_supported})'
        )
        self.relation_name = relation_name
        self.num_related = num_related
        self.max_supported = max_supported

class RelationDataError(ModelError):


class RelationDataTypeError(RelationDataError):


class RelationDataAccessError(RelationDataError):


class RelationNotFoundError(ModelError):


class InvalidStatusError(ModelError):


class SecretNotFoundError(ModelError):


_ACTION_RESULT_KEY_REGEX = re.compile(r'^[a-z0-9](([a-z0-9-.]+)?[a-z0-9])?$')

def _format_action_result_dict(
    input: Dict[str, Any],
    parent_key: Optional[str] = None,
    output: Optional[Dict[str, str]] = None,
) -> Dict[str, str]:

    output_: Dict[str, str] = output or {}

    for key, value in input.items():
        if not isinstance(key, str):
            raise ValueError(f'invalid key {key!r}; must be a string')
        if not _ACTION_RESULT_KEY_REGEX.match(key):
            raise ValueError(
                f"key {key!r} is invalid: must be similar to 'key', 'some-key2', or 'some.key'"
            )

        if parent_key:
            key = f'{parent_key}.{key}'

        if isinstance(value, MutableMapping):
            value = typing.cast(Dict[str, Any], value)
            output_ = _format_action_result_dict(value, key, output_)
        elif key in output_:
            raise ValueError(
                f"duplicate key detected in dictionary passed to 'action-set': {key!r}"
            )
        else:
            output_[key] = value

    return output_

class _ModelBackend:


    LEASE_RENEWAL_PERIOD = datetime.timedelta(seconds=30)
    _STORAGE_KEY_RE = re.compile(
        r'.*^-s\s+\(=\s+(?P<storage_key>.*?)\)\s*?$', re.MULTILINE | re.DOTALL
    )

    def __init__(
        self,
        unit_name: Optional[str] = None,
        model_name: Optional[str] = None,
        model_uuid: Optional[str] = None,
    ):
        unit_name_ = unit_name or os.getenv('JUJU_UNIT_NAME')
        if unit_name_ is None:
            raise ValueError('JUJU_UNIT_NAME not set')
        self.unit_name: str = unit_name_

        self.model_name: str = model_name or typing.cast(str, os.getenv('JUJU_MODEL_NAME'))
        self.model_uuid: str = model_uuid or typing.cast(str, os.getenv('JUJU_MODEL_UUID'))
        self.app_name: str = self.unit_name.split('/')[0]

        self._is_leader: Optional[bool] = None
        self._leader_check_time = None
        self._hook_is_running = ''

    def _run(
        self,
        *args: str,
        return_output: bool = False,
        use_json: bool = False,
        input_stream: Optional[str] = None,
    ) -> Union[str, Any, None]:
        kwargs = {
            'stdout': subprocess.PIPE,
            'stderr': subprocess.PIPE,
            'check': True,
            'encoding': 'utf-8',
        }
        if input_stream:
            kwargs.update({'input': input_stream})
        which_cmd = shutil.which(args[0])
        if which_cmd is None:
            raise RuntimeError(f'command not found: {args[0]}')
        args = (which_cmd,) + args[1:]
        if use_json:
            args += ('--format=json',)
        try:
            result = subprocess.run(args, **kwargs)
        except subprocess.CalledProcessError as e:
            raise ModelError(e.stderr) from e
        if return_output:
            if result.stdout is None:
                return ''
            else:
                text: str = result.stdout
                if use_json:
                    return json.loads(text)
                else:
                    return text

    @staticmethod
    def _is_relation_not_found(model_error: Exception) -> bool:
        return 'relation not found' in str(model_error)

    def relation_ids(self, relation_name: str) -> List[int]:
        relation_ids = self._run('relation-ids', relation_name, return_output=True, use_json=True)
        relation_ids = typing.cast(Iterable[str], relation_ids)
        return [int(relation_id.split(':')[-1]) for relation_id in relation_ids]

    def relation_list(self, relation_id: int) -> List[str]:
        try:
            rel_list = self._run(
                'relation-list', '-r', str(relation_id), return_output=True, use_json=True
            )
            return typing.cast(List[str], rel_list)
        except ModelError as e:
            if self._is_relation_not_found(e):
                raise RelationNotFoundError() from e
            raise

    def relation_remote_app_name(self, relation_id: int) -> Optional[str]:

        if 'JUJU_RELATION_ID' in os.environ and 'JUJU_REMOTE_APP' in os.environ:
            event_relation_id = int(os.environ['JUJU_RELATION_ID'].split(':')[-1])
            if relation_id == event_relation_id:
                return os.getenv('JUJU_REMOTE_APP') or None

        try:
            rel_id = self._run(
                'relation-list', '-r', str(relation_id), '--app', return_output=True, use_json=True
            )
            return typing.cast(str, rel_id)

        except ModelError as e:
            if self._is_relation_not_found(e):
                return None
            if 'option provided but not defined: --app' in str(e):
                return None
            raise

    def relation_get(
        self, relation_id: int, member_name: str, is_app: bool
    ) -> '_RelationDataContent_Raw':
        if not isinstance(is_app, bool):
            raise TypeError('is_app parameter to relation_get must be a boolean')

        if is_app:
            version = JujuVersion.from_environ()
            if not version.has_app_data():
                raise RuntimeError(
                    f'getting application data is not supported on Juju version {version}'
                )

        args = ['relation-get', '-r', str(relation_id), '-', member_name]
        if is_app:
            args.append('--app')

        try:
            raw_data_content = self._run(*args, return_output=True, use_json=True)
            return typing.cast('_RelationDataContent_Raw', raw_data_content)
        except ModelError as e:
            if self._is_relation_not_found(e):
                raise RelationNotFoundError() from e
            raise

    def relation_set(self, relation_id: int, key: str, value: str, is_app: bool) -> None:
        if not isinstance(is_app, bool):
            raise TypeError('is_app parameter to relation_set must be a boolean')

        if is_app:
            version = JujuVersion.from_environ()
            if not version.has_app_data():
                raise RuntimeError(
                    f'setting application data is not supported on Juju version {version}'
                )

        args = ['relation-set', '-r', str(relation_id)]
        if is_app:
            args.append('--app')
        args.extend(['--file', '-'])

        try:
            content = yaml.safe_dump({key: value})
            self._run(*args, input_stream=content)
        except ModelError as e:
            if self._is_relation_not_found(e):
                raise RelationNotFoundError() from e
            raise

    def config_get(self) -> Dict[str, Union[bool, int, float, str]]:
        out = self._run('config-get', return_output=True, use_json=True)
        return typing.cast(Dict[str, Union[bool, int, float, str]], out)

    def is_leader(self) -> bool:

        now = time.monotonic()
        if self._leader_check_time is None:
            check = True
        else:
            time_since_check = datetime.timedelta(seconds=now - self._leader_check_time)
            check = time_since_check > self.LEASE_RENEWAL_PERIOD or self._is_leader is None
        if check:
            self._leader_check_time = now
            is_leader = self._run('is-leader', return_output=True, use_json=True)
            self._is_leader = typing.cast(bool, is_leader)

        return typing.cast(bool, self._is_leader)

    def resource_get(self, resource_name: str) -> str:
        out = self._run('resource-get', resource_name, return_output=True)
        return typing.cast(str, out).strip()

    def pod_spec_set(
        self, spec: Mapping[str, Any], k8s_resources: Optional[Mapping[str, Any]] = None
    ):
        tmpdir = Path(tempfile.mkdtemp('-pod-spec-set'))
        try:
            spec_path = tmpdir / 'spec.yaml'
            with spec_path.open('wt', encoding='utf8') as f:
                yaml.safe_dump(spec, stream=f)
            args = ['--file', str(spec_path)]
            if k8s_resources:
                k8s_res_path = tmpdir / 'k8s-resources.yaml'
                with k8s_res_path.open('wt', encoding='utf8') as f:
                    yaml.safe_dump(k8s_resources, stream=f)
                args.extend(['--k8s-resources', str(k8s_res_path)])
            self._run('pod-spec-set', *args)
        finally:
            shutil.rmtree(str(tmpdir))

    def status_get(self, *, is_app: bool = False) -> '_StatusDict':

        content = self._run(
            'status-get',
            '--include-data',
            f'--application={is_app}',
            use_json=True,
            return_output=True,
        )

        if is_app:
            content = typing.cast(Dict[str, Dict[str, str]], content)
            app_status = content['application-status']
            return {'status': app_status['status'], 'message': app_status['message']}
        else:
            return typing.cast('_StatusDict', content)

    def status_set(self, status: str, message: str = '', *, is_app: bool = False) -> None:

        if not isinstance(is_app, bool):
            raise TypeError('is_app parameter must be boolean')
        self._run('status-set', f'--application={is_app}', status, message)

    def storage_list(self, name: str) -> List[int]:
        storages = self._run('storage-list', name, return_output=True, use_json=True)
        storages = typing.cast(List[str], storages)
        return [int(s.split('/')[1]) for s in storages]

    def _storage_event_details(self) -> Tuple[int, str]:
        output = self._run('storage-get', '--help', return_output=True)
        output = typing.cast(str, output)
        match = self._STORAGE_KEY_RE.match(output)
        if match is None:
            raise RuntimeError(f'unable to find storage key in {output!r}')
        key = match.groupdict()['storage_key']

        index = int(key.split('/')[1])
        location = self.storage_get(key, 'location')
        return index, location

    def storage_get(self, storage_name_id: str, attribute: str) -> str:
        if not len(attribute) > 0:
            raise RuntimeError(
                'calling storage_get with `attribute=""` will return a dict '
                'and not a string. This usage is not supported.'
            )
        out = self._run(
            'storage-get', '-s', storage_name_id, attribute, return_output=True, use_json=True
        )
        return typing.cast(str, out)

    def storage_add(self, name: str, count: int = 1) -> None:
        if not isinstance(count, int) or isinstance(count, bool):
            raise TypeError(f'storage count must be integer, got: {count} ({type(count)})')
        self._run('storage-add', f'{name}={count}')

    def action_get(self) -> Dict[str, Any]:
        out = self._run('action-get', return_output=True, use_json=True)
        return typing.cast(Dict[str, Any], out)

    def action_set(self, results: Dict[str, Any]) -> None:
        flat_results = _format_action_result_dict(results)
        self._run('action-set', *[f'{k}={v}' for k, v in flat_results.items()])

    def action_log(self, message: str) -> None:
        self._run('action-log', message)

    def action_fail(self, message: str = '') -> None:
        self._run('action-fail', message)

    def application_version_set(self, version: str) -> None:
        self._run('application-version-set', '--', version)

    @classmethod
    def log_split(
        cls, message: str, max_len: int = MAX_LOG_LINE_LEN
    ) -> Generator[str, None, None]:

        if len(message) > max_len:
            yield f'Log string greater than {max_len}. Splitting into multiple chunks: '

        while message:
            yield message[:max_len]
            message = message[max_len:]

    def juju_log(self, level: str, message: str) -> None:

        for line in self.log_split(message):
            self._run('juju-log', '--log-level', level, '--', line)

    def network_get(self, binding_name: str, relation_id: Optional[int] = None) -> '_NetworkDict':

        cmd = ['network-get', binding_name]
        if relation_id is not None:
            cmd.extend(['-r', str(relation_id)])
        try:
            network = self._run(*cmd, return_output=True, use_json=True)
            return typing.cast('_NetworkDict', network)
        except ModelError as e:
            if self._is_relation_not_found(e):
                raise RelationNotFoundError() from e
            raise

    def add_metrics(
        self, metrics: Mapping[str, Union[int, float]], labels: Optional[Mapping[str, str]] = None
    ) -> None:
        cmd: List[str] = ['add-metric']
        if labels:
            label_args: List[str] = []
            for k, v in labels.items():
                _ModelBackendValidator.validate_metric_label(k)
                _ModelBackendValidator.validate_label_value(k, v)
                label_args.append(f'{k}={v}')
            cmd.extend(['--labels', ','.join(label_args)])

        metric_args: List[str] = []
        for k, v in metrics.items():
            _ModelBackendValidator.validate_metric_key(k)
            metric_value = _ModelBackendValidator.format_metric_value(v)
            metric_args.append(f'{k}={metric_value}')
        cmd.extend(metric_args)
        self._run(*cmd)

    def get_pebble(self, socket_path: str) -> pebble.Client:

        return pebble.Client(socket_path=socket_path)

    def planned_units(self) -> int:

        app_state = self._run('goal-state', return_output=True, use_json=True)
        app_state = typing.cast(Dict[str, Dict[str, Any]], app_state)

        units = app_state.get('units', {})
        num_alive = sum(1 for unit in units.values() if unit['status'] != 'dying')
        return num_alive

    def update_relation_data(
        self, relation_id: int, _entity: Union['Unit', 'Application'], key: str, value: str
    ):
        self.relation_set(relation_id, key, value, isinstance(_entity, Application))

    def secret_get(
        self,
        *,
        id: Optional[str] = None,
        label: Optional[str] = None,
        refresh: bool = False,
        peek: bool = False,
    ) -> Dict[str, str]:
        args: List[str] = []
        if id is not None:
            args.append(id)
        if label is not None:
            args.extend(['--label', label])
        if refresh:
            args.append('--refresh')
        if peek:
            args.append('--peek')
        try:
            result = self._run('secret-get', *args, return_output=True, use_json=True)
        except ModelError as e:
            if 'not found' in str(e):
                raise SecretNotFoundError() from e
            raise
        return typing.cast(Dict[str, str], result)

    def _run_for_secret(
        self, *args: str, return_output: bool = False, use_json: bool = False
    ) -> Union[str, Any, None]:
        try:
            return self._run(*args, return_output=return_output, use_json=use_json)
        except ModelError as e:
            if 'not found' in str(e):
                raise SecretNotFoundError() from e
            raise

    def secret_info_get(
        self, *, id: Optional[str] = None, label: Optional[str] = None
    ) -> SecretInfo:
        args: List[str] = []
        if id is not None:
            args.append(id)
        elif label is not None:
            args.extend(['--label', label])
        result = self._run_for_secret('secret-info-get', *args, return_output=True, use_json=True)
        info_dicts = typing.cast(Dict[str, Any], result)
        id = list(info_dicts)[0]
        return SecretInfo.from_dict(id, typing.cast(Dict[str, Any], info_dicts[id]))

    def secret_set(
        self,
        id: str,
        *,
        content: Optional[Dict[str, str]] = None,
        label: Optional[str] = None,
        description: Optional[str] = None,
        expire: Optional[datetime.datetime] = None,
        rotate: Optional[SecretRotate] = None,
    ):
        args = [id]
        if label is not None:
            args.extend(['--label', label])
        if description is not None:
            args.extend(['--description', description])
        if expire is not None:
            args.extend(['--expire', expire.isoformat()])
        if rotate is not None:
            args += ['--rotate', rotate.value]
        if content is not None:
            for k, v in content.items():
                args.append(f'{k}={v}')
        self._run_for_secret('secret-set', *args)

    def secret_add(
        self,
        content: Dict[str, str],
        *,
        label: Optional[str] = None,
        description: Optional[str] = None,
        expire: Optional[datetime.datetime] = None,
        rotate: Optional[SecretRotate] = None,
        owner: Optional[str] = None,
    ) -> str:
        args: List[str] = []
        if label is not None:
            args.extend(['--label', label])
        if description is not None:
            args.extend(['--description', description])
        if expire is not None:
            args.extend(['--expire', expire.isoformat()])
        if rotate is not None:
            args += ['--rotate', rotate.value]
        if owner is not None:
            args += ['--owner', owner]
        for k, v in content.items():
            args.append(f'{k}={v}')
        result = self._run('secret-add', *args, return_output=True)
        secret_id = typing.cast(str, result)
        return secret_id.strip()

    def secret_grant(self, id: str, relation_id: int, *, unit: Optional[str] = None):
        args = [id, '--relation', str(relation_id)]
        if unit is not None:
            args += ['--unit', str(unit)]
        self._run_for_secret('secret-grant', *args)

    def secret_revoke(self, id: str, relation_id: int, *, unit: Optional[str] = None):
        args = [id, '--relation', str(relation_id)]
        if unit is not None:
            args += ['--unit', str(unit)]
        self._run_for_secret('secret-revoke', *args)

    def secret_remove(self, id: str, *, revision: Optional[int] = None):
        args = [id]
        if revision is not None:
            args.extend(['--revision', str(revision)])
        self._run_for_secret('secret-remove', *args)

    def open_port(self, protocol: str, port: Optional[int] = None):
        arg = f'{port}/{protocol}' if port is not None else protocol
        self._run('open-port', arg)

    def close_port(self, protocol: str, port: Optional[int] = None):
        arg = f'{port}/{protocol}' if port is not None else protocol
        self._run('close-port', arg)

    def opened_ports(self) -> Set[Port]:
        output = typing.cast(str, self._run('opened-ports', return_output=True))
        ports: Set[Port] = set()
        for line in output.splitlines():
            line = line.strip()
            if not line:
                continue
            port = self._parse_opened_port(line)
            if port is not None:
                ports.add(port)
        return ports

    @classmethod
    def _parse_opened_port(cls, port_str: str) -> Optional[Port]:
        if port_str == 'icmp':
            return Port('icmp', None)
        port_range, slash, protocol = port_str.partition('/')
        if not slash or protocol not in ['tcp', 'udp']:
            logger.warning('Unexpected opened-ports protocol: %s', port_str)
            return None
        port, hyphen, _ = port_range.partition('-')
        if hyphen:
            logger.warning('Ignoring opened-ports port range: %s', port_str)
        protocol_lit = typing.cast(typing.Literal['tcp', 'udp'], protocol)
        return Port(protocol_lit, int(port))

    def reboot(self, now: bool = False):
        if now:
            self._run('juju-reboot', '--now')
            sys.exit()
        else:
            self._run('juju-reboot')

    def credential_get(self) -> 'CloudSpec':

        result = self._run('credential-get', return_output=True, use_json=True)
        return CloudSpec.from_dict(typing.cast(Dict[str, Any], result))

class _ModelBackendValidator:


    METRIC_KEY_REGEX = re.compile(r'^[a-zA-Z](?:[a-zA-Z0-9-_]*[a-zA-Z0-9])?$')

    @classmethod
    def validate_metric_key(cls, key: str):
        if cls.METRIC_KEY_REGEX.match(key) is None:
            raise ModelError(
                f'invalid metric key {key!r}: must match {cls.METRIC_KEY_REGEX.pattern}'
            )

    @classmethod
    def validate_metric_label(cls, label_name: str):
        if cls.METRIC_KEY_REGEX.match(label_name) is None:
            raise ModelError(
                f'invalid metric label name {label_name!r}: '
                f'must match {cls.METRIC_KEY_REGEX.pattern}'
            )

    @classmethod
    def format_metric_value(cls, value: Union[int, float]):
        if not isinstance(value, (int, float)):
            raise ModelError(
                f'invalid metric value {value!r} provided:' ' must be a positive finite float'
            )

        if math.isnan(value) or math.isinf(value) or value < 0:
            raise ModelError(
                f'invalid metric value {value!r} provided:' ' must be a positive finite float'
            )
        return str(value)

    @classmethod
    def validate_label_value(cls, label: str, value: str):
        if not value:
            raise ModelError(f'metric label {label} has an empty value, which is not allowed')
        v = str(value)
        if re.search('[,=]', v) is not None:
            raise ModelError(f'metric label values must not contain "," or "=": {label}={value!r}')

class LazyNotice:


    id: str
    user_id: Optional[int]
    type: Union[pebble.NoticeType, str]
    key: str
    first_occurred: datetime.datetime
    last_occurred: datetime.datetime
    last_repeated: datetime.datetime
    occurrences: int
    last_data: Dict[str, str]
    repeat_after: Optional[datetime.timedelta]
    expire_after: Optional[datetime.timedelta]

    def __init__(self, container: Container, id: str, type: str, key: str):
        self._container = container
        self.id = id
        try:
            self.type = pebble.NoticeType(type)
        except ValueError:
            self.type = type
        self.key = key

        self._notice: Optional[ops.pebble.Notice] = None

    def __repr__(self):
        type_repr = self.type if isinstance(self.type, pebble.NoticeType) else repr(self.type)
        return f'LazyNotice(id={self.id!r}, type={type_repr}, key={self.key!r})'

    def __getattr__(self, item: str):
        self._ensure_loaded()
        return getattr(self._notice, item)

    def _ensure_loaded(self):
        if self._notice is not None:
            return
        self._notice = self._container.get_notice(self.id)
        assert self._notice.type == self.type
        assert self._notice.key == self.key

class LazyCheckInfo:


    name: str
    level: Optional[Union[pebble.CheckLevel, str]]
    status: Union[pebble.CheckStatus, str]
    failures: int
    threshold: int
    change_id: Optional[pebble.ChangeID]

    def __init__(self, container: Container, name: str):
        self._container = container
        self.name = name
        self._info: Optional[ops.pebble.CheckInfo] = None

    def __repr__(self):
        return f'LazyCheckInfo(name={self.name!r})'

    def __getattr__(self, item: str):
        self._ensure_loaded()
        return getattr(self._info, item)

    def _ensure_loaded(self):
        if self._info is not None:
            return
        self._info = self._container.get_check(self.name)

@dataclasses.dataclass(frozen=True)
class CloudCredential:


    auth_type: str

    attributes: Dict[str, str] = dataclasses.field(default_factory=dict)

    redacted: List[str] = dataclasses.field(default_factory=list)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> 'CloudCredential':

        return cls(
            auth_type=d['auth-type'],
            attributes=d.get('attrs') or {},
            redacted=d.get('redacted') or [],
        )

@dataclasses.dataclass(frozen=True)
class CloudSpec:


    type: str

    name: str

    region: Optional[str] = None

    endpoint: Optional[str] = None

    identity_endpoint: Optional[str] = None

    storage_endpoint: Optional[str] = None

    credential: Optional[CloudCredential] = None

    ca_certificates: List[str] = dataclasses.field(default_factory=list)

    skip_tls_verify: bool = False

    is_controller_cloud: bool = False

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> 'CloudSpec':

        return cls(
            type=d['type'],
            name=d['name'],
            region=d.get('region') or None,
            endpoint=d.get('endpoint') or None,
            identity_endpoint=d.get('identity-endpoint') or None,
            storage_endpoint=d.get('storage-endpoint') or None,
            credential=CloudCredential.from_dict(d['credential']) if d.get('credential') else None,
            ca_certificates=d.get('cacertificates') or [],
            skip_tls_verify=d.get('skip-tls-verify') or False,
            is_controller_cloud=d.get('is-controller-cloud') or False,
        )

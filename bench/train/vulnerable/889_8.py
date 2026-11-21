
import logging
import urllib
from typing import Any, Dict, Optional

from synapse.api.constants import Membership
from synapse.api.errors import Codes, HttpResponseException, SynapseError
from synapse.api.urls import (
    FEDERATION_UNSTABLE_PREFIX,
    FEDERATION_V1_PREFIX,
    FEDERATION_V2_PREFIX,
)
from synapse.logging.utils import log_function

logger = logging.getLogger(__name__)

class TransportLayerClient:


    def __init__(self, hs):
        self.server_name = hs.hostname
        self.client = hs.get_http_client()

    @log_function
    def get_room_state_ids(self, destination, room_id, event_id):

        logger.debug("get_room_state_ids dest=%s, room=%s", destination, room_id)

        path = _create_v1_path("/state_ids/%s", room_id)
        return self.client.get_json(
            destination,
            path=path,
            args={"event_id": event_id},
            try_trailing_slash_on_400=True,
        )

    @log_function
    def get_event(self, destination, event_id, timeout=None):

        logger.debug("get_pdu dest=%s, event_id=%s", destination, event_id)

        path = _create_v1_path("/event/%s", event_id)
        return self.client.get_json(
            destination, path=path, timeout=timeout, try_trailing_slash_on_400=True
        )

    @log_function
    def backfill(self, destination, room_id, event_tuples, limit):

        logger.debug(
            "backfill dest=%s, room_id=%s, event_tuples=%r, limit=%s",
            destination,
            room_id,
            event_tuples,
            str(limit),
        )

        if not event_tuples:
            return

        path = _create_v1_path("/backfill/%s", room_id)

        args = {"v": event_tuples, "limit": [str(limit)]}

        return self.client.get_json(
            destination, path=path, args=args, try_trailing_slash_on_400=True
        )

    @log_function
    async def send_transaction(self, transaction, json_data_callback=None):

        logger.debug(
            "send_data dest=%s, txid=%s",
            transaction.destination,
            transaction.transaction_id,
        )

        if transaction.destination == self.server_name:
            raise RuntimeError("Transport layer cannot send to itself!")

        json_data = transaction.get_dict()

        path = _create_v1_path("/send/%s", transaction.transaction_id)

        response = await self.client.put_json(
            transaction.destination,
            path=path,
            data=json_data,
            json_data_callback=json_data_callback,
            long_retries=True,
            backoff_on_404=True,
            try_trailing_slash_on_400=True,
        )

        return response

    @log_function
    async def make_query(
        self, destination, query_type, args, retry_on_dns_fail, ignore_backoff=False
    ):
        path = _create_v1_path("/query/%s", query_type)

        content = await self.client.get_json(
            destination=destination,
            path=path,
            args=args,
            retry_on_dns_fail=retry_on_dns_fail,
            timeout=10000,
            ignore_backoff=ignore_backoff,
        )

        return content

    @log_function
    async def make_membership_event(
        self, destination, room_id, user_id, membership, params
    ):

        valid_memberships = {Membership.JOIN, Membership.LEAVE}
        if membership not in valid_memberships:
            raise RuntimeError(
                "make_membership_event called with membership='%s', must be one of %s"
                % (membership, ",".join(valid_memberships))
            )
        path = _create_v1_path("/make_%s/%s/%s", membership, room_id, user_id)

        ignore_backoff = False
        retry_on_dns_fail = False

        if membership == Membership.LEAVE:
            ignore_backoff = True
            retry_on_dns_fail = True

        content = await self.client.get_json(
            destination=destination,
            path=path,
            args=params,
            retry_on_dns_fail=retry_on_dns_fail,
            timeout=20000,
            ignore_backoff=ignore_backoff,
        )

        return content

    @log_function
    async def send_join_v1(self, destination, room_id, event_id, content):
        path = _create_v1_path("/send_join/%s/%s", room_id, event_id)

        response = await self.client.put_json(
            destination=destination, path=path, data=content
        )

        return response

    @log_function
    async def send_join_v2(self, destination, room_id, event_id, content):
        path = _create_v2_path("/send_join/%s/%s", room_id, event_id)

        response = await self.client.put_json(
            destination=destination, path=path, data=content
        )

        return response

    @log_function
    async def send_leave_v1(self, destination, room_id, event_id, content):
        path = _create_v1_path("/send_leave/%s/%s", room_id, event_id)

        response = await self.client.put_json(
            destination=destination,
            path=path,
            data=content,
            ignore_backoff=True,
        )

        return response

    @log_function
    async def send_leave_v2(self, destination, room_id, event_id, content):
        path = _create_v2_path("/send_leave/%s/%s", room_id, event_id)

        response = await self.client.put_json(
            destination=destination,
            path=path,
            data=content,
            ignore_backoff=True,
        )

        return response

    @log_function
    async def send_invite_v1(self, destination, room_id, event_id, content):
        path = _create_v1_path("/invite/%s/%s", room_id, event_id)

        response = await self.client.put_json(
            destination=destination, path=path, data=content, ignore_backoff=True
        )

        return response

    @log_function
    async def send_invite_v2(self, destination, room_id, event_id, content):
        path = _create_v2_path("/invite/%s/%s", room_id, event_id)

        response = await self.client.put_json(
            destination=destination, path=path, data=content, ignore_backoff=True
        )

        return response

    @log_function
    async def get_public_rooms(
        self,
        remote_server: str,
        limit: Optional[int] = None,
        since_token: Optional[str] = None,
        search_filter: Optional[Dict] = None,
        include_all_networks: bool = False,
        third_party_instance_id: Optional[str] = None,
    ):

        if search_filter:
            path = _create_v1_path("/publicRooms")

            data = {
                "include_all_networks": "true" if include_all_networks else "false"
            }
            if third_party_instance_id:
                data["third_party_instance_id"] = third_party_instance_id
            if limit:
                data["limit"] = str(limit)
            if since_token:
                data["since"] = since_token

            data["filter"] = search_filter

            try:
                response = await self.client.post_json(
                    destination=remote_server, path=path, data=data, ignore_backoff=True
                )
            except HttpResponseException as e:
                if e.code == 403:
                    raise SynapseError(
                        403,
                        "You are not allowed to view the public rooms list of %s"
                        % (remote_server,),
                        errcode=Codes.FORBIDDEN,
                    )
                raise
        else:
            path = _create_v1_path("/publicRooms")

            args = {
                "include_all_networks": "true" if include_all_networks else "false"
            }
            if third_party_instance_id:
                args["third_party_instance_id"] = (third_party_instance_id,)
            if limit:
                args["limit"] = [str(limit)]
            if since_token:
                args["since"] = [since_token]

            try:
                response = await self.client.get_json(
                    destination=remote_server, path=path, args=args, ignore_backoff=True
                )
            except HttpResponseException as e:
                if e.code == 403:
                    raise SynapseError(
                        403,
                        "You are not allowed to view the public rooms list of %s"
                        % (remote_server,),
                        errcode=Codes.FORBIDDEN,
                    )
                raise

        return response

    @log_function
    async def exchange_third_party_invite(self, destination, room_id, event_dict):
        path = _create_v1_path("/exchange_third_party_invite/%s", room_id)

        response = await self.client.put_json(
            destination=destination, path=path, data=event_dict
        )

        return response

    @log_function
    async def get_event_auth(self, destination, room_id, event_id):
        path = _create_v1_path("/event_auth/%s/%s", room_id, event_id)

        content = await self.client.get_json(destination=destination, path=path)

        return content

    @log_function
    async def query_client_keys(self, destination, query_content, timeout):

        path = _create_v1_path("/user/keys/query")

        content = await self.client.post_json(
            destination=destination, path=path, data=query_content, timeout=timeout
        )
        return content

    @log_function
    async def query_user_devices(self, destination, user_id, timeout):

        path = _create_v1_path("/user/devices/%s", user_id)

        content = await self.client.get_json(
            destination=destination, path=path, timeout=timeout
        )
        return content

    @log_function
    async def claim_client_keys(self, destination, query_content, timeout):


        path = _create_v1_path("/user/keys/claim")

        content = await self.client.post_json(
            destination=destination, path=path, data=query_content, timeout=timeout
        )
        return content

    @log_function
    async def get_missing_events(
        self,
        destination,
        room_id,
        earliest_events,
        latest_events,
        limit,
        min_depth,
        timeout,
    ):
        path = _create_v1_path("/get_missing_events/%s", room_id)

        content = await self.client.post_json(
            destination=destination,
            path=path,
            data={
                "limit": int(limit),
                "min_depth": int(min_depth),
                "earliest_events": earliest_events,
                "latest_events": latest_events,
            },
            timeout=timeout,
        )

        return content

    @log_function
    def get_group_profile(self, destination, group_id, requester_user_id):

        path = _create_v1_path("/groups/%s/profile", group_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def update_group_profile(self, destination, group_id, requester_user_id, content):

        path = _create_v1_path("/groups/%s/profile", group_id)

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    @log_function
    def get_group_summary(self, destination, group_id, requester_user_id):

        path = _create_v1_path("/groups/%s/summary", group_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def get_rooms_in_group(self, destination, group_id, requester_user_id):

        path = _create_v1_path("/groups/%s/rooms", group_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    def add_room_to_group(
        self, destination, group_id, requester_user_id, room_id, content
    ):

        path = _create_v1_path("/groups/%s/room/%s", group_id, room_id)

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    def update_room_in_group(
        self, destination, group_id, requester_user_id, room_id, config_key, content
    ):

        path = _create_v1_path(
            "/groups/%s/room/%s/config/%s", group_id, room_id, config_key
        )

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    def remove_room_from_group(self, destination, group_id, requester_user_id, room_id):

        path = _create_v1_path("/groups/%s/room/%s", group_id, room_id)

        return self.client.delete_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def get_users_in_group(self, destination, group_id, requester_user_id):

        path = _create_v1_path("/groups/%s/users", group_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def get_invited_users_in_group(self, destination, group_id, requester_user_id):

        path = _create_v1_path("/groups/%s/invited_users", group_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def accept_group_invite(self, destination, group_id, user_id, content):

        path = _create_v1_path("/groups/%s/users/%s/accept_invite", group_id, user_id)

        return self.client.post_json(
            destination=destination, path=path, data=content, ignore_backoff=True
        )

    @log_function
    def join_group(self, destination, group_id, user_id, content):

        path = _create_v1_path("/groups/%s/users/%s/join", group_id, user_id)

        return self.client.post_json(
            destination=destination, path=path, data=content, ignore_backoff=True
        )

    @log_function
    def invite_to_group(
        self, destination, group_id, user_id, requester_user_id, content
    ):

        path = _create_v1_path("/groups/%s/users/%s/invite", group_id, user_id)

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    @log_function
    def invite_to_group_notification(self, destination, group_id, user_id, content):


        path = _create_v1_path("/groups/local/%s/users/%s/invite", group_id, user_id)

        return self.client.post_json(
            destination=destination, path=path, data=content, ignore_backoff=True
        )

    @log_function
    def remove_user_from_group(
        self, destination, group_id, requester_user_id, user_id, content
    ):

        path = _create_v1_path("/groups/%s/users/%s/remove", group_id, user_id)

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    @log_function
    def remove_user_from_group_notification(
        self, destination, group_id, user_id, content
    ):


        path = _create_v1_path("/groups/local/%s/users/%s/remove", group_id, user_id)

        return self.client.post_json(
            destination=destination, path=path, data=content, ignore_backoff=True
        )

    @log_function
    def renew_group_attestation(self, destination, group_id, user_id, content):


        path = _create_v1_path("/groups/%s/renew_attestation/%s", group_id, user_id)

        return self.client.post_json(
            destination=destination, path=path, data=content, ignore_backoff=True
        )

    @log_function
    def update_group_summary_room(
        self, destination, group_id, user_id, room_id, category_id, content
    ):

        if category_id:
            path = _create_v1_path(
                "/groups/%s/summary/categories/%s/rooms/%s",
                group_id,
                category_id,
                room_id,
            )
        else:
            path = _create_v1_path("/groups/%s/summary/rooms/%s", group_id, room_id)

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": user_id},
            data=content,
            ignore_backoff=True,
        )

    @log_function
    def delete_group_summary_room(
        self, destination, group_id, user_id, room_id, category_id
    ):

        if category_id:
            path = _create_v1_path(
                "/groups/%s/summary/categories/%s/rooms/%s",
                group_id,
                category_id,
                room_id,
            )
        else:
            path = _create_v1_path("/groups/%s/summary/rooms/%s", group_id, room_id)

        return self.client.delete_json(
            destination=destination,
            path=path,
            args={"requester_user_id": user_id},
            ignore_backoff=True,
        )

    @log_function
    def get_group_categories(self, destination, group_id, requester_user_id):

        path = _create_v1_path("/groups/%s/categories", group_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def get_group_category(self, destination, group_id, requester_user_id, category_id):

        path = _create_v1_path("/groups/%s/categories/%s", group_id, category_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def update_group_category(
        self, destination, group_id, requester_user_id, category_id, content
    ):

        path = _create_v1_path("/groups/%s/categories/%s", group_id, category_id)

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    @log_function
    def delete_group_category(
        self, destination, group_id, requester_user_id, category_id
    ):

        path = _create_v1_path("/groups/%s/categories/%s", group_id, category_id)

        return self.client.delete_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def get_group_roles(self, destination, group_id, requester_user_id):

        path = _create_v1_path("/groups/%s/roles", group_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def get_group_role(self, destination, group_id, requester_user_id, role_id):

        path = _create_v1_path("/groups/%s/roles/%s", group_id, role_id)

        return self.client.get_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def update_group_role(
        self, destination, group_id, requester_user_id, role_id, content
    ):

        path = _create_v1_path("/groups/%s/roles/%s", group_id, role_id)

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    @log_function
    def delete_group_role(self, destination, group_id, requester_user_id, role_id):

        path = _create_v1_path("/groups/%s/roles/%s", group_id, role_id)

        return self.client.delete_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    @log_function
    def update_group_summary_user(
        self, destination, group_id, requester_user_id, user_id, role_id, content
    ):

        if role_id:
            path = _create_v1_path(
                "/groups/%s/summary/roles/%s/users/%s", group_id, role_id, user_id
            )
        else:
            path = _create_v1_path("/groups/%s/summary/users/%s", group_id, user_id)

        return self.client.post_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    @log_function
    def set_group_join_policy(self, destination, group_id, requester_user_id, content):

        path = _create_v1_path("/groups/%s/settings/m.join_policy", group_id)

        return self.client.put_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            data=content,
            ignore_backoff=True,
        )

    @log_function
    def delete_group_summary_user(
        self, destination, group_id, requester_user_id, user_id, role_id
    ):

        if role_id:
            path = _create_v1_path(
                "/groups/%s/summary/roles/%s/users/%s", group_id, role_id, user_id
            )
        else:
            path = _create_v1_path("/groups/%s/summary/users/%s", group_id, user_id)

        return self.client.delete_json(
            destination=destination,
            path=path,
            args={"requester_user_id": requester_user_id},
            ignore_backoff=True,
        )

    def bulk_get_publicised_groups(self, destination, user_ids):


        path = _create_v1_path("/get_groups_publicised")

        content = {"user_ids": user_ids}

        return self.client.post_json(
            destination=destination, path=path, data=content, ignore_backoff=True
        )

    def get_room_complexity(self, destination, room_id):

        path = _create_path(FEDERATION_UNSTABLE_PREFIX, "/rooms/%s/complexity", room_id)

        return self.client.get_json(destination=destination, path=path)

def _create_path(federation_prefix, path, *args):

    return federation_prefix + path % tuple(urllib.parse.quote(arg, "") for arg in args)

def _create_v1_path(path, *args):

    return _create_path(FEDERATION_V1_PREFIX, path, *args)

def _create_v2_path(path, *args):

    return _create_path(FEDERATION_V2_PREFIX, path, *args)

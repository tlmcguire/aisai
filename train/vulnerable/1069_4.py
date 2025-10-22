

from __future__ import annotations

import json
from typing import Any, Callable

import orjson
from gradio_client.documentation import document

from gradio.components.base import Component
from gradio.events import Events

@document()
class JSON(Component):


    EVENTS = [Events.change]

    def __init__(
        self,
        value: str | dict | list | Callable | None = None,
        *,
        label: str | None = None,
        every: float | None = None,
        show_label: bool | None = None,
        container: bool = True,
        scale: int | None = None,
        min_width: int = 160,
        visible: bool = True,
        elem_id: str | None = None,
        elem_classes: list[str] | str | None = None,
        render: bool = True,
        key: int | str | None = None,
    ):

        super().__init__(
            label=label,
            every=every,
            show_label=show_label,
            container=container,
            scale=scale,
            min_width=min_width,
            visible=visible,
            elem_id=elem_id,
            elem_classes=elem_classes,
            render=render,
            key=key,
            value=value,
        )

    def preprocess(self, payload: dict | list | None) -> dict | list | None:

        return payload

    def postprocess(self, value: dict | list | str | None) -> dict | list | None:

        if value is None:
            return None
        if isinstance(value, str):
            return orjson.loads(value)
        else:
            return orjson.loads(
                orjson.dumps(
                    value,
                    option=orjson.OPT_SERIALIZE_NUMPY | orjson.OPT_PASSTHROUGH_DATETIME,
                    default=str,
                )
            )

    def example_payload(self) -> Any:
        return {"foo": "bar"}

    def example_value(self) -> Any:
        return {"foo": "bar"}

    def read_from_flag(self, payload: Any):
        return json.loads(payload)

    def api_info(self) -> dict[str, Any]:
        return {"type": {}, "description": "any valid json"}

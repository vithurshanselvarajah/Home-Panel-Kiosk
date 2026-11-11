"""Checks the trimmed ESPHome proto against aioesphomeapi's own types.

Home Assistant speaks the ESPHome native API with aioesphomeapi, so the
device side has to encode messages exactly the way aioesphomeapi does. A
difference is a silent bug: the message parses, the field is wrong, and
the symptom is an entity that never changes.

This script encodes the same values with aioesphomeapi and prints the
bytes. The Go test in device/esphome/wire_test.go asserts the same
values, so the two are checked against each other.

    python3 tools/check_wire.py
"""
import sys

from aioesphomeapi import api_pb2

# (message name, aioesphomeapi message, field values)
VECTORS = [
    ("HelloRequest", api_pb2.HelloRequest,
     dict(client_info="Home Assistant", api_version_major=1, api_version_minor=16)),
    ("HelloResponse", api_pb2.HelloResponse,
     dict(api_version_major=1, api_version_minor=16, server_info="Home Panel")),
    ("ListEntitiesSwitchResponse", api_pb2.ListEntitiesSwitchResponse,
     dict(key=42, name="Screen", object_id="screen", icon="mdi:monitor")),
    ("ListEntitiesNumberResponse", api_pb2.ListEntitiesNumberResponse,
     dict(key=43, name="Brightness", object_id="brightness",
          min_value=0, max_value=100, step=1, mode=1)),
    ("ListEntitiesSelectResponse", api_pb2.ListEntitiesSelectResponse,
     dict(key=44, name="View", object_id="view",
          options=["dashboard", "slideshow"])),
    ("ListEntitiesTextResponse", api_pb2.ListEntitiesTextResponse,
     dict(key=45, name="Notification", object_id="notification", mode=0)),
    ("ListEntitiesButtonResponse", api_pb2.ListEntitiesButtonResponse,
     dict(key=46, name="Wake", object_id="wake", device_class="restart")),
    ("SwitchCommandRequest", api_pb2.SwitchCommandRequest,
     dict(key=42, state=True)),
    ("NumberCommandRequest", api_pb2.NumberCommandRequest,
     dict(key=43, state=45.0)),
    ("SelectCommandRequest", api_pb2.SelectCommandRequest,
     dict(key=44, state="slideshow")),
    ("TextCommandRequest", api_pb2.TextCommandRequest,
     dict(key=45, state="hello")),
    ("ButtonCommandRequest", api_pb2.ButtonCommandRequest,
     dict(key=46)),
    ("SwitchStateResponse", api_pb2.SwitchStateResponse,
     dict(key=42, state=True)),
    ("NumberStateResponse", api_pb2.NumberStateResponse,
     dict(key=43, state=45.0)),
    ("SelectStateResponse", api_pb2.SelectStateResponse,
     dict(key=44, state="dashboard")),
    ("TextStateResponse", api_pb2.TextStateResponse,
     dict(key=45, state="hi")),
    ("SensorStateResponse", api_pb2.SensorStateResponse,
     dict(key=47, state=42.5)),
    ("BinarySensorStateResponse", api_pb2.BinarySensorStateResponse,
     dict(key=48, state=True)),
    ("ListEntitiesServicesResponse", api_pb2.ListEntitiesServicesResponse,
     dict(name="display", key=99, description="d")),
    ("ExecuteServiceRequest", api_pb2.ExecuteServiceRequest,
     dict(key=99)),
    ("HomeassistantActionRequest", api_pb2.HomeassistantActionRequest,
     dict(service="light.toggle")),
    ("HomeAssistantStateResponse", api_pb2.HomeAssistantStateResponse,
     dict(entity_id="sensor.x", state="1", attribute="a")),
    ("PingRequest", api_pb2.PingRequest, {}),
    ("DisconnectRequest", api_pb2.DisconnectRequest, {}),
]


def main():
    print("The Go test asserts these bytes. Any difference here is a")
    print("protocol mismatch with Home Assistant.\n")
    for name, message, fields in VECTORS:
        encoded = message(**fields).SerializeToString()
        print(f'\t\t"{name}",')
        print(f'\t\t"{encoded.hex()}",')
    print(f"\n{len(VECTORS)} vectors printed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

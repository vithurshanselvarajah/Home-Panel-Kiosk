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







    ("VoiceAssistantRequest", api_pb2.VoiceAssistantRequest,
     dict(start=True, conversation_id="c1", flags=0,
          wake_word_phrase="hey jarvis")),
    ("VoiceAssistantResponse", api_pb2.VoiceAssistantResponse,
     dict(port=0, error=False)),
    ("VoiceAssistantEventResponse", api_pb2.VoiceAssistantEventResponse,
     dict(event_type=api_pb2.VoiceAssistantEvent.VOICE_ASSISTANT_STT_END,
          data=[api_pb2.VoiceAssistantEventData(name="text", value="hello")])),
    ("VoiceAssistantAudio", api_pb2.VoiceAssistantAudio,
     dict(data=b"\x01\x02\x03\x04", end=False)),
    ("VoiceAssistantAnnounceRequest", api_pb2.VoiceAssistantAnnounceRequest,
     dict(media_id="http://ha/media.mp3", text="hello")),
    ("VoiceAssistantAnnounceFinished", api_pb2.VoiceAssistantAnnounceFinished, {}),
    ("VoiceAssistantConfigurationResponse", api_pb2.VoiceAssistantConfigurationResponse,
     dict(available_wake_words=[
             api_pb2.VoiceAssistantWakeWord(id="hey_jarvis_v0.1",
                                            wake_word="hey jarvis",
                                            trained_languages=["en"])],
          active_wake_words=["hey_jarvis_v0.1"], max_active_wake_words=1)),
    ("VoiceAssistantSetConfiguration", api_pb2.VoiceAssistantSetConfiguration,
     dict(active_wake_words=["hey_jarvis_v0.1"])),
    ("VoiceAssistantTimerEventResponse", api_pb2.VoiceAssistantTimerEventResponse,
     dict(event_type=api_pb2.VoiceAssistantTimerEvent.VOICE_ASSISTANT_TIMER_FINISHED)),




    ("ListEntitiesMediaPlayerResponse", api_pb2.ListEntitiesMediaPlayerResponse,
     dict(key=50, name="", supports_pause=True,
          feature_flags=(1 << 0) | (1 << 2) | (1 << 3) | (1 << 9) | (1 << 12) | (1 << 14) | (1 << 20),
          supported_formats=[
              api_pb2.MediaPlayerSupportedFormat(
                  purpose=api_pb2.MediaPlayerFormatPurpose.MEDIA_PLAYER_FORMAT_PURPOSE_DEFAULT,
                  format="wav", sample_rate=48000, num_channels=1, sample_bytes=2),
              api_pb2.MediaPlayerSupportedFormat(
                  purpose=api_pb2.MediaPlayerFormatPurpose.MEDIA_PLAYER_FORMAT_PURPOSE_ANNOUNCEMENT,
                  format="wav", sample_rate=48000, num_channels=1, sample_bytes=2),
          ])),
    ("MediaPlayerCommandRequest", api_pb2.MediaPlayerCommandRequest,
     dict(key=50, has_command=True,
          command=api_pb2.MediaPlayerCommand.MEDIA_PLAYER_COMMAND_PAUSE)),
    ("MediaPlayerStateResponse", api_pb2.MediaPlayerStateResponse,
     dict(key=50, state=api_pb2.MediaPlayerState.MEDIA_PLAYER_STATE_PLAYING,
          volume=0.5, muted=False)),

    ("ListEntitiesCameraResponse", api_pb2.ListEntitiesCameraResponse,
     dict(key=51, name="Camera", object_id="camera")),
    ("CameraImageRequest", api_pb2.CameraImageRequest,
     dict(single=True, stream=False)),
    ("CameraImageResponse", api_pb2.CameraImageResponse,
     dict(key=51, data=b"\xff\xd8\xff", done=True)),

    ("ListEntitiesEventResponse", api_pb2.ListEntitiesEventResponse,
     dict(key=52, name="Action Button", object_id="action_button",
          device_class="button", event_types=["single"])),

    ("SendspinPairingTokenResponse", api_pb2.SendspinPairingTokenResponse,
     dict(status=api_pb2.SendspinPairingTokenStatus.SENDSPIN_PAIRING_TOKEN_STATUS_OK,
          token="tok")),
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

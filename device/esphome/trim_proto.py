"""Trims the ESPHome api.proto down to the messages the kiosk needs.

The upstream file is 3200 lines and about 190 messages, most of them for
hardware this device does not have. Trimming it keeps the generated Go
small and keeps the wire format honest: the subset is checked against
aioesphomeapi's own generated types.

The upstream file is kept beside this one as api.proto.upstream, so the
trim can be run again when the protocol changes. The Go code is
generated from the trimmed file with the script in tools/.
"""
import re
import sys

SRC = "api.proto.upstream"
DST = "esphome_api.proto"

# Messages the device side needs, directly or by field reference.
NEEDED = [
    # Handshake.
    "HelloRequest", "HelloResponse",
    "DisconnectRequest", "DisconnectResponse",
    "PingRequest", "PingResponse",
    # Device identity.
    "DeviceInfoRequest", "DeviceInfoResponse",
    "DeviceCapabilitiesRequest", "DeviceCapabilitiesResponse",
    # Entity listing.
    "ListEntitiesRequest", "ListEntitiesDoneResponse",
    "ListEntitiesBinarySensorResponse", "ListEntitiesButtonResponse",
    "ListEntitiesNumberResponse", "ListEntitiesSelectResponse",
    "ListEntitiesSensorResponse", "ListEntitiesSwitchResponse",
    "ListEntitiesTextResponse", "ListEntitiesTextSensorResponse",
    # Device defined services.
    "ListEntitiesServicesResponse", "ListEntitiesServicesArgument",
    "ExecuteServiceRequest", "ExecuteServiceArgument", "ExecuteServiceResponse",
    # State pushes.
    "SubscribeStatesRequest",
    "BinarySensorStateResponse", "NumberStateResponse", "SelectStateResponse",
    "SensorStateResponse", "SwitchStateResponse", "TextStateResponse",
    "TextSensorStateResponse",
    # Commands from Home Assistant to the device.
    #
    # There is no binary sensor command: a binary sensor is read-only,
    # and upstream dropped the message when that became true.
    "ButtonCommandRequest", "NumberCommandRequest",
    "SelectCommandRequest", "SwitchCommandRequest", "TextCommandRequest",
    # Device to Home Assistant.
    "HomeassistantActionRequest", "HomeassistantServiceMap",
    "SubscribeHomeassistantServicesRequest",
    "SubscribeHomeAssistantStatesRequest", "SubscribeHomeAssistantStateResponse",
    "HomeAssistantStateResponse",
    # The voice assistant.
    #
    # The panel is a voice satellite: it detects the wake word itself and
    # starts the turn, then streams the microphone to Home Assistant and
    # plays the reply. These are the messages that carry all of that.
    "SubscribeVoiceAssistantRequest",
    "VoiceAssistantRequest", "VoiceAssistantResponse",
    "VoiceAssistantEventResponse", "VoiceAssistantEventData",
    "VoiceAssistantAudio",
    "VoiceAssistantAnnounceRequest", "VoiceAssistantAnnounceFinished",
    "VoiceAssistantConfigurationRequest", "VoiceAssistantConfigurationResponse",
    "VoiceAssistantWakeWord", "VoiceAssistantExternalWakeWord",
    "VoiceAssistantSetConfiguration",
    "VoiceAssistantTimerEventResponse",
    "VoiceAssistantAudioSettings",
    # The speaker, as a media player.
    #
    # A media player entity is not optional. Home Assistant does not
    # surface a device at all if it reports no entities, and the reply to
    # a voice turn comes back as audio on this entity.
    "ListEntitiesMediaPlayerResponse", "MediaPlayerSupportedFormat",
    "MediaPlayerCommandRequest", "MediaPlayerStateResponse",
    # The camera.
    "ListEntitiesCameraResponse",
    "CameraImageRequest", "CameraImageResponse",
    # Events, for things that are pressed rather than set.
    "ListEntitiesEventResponse",
    # The music player's pairing token.
    #
    # Home Assistant tracks the Sendspin client ids of the devices it
    # knows about, and hands the token to a Sendspin server such as Music
    # Assistant. That is what stops the same speaker appearing twice.
    "SendspinPairingTokenRequest", "SendspinPairingTokenResponse",
]

# Enums referenced by the messages above.
ENUMS = [
    "EntityCategory", "ServiceArgType", "SupportsResponseType",
    "SensorStateClass", "TextMode", "NumberMode", "DeviceClass",
    "UnitOfMass", "UnitOfLength", "UnitOfTemperature", "UnitOfPressure",
    "UnitOfPower", "UnitOfEnergy", "UnitOfVolume", "UnitOfFrequency",
    "UnitOfTime", "UnitOfElectricCurrent", "UnitOfConductivity",
    "UnitOfMagneticField", "UnitOfAngle", "UnitOfRotation", "UnitOfSpeed",
    "UnitOfDistance", "UnitOfDataRate", "UnitOfApparentPower",
    "UnitOfReactivePower", "UnitOfSoundPressure", "IrTextSensorType",
    # The voice assistant and the speaker.
    "VoiceAssistantEvent", "VoiceAssistantTimerEvent",
    "MediaPlayerCommand", "MediaPlayerState", "MediaPlayerFormatPurpose",
    # The music player's pairing token.
    "SendspinPairingTokenStatus",
]


def extract_blocks(text, keyword):
    """Returns {name: block_text} for every `keyword Name { ... }` block."""
    blocks = {}
    pattern = re.compile(rf"^{keyword} (\w+) \{{", re.M)
    for match in pattern.finditer(text):
        name = match.group(1)
        start = match.start()
        # Walk braces to find the end of the block.
        depth = 0
        i = text.index("{", start)
        while i < len(text):
            if text[i] == "{":
                depth += 1
            elif text[i] == "}":
                depth -= 1
                if depth == 0:
                    break
            i += 1
        blocks[name] = text[start:i + 1]
    return blocks


def main():
    src = open(SRC).read()

    # Drop the custom options and the service block: the generated Go code
    # does not need ESPHome's compile-time metadata, and the service
    # definition would pull in the whole file.
    body = src

    blocks = extract_blocks(body, "message")
    enums = extract_blocks(body, "enum")

    # Pull in every message and enum a kept one refers to. The device info
    # response names the area and the serial proxy, and the capabilities
    # response names the voice assistant capabilities, so they come along
    # whether the kiosk uses them or not.
    kept_messages = set(NEEDED)
    changed = True
    while changed:
        changed = False
        for name in list(kept_messages):
            if name not in blocks:
                continue
            for ref in re.findall(r"\b([A-Z]\w+)\b", blocks[name]):
                if ref in blocks and ref not in kept_messages:
                    kept_messages.add(ref)
                    changed = True

    kept_enums = set(ENUMS)
    changed = True
    while changed:
        changed = False
        for name in list(kept_messages) + list(kept_enums):
            source = blocks.get(name) or enums.get(name) or ""
            for ref in re.findall(r"\b([A-Z]\w+)\b", source):
                if ref in enums and ref not in kept_enums:
                    kept_enums.add(ref)
                    changed = True

    missing = [n for n in NEEDED if n not in blocks]
    if missing:
        print("WARN missing messages:", missing, file=sys.stderr)

    out = ['syntax = "proto3";', ""]
    out.append("// Trimmed from the ESPHome api.proto. Only the messages the Home Panel")
    out.append("// device side needs are kept.")
    out.append("")
    out.append("package esphome;")
    out.append("")
    out.append("option go_package = \"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome;esphome\";")
    out.append("")

    for name in sorted(kept_enums):
        if name not in enums:
            continue
        out.append(enums[name])
        out.append("")

    for name in sorted(kept_messages):
        if name not in blocks:
            continue
        block = blocks[name]
        # Strip ESPHome's custom options: the message-level ones and the
        # field-level ones. They are compile-time metadata for the C++
        # generator and mean nothing to protoc, and protoc refuses to
        # compile a file that uses an option it does not know.
        block = re.sub(r"^\s*option \([^)]*\) = [^;]*;\s*$", "", block, flags=re.M)
        block = re.sub(r"\s*\[[^\]]*\]", "", block)
        out.append(block)
        out.append("")

    open(DST, "w").write("\n".join(out))
    print(f"wrote {DST}: {len(kept_messages)} messages, {len(kept_enums)} enums")


if __name__ == "__main__":
    main()

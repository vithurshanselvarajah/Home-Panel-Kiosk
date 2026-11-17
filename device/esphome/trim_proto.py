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


NEEDED = [

    "HelloRequest", "HelloResponse",
    "DisconnectRequest", "DisconnectResponse",
    "PingRequest", "PingResponse",

    "DeviceInfoRequest", "DeviceInfoResponse",
    "DeviceCapabilitiesRequest", "DeviceCapabilitiesResponse",

    "ListEntitiesRequest", "ListEntitiesDoneResponse",
    "ListEntitiesBinarySensorResponse", "ListEntitiesButtonResponse",
    "ListEntitiesNumberResponse", "ListEntitiesSelectResponse",
    "ListEntitiesSensorResponse", "ListEntitiesSwitchResponse",
    "ListEntitiesTextResponse", "ListEntitiesTextSensorResponse",

    "ListEntitiesServicesResponse", "ListEntitiesServicesArgument",
    "ExecuteServiceRequest", "ExecuteServiceArgument", "ExecuteServiceResponse",

    "SubscribeStatesRequest",
    "BinarySensorStateResponse", "NumberStateResponse", "SelectStateResponse",
    "SensorStateResponse", "SwitchStateResponse", "TextStateResponse",
    "TextSensorStateResponse",




    "ButtonCommandRequest", "NumberCommandRequest",
    "SelectCommandRequest", "SwitchCommandRequest", "TextCommandRequest",

    "HomeassistantActionRequest", "HomeassistantServiceMap",
    "SubscribeHomeassistantServicesRequest",
    "SubscribeHomeAssistantStatesRequest", "SubscribeHomeAssistantStateResponse",
    "HomeAssistantStateResponse",





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





    "ListEntitiesMediaPlayerResponse", "MediaPlayerSupportedFormat",
    "MediaPlayerCommandRequest", "MediaPlayerStateResponse",

    "ListEntitiesEventResponse",





    "SendspinPairingTokenRequest", "SendspinPairingTokenResponse",
]


ENUMS = [
    "EntityCategory", "ServiceArgType", "SupportsResponseType",
    "SensorStateClass", "TextMode", "NumberMode", "DeviceClass",
    "UnitOfMass", "UnitOfLength", "UnitOfTemperature", "UnitOfPressure",
    "UnitOfPower", "UnitOfEnergy", "UnitOfVolume", "UnitOfFrequency",
    "UnitOfTime", "UnitOfElectricCurrent", "UnitOfConductivity",
    "UnitOfMagneticField", "UnitOfAngle", "UnitOfRotation", "UnitOfSpeed",
    "UnitOfDistance", "UnitOfDataRate", "UnitOfApparentPower",
    "UnitOfReactivePower", "UnitOfSoundPressure", "IrTextSensorType",

    "VoiceAssistantEvent", "VoiceAssistantTimerEvent",
    "MediaPlayerCommand", "MediaPlayerState", "MediaPlayerFormatPurpose",

    "SendspinPairingTokenStatus",
]


def extract_blocks(text, keyword):
    """Returns {name: block_text} for every `keyword Name { ... }` block."""
    blocks = {}
    pattern = re.compile(rf"^{keyword} (\w+) \{{", re.M)
    for match in pattern.finditer(text):
        name = match.group(1)
        start = match.start()

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




    body = src

    blocks = extract_blocks(body, "message")
    enums = extract_blocks(body, "enum")





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




        block = re.sub(r"^\s*option \([^)]*\) = [^;]*;\s*$", "", block, flags=re.M)
        block = re.sub(r"\s*\[[^\]]*\]", "", block)
        out.append(block)
        out.append("")

    open(DST, "w").write("\n".join(out))
    print(f"wrote {DST}: {len(kept_messages)} messages, {len(kept_enums)} enums")


if __name__ == "__main__":
    main()

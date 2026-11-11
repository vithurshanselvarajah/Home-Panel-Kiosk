#!/bin/sh
# Regenerates the ESPHome protocol types.
#
# The upstream protocol file is kept in device/esphome/api.proto.upstream.
# It is trimmed down to the messages the kiosk needs, and the Go types are
# generated from the trimmed file.
#
# It needs protoc, which is not part of the Go toolchain. The Python
# package grpcio-tools carries one:
#
#   pip3 install grpcio-tools
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#
# After regenerating, run the wire check. It compares the bytes this
# side produces against the bytes aioesphomeapi produces, and a
# difference means the protocol does not match what Home Assistant
# speaks.
#
#   python3 tools/check_wire.py
#   go test ./device/esphome/
set -e

root=$(cd "$(dirname "$0")/.." && pwd)
proto="$root/device/esphome"

cd "$proto"
python3 trim_proto.py

python3 -m grpc_tools.protoc -I. \
    --go_out=. \
    --go_opt=paths=source_relative \
    --go_opt=Mesphome_api.proto=github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome \
    esphome_api.proto

echo "the types are regenerated. Run the wire check before committing."

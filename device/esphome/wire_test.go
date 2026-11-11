package esphome

import (
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/proto"
)

// The bytes here were produced by aioesphomeapi's own generated types.
//
// The protocol is shared with Home Assistant, and Home Assistant speaks it
// with aioesphomeapi. A difference between the two encodings would be a
// silent bug: the message would parse, the field would be wrong, and the
// symptom would be an entity that never changes.
//
// The Python side of this check is tools/check_wire.py, which encodes the
// same values with aioesphomeapi and compares them against the same list.
var wireVectors = []struct {
	name  string
	msg   proto.Message
	bytes string
}{
	{
		"HelloRequest",
		&HelloRequest{ClientInfo: "Home Assistant", ApiVersionMajor: 1, ApiVersionMinor: 16},
		"0a0e486f6d6520417373697374616e7410011810",
	},
	{
		"HelloResponse",
		&HelloResponse{ApiVersionMajor: 1, ApiVersionMinor: 16, ServerInfo: "Home Panel"},
		"080110101a0a486f6d652050616e656c",
	},
	{
		"ListEntitiesSwitchResponse",
		&ListEntitiesSwitchResponse{Key: 42, Name: "Screen", ObjectId: "screen", Icon: "mdi:monitor"},
		"0a0673637265656e152a0000001a0653637265656e2a0b6d64693a6d6f6e69746f72",
	},
	{
		"ListEntitiesNumberResponse",
		&ListEntitiesNumberResponse{Key: 43, Name: "Brightness", ObjectId: "brightness", MinValue: 0, MaxValue: 100, Step: 1, Mode: 1},
		"0a0a6272696768746e657373152b0000001a0a4272696768746e6573733d0000c842450000803f6001",
	},
	{
		"ListEntitiesSelectResponse",
		&ListEntitiesSelectResponse{Key: 44, Name: "View", ObjectId: "view", Options: []string{"dashboard", "slideshow"}},
		"0a0476696577152c0000001a0456696577320964617368626f6172643209736c69646573686f77",
	},
	{
		"ListEntitiesTextResponse",
		&ListEntitiesTextResponse{Key: 45, Name: "Notification", ObjectId: "notification", Mode: 0},
		"0a0c6e6f74696669636174696f6e152d0000001a0c4e6f74696669636174696f6e",
	},
	{
		"ListEntitiesButtonResponse",
		&ListEntitiesButtonResponse{Key: 46, Name: "Wake", ObjectId: "wake", DeviceClass: "restart"},
		"0a0477616b65152e0000001a0457616b65420772657374617274",
	},
	{
		"SwitchCommandRequest",
		&SwitchCommandRequest{Key: 42, State: true},
		"0d2a0000001001",
	},
	{
		"NumberCommandRequest",
		&NumberCommandRequest{Key: 43, State: 45},
		"0d2b0000001500003442",
	},
	{
		"SelectCommandRequest",
		&SelectCommandRequest{Key: 44, State: "slideshow"},
		"0d2c0000001209736c69646573686f77",
	},
	{
		"TextCommandRequest",
		&TextCommandRequest{Key: 45, State: "hello"},
		"0d2d000000120568656c6c6f",
	},
	{
		"ButtonCommandRequest",
		&ButtonCommandRequest{Key: 46},
		"0d2e000000",
	},
	{
		"SwitchStateResponse",
		&SwitchStateResponse{Key: 42, State: true},
		"0d2a0000001001",
	},
	{
		"NumberStateResponse",
		&NumberStateResponse{Key: 43, State: 45},
		"0d2b0000001500003442",
	},
	{
		"SelectStateResponse",
		&SelectStateResponse{Key: 44, State: "dashboard"},
		"0d2c000000120964617368626f617264",
	},
	{
		"TextStateResponse",
		&TextStateResponse{Key: 45, State: "hi"},
		"0d2d00000012026869",
	},
	{
		"SensorStateResponse",
		&SensorStateResponse{Key: 47, State: 42.5},
		"0d2f0000001500002a42",
	},
	{
		"BinarySensorStateResponse",
		&BinarySensorStateResponse{Key: 48, State: true},
		"0d300000001001",
	},
	{
		"ListEntitiesServicesResponse",
		&ListEntitiesServicesResponse{Name: "display", Key: 99, Description: "d"},
		"0a07646973706c617915630000002a0164",
	},
	{
		"ExecuteServiceRequest",
		&ExecuteServiceRequest{Key: 99},
		"0d63000000",
	},
	{
		"HomeassistantActionRequest",
		&HomeassistantActionRequest{Service: "light.toggle"},
		"0a0c6c696768742e746f67676c65",
	},
	{
		"HomeAssistantStateResponse",
		&HomeAssistantStateResponse{EntityId: "sensor.x", State: "1", Attribute: "a"},
		"0a0873656e736f722e781201311a0161",
	},
	{"PingRequest", &PingRequest{}, ""},
	{"DisconnectRequest", &DisconnectRequest{}, ""},
}

// TestWireVectorsMatchAioesphomeapi checks the encoding against the bytes
// aioesphomeapi produces for the same values.
//
// The expected bytes were taken from the Python side of the check, which
// encodes with aioesphomeapi's own generated types. Both sides speak the
// same protocol, so both must produce the same bytes.
func TestWireVectorsMatchAioesphomeapi(t *testing.T) {
	for _, vector := range wireVectors {
		encoded, err := proto.Marshal(vector.msg)
		if err != nil {
			t.Fatalf("%s: %v", vector.name, err)
		}
		got := hex.EncodeToString(encoded)
		want := vector.bytes
		if want != "" && got != want {
			t.Errorf("%s:\n got %s\nwant %s", vector.name, got, want)
		}
	}
}

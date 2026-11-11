package esphome

import (
	"testing"
)

// TestServiceArgumentsAreChecked checks that the arguments are checked
// before the service runs.
//
// An argument of the wrong kind is a mistake, and passing it on is
// passing the mistake on.
func TestServiceArgumentsAreChecked(t *testing.T) {
	called := false
	service := &Service{
		Name: "send_notification",
		Args: []ServiceArg{
			{Name: "message", Kind: ServiceArgType_SERVICE_ARG_TYPE_STRING},
			{Name: "duration", Kind: ServiceArgType_SERVICE_ARG_TYPE_INT, Optional: true, Default: "8000"},
		},
	}
	service.OnCall(func(args map[string]any) error {
		called = true
		if args["message"] != "hello" {
			t.Errorf("the message is %v, it should be hello", args["message"])
		}
		return nil
	})

	// A string where a number is wanted.
	if err := service.Call(map[string]any{"message": "hello", "duration": "soon"}); err == nil {
		t.Errorf("the service was called with a piece of text where a number belongs")
	}
	if called {
		t.Errorf("the service ran despite the argument being wrong")
	}

	// The argument that is missing.
	if err := service.Call(map[string]any{}); err == nil {
		t.Errorf("the service was called without the argument it needs")
	}

	// A number where a number is wanted.
	if err := service.Call(map[string]any{"message": "hello", "duration": 5000}); err != nil {
		t.Errorf("the service was refused: %v", err)
	}
	if !called {
		t.Errorf("the service did not run")
	}
}

// TestServiceOptionalArgumentsAreDefaulted checks that an argument
// that is not given gets its default.
func TestServiceOptionalArgumentsAreDefaulted(t *testing.T) {
	service := &Service{
		Name: "open_url",
		Args: []ServiceArg{
			{Name: "url", Kind: ServiceArgType_SERVICE_ARG_TYPE_STRING},
			{Name: "duration", Kind: ServiceArgType_SERVICE_ARG_TYPE_INT, Optional: true, Default: "30000"},
		},
	}
	var got map[string]any
	service.OnCall(func(args map[string]any) error {
		got = args
		return nil
	})
	if err := service.Call(map[string]any{"url": "http://example.com"}); err != nil {
		t.Fatalf("the service was refused: %v", err)
	}
	if got["duration"] != "30000" {
		t.Errorf("the duration is %v, it should be the default 30000", got["duration"])
	}
}

// TestServerListsServices checks that the services are listed with the
// entities.
func TestServerListsServices(t *testing.T) {
	services := NewServiceRegistry()
	services.Add(&Service{
		Name:        "send_notification",
		Description: "Shows a notification on the panel",
		Args: []ServiceArg{
			{Name: "message", Kind: ServiceArgType_SERVICE_ARG_TYPE_STRING, Description: "The text"},
		},
	})
	server := NewServer(ServerOptions{
		Name: "home-panel", FriendlyName: "Home Panel", MAC: "aabbccddeeff",
		Version: "0.0.2", Registry: NewRegistry(), Services: services,
	})
	message := server.serviceMessage(services.All()[0])
	if message.Name != "send_notification" {
		t.Errorf("the service is named %q", message.Name)
	}
	if len(message.Args) != 1 {
		t.Fatalf("the service has %d arguments, it should have 1", len(message.Args))
	}
	if message.Args[0].Name != "message" {
		t.Errorf("the argument is named %q", message.Args[0].Name)
	}
	if message.Args[0].Type != ServiceArgType_SERVICE_ARG_TYPE_STRING {
		t.Errorf("the argument is of the wrong kind: %v", message.Args[0].Type)
	}
}

// TestServiceNamesAreUnique checks that two services do not share a key.
//
// The key is what the service is called by, so two services with the
// same key would mean one of them could never be called.
func TestServiceNamesAreUnique(t *testing.T) {
	registry := NewServiceRegistry()
	first := registry.Add(&Service{Name: "send_notification"})
	second := registry.Add(&Service{Name: "open_url"})
	if first.Key == second.Key {
		t.Errorf("two services have the same key: %d", first.Key)
	}
	if registry.ByKey(first.Key) != first {
		t.Errorf("the service was not found by its key")
	}
}

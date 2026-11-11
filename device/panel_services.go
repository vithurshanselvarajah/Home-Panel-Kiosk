package main

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// The panel's services.
//
// A notification and an address are commands with something in them,
// rather than values that sit there after they have been typed. The
// protocol has a message for them, and Home Assistant turns each one
// into a service of its own.
//
// So they are services and not text boxes. A text box is a value that
// stays in the entity after it has been set, and a command is not a
// value: it is done the moment it is asked for.

// panelServices makes the services the panel offers.
func panelServices(send func(ControlCommand)) *esphome.ServiceRegistry {
	registry := esphome.NewServiceRegistry()

	registry.Add(&esphome.Service{
		Name:        "send_notification",
		Description: "Shows a notification on the panel",
		Args: []esphome.ServiceArg{
			{
				Name:        "message",
				Kind:        esphome.ServiceArgType_SERVICE_ARG_TYPE_STRING,
				Description: "The text of the notification",
			},
			{
				Name:        "title",
				Kind:        esphome.ServiceArgType_SERVICE_ARG_TYPE_STRING,
				Description: "The title of the notification",
				Optional:    true,
				Default:     "Home Panel",
			},
			{
				Name:        "duration",
				Kind:        esphome.ServiceArgType_SERVICE_ARG_TYPE_INT,
				Description: "How long the notification stays, in milliseconds",
				Optional:    true,
				Default:     "8000",
			},
		},
	}).OnCall(func(args map[string]any) error {
		send(ControlCommand{
			Action:   "notification",
			Message:  stringArg(args, "message"),
			Title:    stringArg(args, "title"),
			Duration: intArg(args, "duration"),
		})
		return nil
	})

	registry.Add(&esphome.Service{
		Name:        "open_url",
		Description: "Shows an address on the panel",
		Args: []esphome.ServiceArg{
			{
				Name:        "url",
				Kind:        esphome.ServiceArgType_SERVICE_ARG_TYPE_STRING,
				Description: "The address to show",
			},
			{
				Name:        "duration",
				Kind:        esphome.ServiceArgType_SERVICE_ARG_TYPE_INT,
				Description: "How long the address stays, in milliseconds; zero means until the panel is told otherwise",
				Optional:    true,
				Default:     "30000",
			},
		},
	}).OnCall(func(args map[string]any) error {
		send(ControlCommand{
			Action:   "open_url",
			URL:      stringArg(args, "url"),
			Duration: intArg(args, "duration"),
		})
		return nil
	})

	return registry
}

// stringArg reads a piece of text out of the arguments.
func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}

// intArg reads a whole number out of the arguments.
func intArg(args map[string]any, name string) int {
	switch number := args[name].(type) {
	case int:
		return number
	case float64:
		return int(number)
	}
	return 0
}

package esphome

import (
	"fmt"
	"sync"
)

// A service is something Home Assistant can ask the device to do that
// takes an argument.
//
// A notification and an address are the two: they are commands with
// something in them, rather than a value that sits there after it has
// been typed. The protocol has a message for them, and Home Assistant
// turns each one into a service of its own.
//
// The service is named after the device, so two panels on one network
// do not have a service with the same name.
type Service struct {
	// Key identifies the service on the wire.
	Key uint32
	// Name is the name of the service, without the device in front of
	// it.
	Name string
	// Description says what the service does.
	Description string
	// Args are the arguments the service takes.
	Args []ServiceArg
	// onCall is called when the service is called.
	onCall func(args map[string]any) error
	// mu guards the callback.
	mu sync.Mutex
}

// ServiceArg is one argument of a service.
//
// The kind is what Home Assistant uses to decide what to ask for: a
// string is a box, a number is a number, and a boolean is a toggle.
type ServiceArg struct {
	// Name is the name of the argument.
	Name string
	// Kind is what kind of value it takes.
	//
	// It is the protocol's own kind, so the argument needs no
	// translating when it goes over the wire.
	Kind ServiceArgType
	// Description says what the argument is for.
	Description string
	// Optional says whether the service can be called without it.
	Optional bool
	// Default is the value used when it is not given.
	Default string
}

// ServiceRegistry holds the services the device offers.
type ServiceRegistry struct {
	mu       sync.RWMutex
	services []*Service
	byKey    map[uint32]*Service
	nextKey  uint32
}

// NewServiceRegistry makes an empty registry.
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{byKey: map[uint32]*Service{}, nextKey: 1}
}

// Add registers a service and gives it its key.
func (r *ServiceRegistry) Add(service *Service) *Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	service.Key = r.nextKey
	r.nextKey++
	r.services = append(r.services, service)
	r.byKey[service.Key] = service
	return service
}

// All returns the services in the order they were added.
func (r *ServiceRegistry) All() []*Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Service, len(r.services))
	copy(out, r.services)
	return out
}

// ByKey finds a service by its key.
func (r *ServiceRegistry) ByKey(key uint32) *Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byKey[key]
}

// OnCall sets what is called when the service is called.
func (s *Service) OnCall(fn func(args map[string]any) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCall = fn
}

// Call runs the service with the arguments given.
//
// The arguments are checked against what the service takes, because an
// argument of the wrong kind is a mistake and not something to pass on.
func (s *Service) Call(args map[string]any) error {
	s.mu.Lock()
	handler := s.onCall
	s.mu.Unlock()
	if handler == nil {
		return fmt.Errorf("esphome: the service %q does nothing", s.Name)
	}
	checked, err := s.checkArgs(args)
	if err != nil {
		return err
	}
	return handler(checked)
}

// checkArgs checks the arguments and fills in the ones that are missing.
func (s *Service) checkArgs(args map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(s.Args))
	for _, arg := range s.Args {
		value, given := args[arg.Name]
		if !given || value == nil {
			if arg.Optional {
				if arg.Default != "" {
					out[arg.Name] = arg.Default
				}
				continue
			}
			return nil, fmt.Errorf("esphome: the service %q needs the argument %q", s.Name, arg.Name)
		}
		converted, err := convertArg(arg, value)
		if err != nil {
			return nil, err
		}
		out[arg.Name] = converted
	}
	return out, nil
}

// convertArg turns a value into the kind the argument takes.
func convertArg(arg ServiceArg, value any) (any, error) {
	switch arg.Kind {
	case ServiceArgType_SERVICE_ARG_TYPE_STRING:
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("esphome: the argument %q is a piece of text", arg.Name)
		}
		return text, nil
	case ServiceArgType_SERVICE_ARG_TYPE_INT:
		switch number := value.(type) {
		case int:
			return number, nil
		case int32:
			return int(number), nil
		case int64:
			return int(number), nil
		case float32:
			return int(number), nil
		case float64:
			return int(number), nil
		}
		return nil, fmt.Errorf("esphome: the argument %q is a whole number", arg.Name)
	case ServiceArgType_SERVICE_ARG_TYPE_FLOAT:
		switch number := value.(type) {
		case int:
			return float64(number), nil
		case float32:
			return float64(number), nil
		case float64:
			return number, nil
		}
		return nil, fmt.Errorf("esphome: the argument %q is a number", arg.Name)
	case ServiceArgType_SERVICE_ARG_TYPE_BOOL:
		switch flag := value.(type) {
		case bool:
			return flag, nil
		case string:
			return flag == "true" || flag == "1" || flag == "on", nil
		}
		return nil, fmt.Errorf("esphome: the argument %q is true or false", arg.Name)
	}
	return value, nil
}

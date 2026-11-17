package esphome

import (
	"fmt"
	"sync"
)

type Service struct {
	Key uint32

	Name string

	Description string

	Args []ServiceArg

	onCall func(args map[string]any) error

	mu sync.Mutex
}

type ServiceArg struct {
	Name string

	Kind ServiceArgType

	Description string

	Optional bool

	Default string
}

type ServiceRegistry struct {
	mu       sync.RWMutex
	services []*Service
	byKey    map[uint32]*Service
	nextKey  uint32
}

func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{byKey: map[uint32]*Service{}, nextKey: 1}
}

func (r *ServiceRegistry) Add(service *Service) *Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	service.Key = r.nextKey
	r.nextKey++
	r.services = append(r.services, service)
	r.byKey[service.Key] = service
	return service
}

func (r *ServiceRegistry) All() []*Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Service, len(r.services))
	copy(out, r.services)
	return out
}

func (r *ServiceRegistry) ByKey(key uint32) *Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byKey[key]
}

func (s *Service) OnCall(fn func(args map[string]any) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCall = fn
}

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

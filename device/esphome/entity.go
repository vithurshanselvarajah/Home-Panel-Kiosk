package esphome

import (
	"fmt"
	"sync"
)

type EntityKind int

const (
	KindSwitch EntityKind = iota

	KindNumber

	KindSelect

	KindText

	KindButton

	KindSensor

	KindTextSensor

	KindBinarySensor

	KindMediaPlayer
)

func (k EntityKind) String() string {
	switch k {
	case KindSwitch:
		return "switch"
	case KindNumber:
		return "number"
	case KindSelect:
		return "select"
	case KindText:
		return "text"
	case KindButton:
		return "button"
	case KindSensor:
		return "sensor"
	case KindTextSensor:
		return "text_sensor"
	case KindBinarySensor:
		return "binary_sensor"
	case KindMediaPlayer:
		return "media_player"
	}
	return "unknown"
}

type Entity struct {
	Key uint32

	Kind EntityKind

	Name string

	ObjectID string

	Icon string

	Unit string

	DeviceClass string

	Category EntityCategory

	Options []string

	Min, Max, Step float64

	MinLength, MaxLength uint32

	Mode TextInputMode

	State EntityValue

	onCommand func(value EntityValue)

	mu sync.Mutex
}

type TextInputMode int32

const (
	TextInputPlain TextInputMode = 0

	TextInputPassword TextInputMode = 1
)

type EntityValue struct {
	Kind EntityKind

	Number float64

	Bool bool

	Text string
}

func (v EntityValue) String() string {
	switch v.Kind {
	case KindSwitch:
		if v.Bool {
			return "ON"
		}
		return "OFF"
	case KindNumber:
		return formatFloat(v.Number)
	case KindSelect, KindText:
		return v.Text
	case KindSensor:
		return formatFloat(v.Number)
	case KindTextSensor:
		return v.Text
	case KindBinarySensor:
		if v.Bool {
			return "ON"
		}
		return "OFF"
	}
	return ""
}

type Registry struct {
	mu       sync.RWMutex
	entities []*Entity
	byKey    map[uint32]*Entity
	nextKey  uint32

	onChange func(*Entity)
}

func NewRegistry() *Registry {
	return &Registry{byKey: map[uint32]*Entity{}, nextKey: 1}
}

func (r *Registry) OnChange(fn func(*Entity)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = fn
}

func (r *Registry) Add(entity *Entity) *Entity {
	r.mu.Lock()
	defer r.mu.Unlock()
	entity.Key = r.nextKey
	r.nextKey++
	r.entities = append(r.entities, entity)
	r.byKey[entity.Key] = entity
	return entity
}

func (r *Registry) NextKeyValue() uint32 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nextKey
}

func (r *Registry) All() []*Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Entity, len(r.entities))
	copy(out, r.entities)
	return out
}

func (r *Registry) ByKey(key uint32) *Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byKey[key]
}

func (r *Registry) SetState(key uint32, value EntityValue) {
	r.mu.Lock()
	entity := r.byKey[key]
	if entity == nil {
		r.mu.Unlock()
		return
	}
	if entity.State == value {
		r.mu.Unlock()
		return
	}
	entity.State = value
	changed := entity
	notify := r.onChange
	r.mu.Unlock()

	if notify != nil {
		notify(changed)
	}
}

func (r *Registry) HandleCommand(key uint32, value EntityValue) error {
	r.mu.RLock()
	entity := r.byKey[key]
	r.mu.RUnlock()
	if entity == nil {
		return fmt.Errorf("esphome: there is no entity with the key %d", key)
	}
	if err := entity.accept(value); err != nil {
		return err
	}
	entity.mu.Lock()

	handler := entity.onCommand
	entity.mu.Unlock()
	if handler != nil {
		handler(value)
	}
	return nil
}

func (e *Entity) accept(value EntityValue) error {
	switch e.Kind {
	case KindNumber:
		if value.Number < e.Min || value.Number > e.Max {
			return fmt.Errorf("esphome: %s must be between %s and %s", e.Name, formatFloat(e.Min), formatFloat(e.Max))
		}
	case KindSelect:
		for _, option := range e.Options {
			if option == value.Text {
				return nil
			}
		}
		return fmt.Errorf("esphome: %s is not one of the options", e.Name)
	}
	return nil
}

func (e *Entity) OnCommand(fn func(value EntityValue)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onCommand = fn
}

func (e *Entity) StateOf() EntityValue {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.State
}

package esphome

import (
	"fmt"
	"sync"
)

// The kinds of entity the kiosk offers.
//
// The kinds map onto the protocol's listing messages. A kind is not a
// general purpose value: each one has its own message on the wire, and
// the protocol has no way to describe an entity that is not one of them.
type EntityKind int

const (
	// KindSwitch is a two state entity.
	KindSwitch EntityKind = iota
	// KindNumber is a number with a range.
	KindNumber
	// KindSelect is a choice between a fixed set of options.
	KindSelect
	// KindText is a short piece of text.
	KindText
	// KindButton is something that is pressed.
	KindButton
	// KindSensor is a number that is read, not written.
	KindSensor
	// KindTextSensor is a string that is read, not written.
	KindTextSensor
	// KindBinarySensor is true or false that is read, not written.
	KindBinarySensor
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
	}
	return "unknown"
}

// Entity is one thing the kiosk offers to Home Assistant.
//
// The kiosk is a display with a few controls on it. Each of them is an
// entity, so Home Assistant can show it and act on it.
type Entity struct {
	// Key identifies the entity on the wire. It is assigned when the
	// entity is registered and never changes.
	Key uint32
	// Kind is what kind of entity it is.
	Kind EntityKind
	// Name is the name Home Assistant shows.
	Name string
	// ObjectID is the name Home Assistant uses in the entity id.
	ObjectID string
	// Icon is the icon Home Assistant shows, if there is one.
	Icon string
	// Unit is the unit of measurement, for a sensor.
	Unit string
	// DeviceClass is the class Home Assistant groups the entity by.
	DeviceClass string
	// Options are the choices, for a select.
	Options []string
	// Min, Max and Step are the range, for a number.
	Min, Max, Step float64
	// MinLength and MaxLength are how long the text may be, for a text.
	//
	// The protocol has no "unlimited", so a text that may be any length
	// says so here rather than saying nothing: a length of zero on the
	// wire means a text that may not be typed into at all.
	MinLength, MaxLength uint32
	// Mode is the behaviour of a text entity: a plain field or a field
	// that hides what is typed.
	Mode TextInputMode
	// State is the current value.
	State EntityValue
	// onCommand is called when Home Assistant changes the entity.
	onCommand func(value EntityValue)
	// mu guards the state and the callback.
	mu sync.Mutex
}

// TextInputMode says how a text entity behaves.
type TextInputMode int32

const (
	// TextInputPlain is a plain field.
	TextInputPlain TextInputMode = 0
	// TextInputPassword hides what is typed.
	TextInputPassword TextInputMode = 1
)

// EntityValue is the value of an entity.
//
// The kinds of entity hold different things, so the value is a union
// with a flag saying which part is meaningful. A number is a float, a
// switch and a button are booleans, and a select and a text are
// strings.
type EntityValue struct {
	// Kind says which of the fields below is meaningful.
	Kind EntityKind
	// Number is the value, for a number and for a sensor.
	Number float64
	// Bool is the value, for a switch.
	Bool bool
	// Text is the value, for a select, for a text and for a text
	// sensor.
	Text string
}

// String renders the value the way it is sent on the wire.
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

// Registry holds the entities the kiosk offers.
//
// It is the one place that knows what the kiosk can do. The protocol
// layer asks it what to list and tells it when something changes.
type Registry struct {
	mu       sync.RWMutex
	entities []*Entity
	byKey    map[uint32]*Entity
	nextKey  uint32
	// onChange is called when an entity's state changes, so the change
	// can be pushed to Home Assistant.
	onChange func(*Entity)
}

// NewRegistry makes an empty registry.
func NewRegistry() *Registry {
	return &Registry{byKey: map[uint32]*Entity{}, nextKey: 1}
}

// OnChange sets the callback that is called when a state changes.
func (r *Registry) OnChange(fn func(*Entity)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = fn
}

// Add registers an entity and gives it its key.
//
// The key is what Home Assistant uses to name the entity, so it is
// assigned once and never reused. The order the entities are added in
// is the order they are listed in, and Home Assistant shows them in
// that order.
func (r *Registry) Add(entity *Entity) *Entity {
	r.mu.Lock()
	defer r.mu.Unlock()
	entity.Key = r.nextKey
	r.nextKey++
	r.entities = append(r.entities, entity)
	r.byKey[entity.Key] = entity
	return entity
}

// All returns the entities in the order they were added.
func (r *Registry) All() []*Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Entity, len(r.entities))
	copy(out, r.entities)
	return out
}

// ByKey finds an entity by its key.
func (r *Registry) ByKey(key uint32) *Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byKey[key]
}

// SetState changes the value of an entity and tells the listener.
//
// The change is pushed to Home Assistant, so this is how the kiosk
// reports what it is doing.
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

// HandleCommand applies a command from Home Assistant.
//
// The value is checked against what the entity allows before it is
// applied, because a number outside its range or an option that does
// not exist is a mistake and not something to pass on.
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
	// The state is not changed here.
	//
	// The state is what the device is doing, and the command is what
	// Home Assistant asked for. The two are the same most of the time,
	// but not always: the panel may be in a window when Home Assistant
	// asks for a fullscreen, because the panel cannot change its own
	// window at once.
	//
	// So the command is passed on and the state is set when the device
	// reports what it is actually doing. Setting it here would mean the
	// device's own report was ignored, because the value would be the
	// same and a change that is the same is not a change.
	handler := entity.onCommand
	entity.mu.Unlock()
	if handler != nil {
		handler(value)
	}
	return nil
}

// accept checks that a value is one the entity allows.
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

// OnCommand sets what is called when Home Assistant changes the entity.
func (e *Entity) OnCommand(fn func(value EntityValue)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onCommand = fn
}

// StateOf reads the current value of an entity.
func (e *Entity) StateOf() EntityValue {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.State
}

package cordis

import "reflect"

// Setting is a typed key for configuration that callers attach to a scope
// for one service. The provider reads it from the caller's scope. Declare
// settings next to the service they belong to.
type Setting[O any] struct{ id settingID }

type settingID struct {
	service string
	typ     reflect.Type
}

// NewSetting returns a setting of type O for service.
func NewSetting[O any](service ServiceKey) Setting[O] {
	return Setting[O]{id: settingID{service: service.serviceName(), typ: reflect.TypeFor[O]()}}
}

// Service returns the name of the service the setting belongs to.
func (k Setting[O]) Service() string { return k.id.service }

type setting struct {
	id     settingID
	value  any
	parent *setting
}

// With derives a scope that carries value for this setting. Values
// accumulate: inner scopes add to, and do not replace, outer ones.
func (k Setting[O]) With(s *Scope, value O) *Scope {
	derived := s.derive()
	derived.settings = &setting{id: k.id, value: value, parent: s.settings}
	return derived
}

// Default returns a requirement on the setting's service that also carries
// value as the instance's default for this setting.
func (k Setting[O]) Default(value O) Requirement {
	id := k.id
	return required{name: id.service, setting: &id, value: value}
}

// Values returns the settings that apply to s, lowest precedence first: the
// default declared by the nearest instance that requires the service, then
// the values attached to the scope chain from outermost to innermost. The
// default is not looked up across an isolation boundary of the service.
func (k Setting[O]) Values(s *Scope) []O {
	var values []O
	a := s.act.node.app
	a.mu.Lock()
	realm := realmOf(s, k.id.service)
search:
	for act := s.act; act != nil; act = act.node.owner {
		if realmOf(act.scope, k.id.service) != realm {
			break
		}
		declared := false
		for _, req := range act.node.requires {
			if req.name != k.id.service {
				continue
			}
			declared = true
			if req.setting != nil && *req.setting == k.id {
				value, _ := req.value.(O)
				values = append(values, value)
			}
		}
		if declared {
			break search
		}
	}
	a.mu.Unlock()
	var chain []O
	for entry := s.settings; entry != nil; entry = entry.parent {
		if entry.id == k.id {
			value, _ := entry.value.(O)
			chain = append(chain, value)
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		values = append(values, chain[i])
	}
	return values
}

package contract

// Consumer binds an explicit routing range to a local generated method contract.
// Contract identifies the consumer's methods, not the provider's complete minor.
type Consumer struct {
	Requirement Requirement `json:"requirement"`
	Contract    string      `json:"contract"`
}

func (c Consumer) Check(name string, major, minor uint32, fingerprint string) error {
	r := c.Requirement
	if r.Name != name || r.Major != major || r.MinMinor > r.MaxMinor || minor < r.MinMinor || minor > r.MaxMinor || fingerprint == "" || c.Contract != fingerprint {
		return Fail(Incompatible, "consumer range or method contract does not match generated client")
	}
	return nil
}

func (d DefinitionSpec) Requirements() []Requirement {
	result := make([]Requirement, len(d.Requires))
	for i, consumer := range d.Requires {
		result[i] = consumer.Requirement
	}
	return result
}

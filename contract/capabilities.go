package contract

// Consumer is one requirement of a definition: the service and the range of
// minors it accepts. Contract is the fingerprint of the methods the consumer
// was generated against; packaging uses it to detect generated code that no
// longer matches its declaration. It is not compared with the provider.
type Consumer struct {
	Requirement Requirement `json:"requirement"`
	Contract    string      `json:"contract"`
}

// Requirements are the requirements of d's consumers.
func (d DefinitionSpec) Requirements() []Requirement {
	result := make([]Requirement, len(d.Requires))
	for i, consumer := range d.Requires {
		result[i] = consumer.Requirement
	}
	return result
}

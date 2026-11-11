package clidocs

const EndpointListID = "port.endpoint.list"
const EndpointAttributeID = "port.endpoint.attribute"
const GroupID = "port.group"
const GroupListID = "port.group.list"

type CommandDoc struct {
	ID                 string
	Path               []string
	Summary            string
	Description        string
	Category           string
	CanonicalReference string
	Arguments          []ArgumentDoc
	Flags              []FlagDoc
	Examples           []ExampleDoc
}

type ArgumentDoc struct {
	Name        string
	Description string
	Required    bool
	Variadic    bool
}

type FlagDoc struct {
	Name        string
	Shorthand   string
	Type        string
	Default     string
	Required    bool
	Description string
}

type ExampleDoc struct {
	Description string
	Invocation  string
	Output      string
}

func Command(id string) (CommandDoc, bool) {
	command, ok := Commands[id]
	return command, ok
}

func (c CommandDoc) Flag(name string) (FlagDoc, bool) {
	for _, flag := range c.Flags {
		if flag.Name == name {
			return flag, true
		}
	}
	return FlagDoc{}, false
}

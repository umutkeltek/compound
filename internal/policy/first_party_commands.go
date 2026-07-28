package policy

import "strings"

type FirstPartyCommandAdmission string

const (
	FirstPartyCommandRead     FirstPartyCommandAdmission = "read"
	FirstPartyCommandGoverned FirstPartyCommandAdmission = "governed"
	FirstPartyCommandHold     FirstPartyCommandAdmission = "hold"
)

// FirstPartyCommandCatalog is the shell monitor's read-only view of the
// executable Compound CLI registry. It deliberately knows command names and
// admission posture, not subcommand flags or vendor-specific behavior.
//
// A zero catalog fails closed. The Compound executable supplies its runtime
// registry at the hook seam, so policy cannot silently drift from a second
// handwritten shell allowlist.
type FirstPartyCommandCatalog struct {
	commands map[string]FirstPartyCommandAdmission
}

func NewFirstPartyCommandCatalog(commands map[string]FirstPartyCommandAdmission) FirstPartyCommandCatalog {
	catalog := FirstPartyCommandCatalog{
		commands: make(map[string]FirstPartyCommandAdmission, len(commands)),
	}
	for name, admission := range commands {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		switch admission {
		case FirstPartyCommandRead, FirstPartyCommandGoverned, FirstPartyCommandHold:
			catalog.commands[name] = admission
		default:
			catalog.commands[name] = FirstPartyCommandHold
		}
	}
	return catalog
}

func (c FirstPartyCommandCatalog) Lookup(name string) (FirstPartyCommandAdmission, bool) {
	admission, ok := c.commands[strings.ToLower(strings.TrimSpace(name))]
	return admission, ok
}

func (c FirstPartyCommandCatalog) Without(name string) FirstPartyCommandCatalog {
	next := make(map[string]FirstPartyCommandAdmission, len(c.commands))
	without := strings.ToLower(strings.TrimSpace(name))
	for command, admission := range c.commands {
		if command != without {
			next[command] = admission
		}
	}
	return NewFirstPartyCommandCatalog(next)
}

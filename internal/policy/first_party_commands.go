package policy

import "strings"

type FirstPartyCommandAdmission string

const (
	FirstPartyCommandRead     FirstPartyCommandAdmission = "read"
	FirstPartyCommandGoverned FirstPartyCommandAdmission = "governed"
	FirstPartyCommandInstall  FirstPartyCommandAdmission = "install"
	FirstPartyCommandHold     FirstPartyCommandAdmission = "hold"
)

type firstPartyCommandRule struct {
	admission  FirstPartyCommandAdmission
	subcommand map[string]FirstPartyCommandAdmission
}

// FirstPartyCommandCatalog is the shell monitor's read-only view of the
// executable Compound CLI registry. It deliberately knows command names and
// admission posture, not subcommand flags or vendor-specific behavior.
//
// A zero catalog fails closed. The Compound executable supplies its runtime
// registry at the hook seam, so policy cannot silently drift from a second
// handwritten shell allowlist.
type FirstPartyCommandCatalog struct {
	commands map[string]firstPartyCommandRule
}

func NewFirstPartyCommandCatalog(commands map[string]FirstPartyCommandAdmission) FirstPartyCommandCatalog {
	catalog := FirstPartyCommandCatalog{
		commands: make(map[string]firstPartyCommandRule, len(commands)),
	}
	for name, admission := range commands {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		catalog.commands[name] = firstPartyCommandRule{admission: validFirstPartyAdmission(admission)}
	}
	return catalog
}

func (c FirstPartyCommandCatalog) Lookup(name string) (FirstPartyCommandAdmission, bool) {
	rule, ok := c.commands[strings.ToLower(strings.TrimSpace(name))]
	return rule.admission, ok
}

func (c FirstPartyCommandCatalog) LookupSubcommand(
	name string,
	subcommand string,
) (FirstPartyCommandAdmission, bool) {
	rule, ok := c.commands[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return "", false
	}
	if admission, exists := rule.subcommand[strings.ToLower(strings.TrimSpace(subcommand))]; exists {
		return admission, true
	}
	return rule.admission, true
}

func (c FirstPartyCommandCatalog) WithSubcommands(
	name string,
	subcommands map[string]FirstPartyCommandAdmission,
) FirstPartyCommandCatalog {
	next := c.clone()
	commandName := strings.ToLower(strings.TrimSpace(name))
	rule, ok := next.commands[commandName]
	if !ok {
		return next
	}
	rule.subcommand = make(map[string]FirstPartyCommandAdmission, len(subcommands))
	for subcommand, admission := range subcommands {
		subcommand = strings.ToLower(strings.TrimSpace(subcommand))
		if subcommand != "" {
			rule.subcommand[subcommand] = validFirstPartyAdmission(admission)
		}
	}
	next.commands[commandName] = rule
	return next
}

func (c FirstPartyCommandCatalog) Without(name string) FirstPartyCommandCatalog {
	next := c.clone()
	without := strings.ToLower(strings.TrimSpace(name))
	delete(next.commands, without)
	return next
}

func (c FirstPartyCommandCatalog) clone() FirstPartyCommandCatalog {
	next := FirstPartyCommandCatalog{
		commands: make(map[string]firstPartyCommandRule, len(c.commands)),
	}
	for command, rule := range c.commands {
		subcommands := make(map[string]FirstPartyCommandAdmission, len(rule.subcommand))
		for subcommand, admission := range rule.subcommand {
			subcommands[subcommand] = admission
		}
		rule.subcommand = subcommands
		next.commands[command] = rule
	}
	return next
}

func validFirstPartyAdmission(admission FirstPartyCommandAdmission) FirstPartyCommandAdmission {
	switch admission {
	case FirstPartyCommandRead,
		FirstPartyCommandGoverned,
		FirstPartyCommandInstall,
		FirstPartyCommandHold:
		return admission
	default:
		return FirstPartyCommandHold
	}
}

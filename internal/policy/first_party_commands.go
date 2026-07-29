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
	admission       FirstPartyCommandAdmission
	subcommand      map[string]FirstPartyCommandAdmission
	subcommandFlags map[string]map[string]FirstPartyCommandAdmission
}

// FirstPartyCommandCatalog is the shell monitor's read-only view of the
// executable Compound CLI registry. It deliberately knows command names and
// admission posture, including the small number of flags that make an otherwise
// mutating subcommand provably read-only. It does not infer vendor behavior.
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
	return c.LookupSubcommandWithFlags(name, subcommand, nil)
}

func (c FirstPartyCommandCatalog) LookupSubcommandWithFlags(
	name string,
	subcommand string,
	args []string,
) (FirstPartyCommandAdmission, bool) {
	rule, ok := c.commands[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return "", false
	}
	subcommand = strings.ToLower(strings.TrimSpace(subcommand))
	if flagAdmissions := rule.subcommandFlags[subcommand]; len(flagAdmissions) > 0 {
		for _, arg := range args {
			if admission, exists := flagAdmissions[strings.ToLower(strings.TrimSpace(arg))]; exists {
				return admission, true
			}
		}
	}
	if admission, exists := rule.subcommand[subcommand]; exists {
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

func (c FirstPartyCommandCatalog) WithSubcommandFlagAdmission(
	name string,
	subcommand string,
	flag string,
	admission FirstPartyCommandAdmission,
) FirstPartyCommandCatalog {
	next := c.clone()
	commandName := strings.ToLower(strings.TrimSpace(name))
	rule, ok := next.commands[commandName]
	if !ok {
		return next
	}
	subcommand = strings.ToLower(strings.TrimSpace(subcommand))
	flag = strings.ToLower(strings.TrimSpace(flag))
	if subcommand == "" || flag == "" {
		return next
	}
	if rule.subcommandFlags == nil {
		rule.subcommandFlags = map[string]map[string]FirstPartyCommandAdmission{}
	}
	if rule.subcommandFlags[subcommand] == nil {
		rule.subcommandFlags[subcommand] = map[string]FirstPartyCommandAdmission{}
	}
	rule.subcommandFlags[subcommand][flag] = validFirstPartyAdmission(admission)
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
		subcommandFlags := make(map[string]map[string]FirstPartyCommandAdmission, len(rule.subcommandFlags))
		for subcommand, admissions := range rule.subcommandFlags {
			flags := make(map[string]FirstPartyCommandAdmission, len(admissions))
			for flag, admission := range admissions {
				flags[flag] = admission
			}
			subcommandFlags[subcommand] = flags
		}
		rule.subcommandFlags = subcommandFlags
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

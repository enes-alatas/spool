package hook

import "strings"

const killByPID = "kill your own process by the PID you recorded when you started it ($!)."

// checkKill refuses a kill aimed at PID -1, which signals every process
// the uid can reach: on a bare host, every loop's.
func checkKill(args []string) string {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--":
			return killsAll(args[i+1:])
		case arg == "-s" || arg == "-n":
			i++ // the signal
		case i == 0 && strings.HasPrefix(arg, "-"):
			// the signal, as -9 or -KILL
		default:
			return killsAll(args[i:])
		}
	}
	return ""
}

// killsAll refuses a list of kill targets that holds -1.
func killsAll(targets []string) string {
	for _, target := range targets {
		if target == "-1" {
			return "kill -1 signals every process of this user, which is every loop's on this host; " + killByPID
		}
	}
	return ""
}

// checkKillByName refuses a call that kills and also looks a PID up by
// name, port or file, as in kill $(pgrep -f x), pgrep x | xargs kill or
// kill $(lsof -t -i :8080): a name or a port matches other loops'
// processes the way pkill -f does. A pgrep by a parent the loop started,
// -P $$, walks the loop's own tree and is let through; -P 1 is every
// detached process on the host.
func checkKillByName(calls [][]string) string {
	kills, lookup := false, ""
	for _, words := range calls {
		switch name := program(words[0]); name {
		case "kill":
			kills = true
		case "pidof":
			lookup = name
		case "pgrep":
			if !ownParent(words[1:]) {
				lookup = name
			}
		case "lsof":
			if hasShortOption(words[1:], 't') {
				lookup = name
			}
		}
	}
	if kills && lookup != "" {
		return "a PID that " + lookup + " looks up can be any loop's process on this host, not only yours; " + killByPID
	}
	return ""
}

// ownParent reports whether pgrep arguments select by a parent other than
// PID 1, which a detached process of any loop is re-parented to.
func ownParent(args []string) bool {
	var parents []string
	for i, arg := range args {
		switch {
		case (arg == "-P" || arg == "--parent") && i+1 < len(args):
			parents = append(parents, args[i+1])
		case strings.HasPrefix(arg, "--parent="):
			parents = append(parents, strings.TrimPrefix(arg, "--parent="))
		case strings.HasPrefix(arg, "-P") && len(arg) > 2:
			parents = append(parents, strings.TrimPrefix(arg, "-P"))
		}
	}
	for _, list := range parents {
		for _, parent := range strings.Split(list, ",") {
			if parent == "1" {
				return false
			}
		}
	}
	return len(parents) > 0
}

// hasShortOption reports whether args hold the one-letter option, alone
// or in a cluster such as -ti.
func hasShortOption(args []string, option byte) bool {
	for _, arg := range args {
		if len(arg) > 1 && arg[0] == '-' && arg[1] != '-' && strings.IndexByte(arg[1:], option) >= 0 {
			return true
		}
	}
	return false
}

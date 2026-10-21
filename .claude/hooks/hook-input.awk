# Reads a PreToolUse hook's input, one JSON object, and prints the decoded
# value of one top-level string field (want="cwd") or of one field of
# tool_input (want="tool_input.command"). Plain awk, run under LC_ALL=C:
# the repo's hook has to work in a workstation with no jq or Python (#630).
# \uXXXX escapes are written out as UTF-8, so a signature's — and · match.
function utf8(code) {
	if (code < 128) return sprintf("%c", code)
	if (code < 2048) return sprintf("%c%c", 192 + int(code / 64), 128 + code % 64)
	return sprintf("%c%c%c", 224 + int(code / 4096), 128 + int(code / 64) % 64, 128 + code % 64)
}
function hex(digits,    i, value) {
	value = 0
	for (i = 1; i <= length(digits); i++)
		value = value * 16 + index("0123456789abcdef", tolower(substr(digits, i, 1))) - 1
	return value
}
{ text = text $0 "\n" }
END {
	n = length(text)
	depth = 0
	key = ""; parent = ""
	split(want, path, ".")
	for (i = 1; i <= n; i++) {
		c = substr(text, i, 1)
		if (c == "{") { depth++; if (depth == 2) parent = key; key = ""; continue }
		if (c == "}") { depth--; key = ""; continue }
		if (c != "\"") continue
		# a string: decode it
		value = ""
		for (i++; i <= n; i++) {
			c = substr(text, i, 1)
			if (c == "\"") break
			if (c != "\\") { value = value c; continue }
			i++; c = substr(text, i, 1)
			if (c == "n") value = value "\n"
			else if (c == "t") value = value "\t"
			else if (c == "r") value = value "\r"
			else if (c == "b") value = value "\b"
			else if (c == "f") value = value "\f"
			else if (c == "u") { value = value utf8(hex(substr(text, i + 1, 4))); i += 4 }
			else value = value c
		}
		# a key is a string followed by a colon
		for (j = i + 1; j <= n && substr(text, j, 1) ~ /[ \t\r\n]/; j++) ;
		if (substr(text, j, 1) == ":") { key = value; continue }
		if (path[2] == "" && depth == 1 && key == path[1]) { printf "%s", value; exit }
		if (path[2] != "" && depth == 2 && parent == path[1] && key == path[2]) { printf "%s", value; exit }
		key = ""
	}
}

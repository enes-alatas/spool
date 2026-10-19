// Package roomtext keeps the room's copy chips the hub's own.
//
// The room renders each backticked span of a hub sentence as a chip that
// copies itself, so a pair of backticks reads as the hub saying "type this"
// (#870, #871). A value the hub interpolates into such a sentence is text it
// did not write: a name or host from a request, a filename, the error a
// platform answered with. Plain takes the backticks out of it, so only the
// hub's words make chips and no one outside the hub can draw one (#959).
package roomtext

import "strings"

// Plain returns value with each backtick replaced by a straight quote,
// ready to interpolate into a sentence the room shows.
func Plain(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}

package surface

import "crypto/rand"

const pairAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// PairCode mints the code a sender Spool does not know yet is registered
// with. The operator matches it in the control room before allowing them,
// the same on every surface.
func PairCode() string {
	var code [6]byte
	if _, err := rand.Read(code[:]); err != nil {
		return "ERRTRY"
	}
	for i := range code {
		code[i] = pairAlphabet[int(code[i])%len(pairAlphabet)]
	}
	return string(code[:])
}

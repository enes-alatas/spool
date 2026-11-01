package surface

// SendOutcome is the payload of bus.KindSendOutcome: a surface carried a
// loop's send or gave up on it, or the operator dismissed one of its
// failures (#1144). The hub's health check reads it to watch for
// undelivered sends and for a surface that is down.
type SendOutcome struct {
	// Surface names the surface that tried the send, as its outbound
	// ledger does ("telegram", "slack"); "" for a dismissal, which no
	// surface tried.
	Surface string
	// LoopID is the loop whose identity on the surface carried the send,
	// or whose failure was dismissed; "" when no loop's did.
	LoopID string
	// Failed is true when the surface gave up on the send.
	Failed bool
}

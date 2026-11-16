package world

// Asking reports whether the ticket's worker is waiting on the user after `dispatch ask`: the
// multiplexer reads it idle, its launch is confirmed, dispatch has not just nudged it, and its
// session's latest ask is still open. The workspace label and the rules' asking fact both read
// this, so they cannot disagree. A log that was not read holds no ask.
func (w World) Asking(ticket string) bool {
	wk := w.Workers[ticket]
	if wk.State != Idle || wk.UnconfirmedSince != 0 || w.Nudged[ticket] {
		return false
	}
	log, ok := w.Sessions[ticket]
	return ok && log.AskOpen()
}

// AskedAt is when the ticket's worker's current session last ran `dispatch ask`, in milliseconds
// since the epoch, whether that ask is open or ended. False when the session never asked or its
// log was not read.
func (w World) AskedAt(ticket string) (int64, bool) {
	ask, ok := w.Sessions[ticket].LatestAsk()
	return ask.At, ok
}

// MessageAt is when the ticket's worker's current session took its latest command or other
// message that is not a task notice, in milliseconds since the epoch. False when it took none or
// its log was not read.
func (w World) MessageAt(ticket string) (int64, bool) {
	return w.Sessions[ticket].LatestMessage()
}

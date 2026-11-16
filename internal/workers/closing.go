package workers

import (
	"time"
	"unicode/utf8"
)

// MessageLimit is the most bytes of a closing message the worker row keeps.
const MessageLimit = 64 * 1024

// Closing is what a worker said when it ended the turn that ended At. It is kept apart
// from Record, so the history readings of a Stop carry no chat.
type Closing struct {
	// At is when the turn ended, in milliseconds since the epoch.
	At      int64  `json:"at"`
	Message string `json:"message"`
	// Cut is true when the message was longer than MessageLimit.
	Cut bool `json:"cut"`
	// Transcript names the harness's record of the session, which it reads to tell whether the
	// session took a message after the turn.
	Transcript string `json:"transcript,omitempty"`
}

// NewClosing keeps the first MessageLimit bytes of message, without splitting a rune.
func NewClosing(at int64, message, transcript string) Closing {
	c := Closing{At: at, Message: message, Transcript: transcript}
	if len(message) > MessageLimit {
		cut := MessageLimit
		for back := 0; back < utf8.UTFMax && cut > 0 && !utf8.RuneStart(message[cut]); back++ {
			cut--
		}
		c.Message, c.Cut = message[:cut], true
	}
	return c
}

// TurnTimeLayout is how a turn's end is written for people and models: RFC 3339 in UTC with
// milliseconds, so two turns in one second stay apart.
const TurnTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// FormatTurnTime writes a turn's end, in milliseconds since the epoch, as TurnTimeLayout.
func FormatTurnTime(at int64) string { return time.UnixMilli(at).UTC().Format(TurnTimeLayout) }

// ParseTurnTime reads an RFC 3339 time, with or without milliseconds and in any zone, as
// milliseconds since the epoch.
func ParseTurnTime(s string) (int64, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}

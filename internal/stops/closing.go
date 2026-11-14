package stops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// MessageLimit is the most bytes of a closing message the file keeps.
const MessageLimit = 64 * 1024

// Closing is what a worker said when it ended the turn that ended At. It lives beside the
// session's log and not in Record, so the history readings of a Stop carry no chat.
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

// WriteClosing replaces the closing message of session in the tree.
func WriteClosing(stateDir, tree, session string, c Closing) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	name := sessionlog.ClosingPath(stateDir, tree, session)
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("while writing the closing message: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".closing-*.tmp")
	if err != nil {
		return fmt.Errorf("while writing the closing message: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing the closing message: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing the closing message: %w", err)
	}
	return os.Rename(tmp.Name(), name)
}

// ReadClosing returns the closing message of session's turn that ended at at. A missing or
// unreadable file, and one that holds another turn's message, are reported as absent.
func ReadClosing(stateDir, tree, session string, at int64) (Closing, bool) {
	b, err := os.ReadFile(sessionlog.ClosingPath(stateDir, tree, session))
	if err != nil {
		return Closing{}, false
	}
	var c Closing
	if json.Unmarshal(b, &c) != nil || c.At != at {
		return Closing{}, false
	}
	return c, true
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

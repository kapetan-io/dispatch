package dispatch

import "encoding/json"

// hookInput reads the first JSON value on stdin, as a hook that never closes stdin would need.
func hookInput(opts Options) ([]byte, bool) {
	var input json.RawMessage
	err := json.NewDecoder(opts.Stdin).Decode(&input)
	return input, err == nil
}

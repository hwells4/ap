package signals

import (
	"encoding/json"
	"testing"
)

// FuzzParseSignal exercises the signal parser with arbitrary JSON input.
func FuzzParseSignal(f *testing.F) {
	// Valid complete signal
	f.Add([]byte(`{"inject":"critical context","spawn":[{"run":"test-scanner","session":"auth-tests","context":"scan auth","n":3}],"escalate":{"type":"human","reason":"need choice","options":["A","B"]}}`))

	// Empty / null
	f.Add([]byte(""))
	f.Add([]byte("null"))
	f.Add([]byte("{}"))

	// Inject only
	f.Add([]byte(`{"inject":"hello"}`))

	// Spawn only
	f.Add([]byte(`{"spawn":[{"run":"a","session":"b"}]}`))
	f.Add([]byte(`{"spawn":[{"run":"a","session":"b","n":1}]}`))
	f.Add([]byte(`{"spawn":[]}`))
	f.Add([]byte(`{"spawn":null}`))

	// Escalate only
	f.Add([]byte(`{"escalate":{"type":"human","reason":"help"}}`))
	f.Add([]byte(`{"escalate":{"type":"human","reason":"help","options":["x","y"]}}`))
	f.Add([]byte(`{"escalate":null}`))

	// Reserved signals that become warnings
	f.Add([]byte(`{"checkpoint":{"name":"cp1"},"budget":{"remaining":10}}`))

	// Malformed / invalid shapes
	f.Add([]byte(`"bad"`))
	f.Add([]byte(`[1,2,3]`))
	f.Add([]byte(`{"spawn":"bad"}`))
	f.Add([]byte(`{"spawn":[{"run":"x"}]}`))
	f.Add([]byte(`{"mystery":true}`))
	f.Add([]byte(`{"spawn":[{"run":"x","session":"y","extra":1}]}`))
	f.Add([]byte(`{"escalate":{"type":"human"}}`))
	f.Add([]byte(`{"inject":123}`))
	f.Add([]byte(`{"spawn":[123]}`))

	// Invalid JSON
	f.Add([]byte(`{not json`))
	f.Add([]byte(`{"key":}`))
	f.Add([]byte("\xff\xfe"))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(json.RawMessage(data))
	})
}

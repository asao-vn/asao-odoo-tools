package clicore

import (
	"context"
	"encoding/json"
)

// Relay runs another leaf as if its command line had been typed, through the executable's one execution
// path (internal/dispatch), and answers the data of its result envelope, or its refusal. Only a
// ClientLocalWorkspace leaf receives one (RelayFrom); the relayed leaf is governed on its own, never by the
// leaf that relays it.
type Relay func(ctx context.Context, argv []string) (json.RawMessage, *Error)

type relayKey struct{}

// WithRelay gives a ClientLocalWorkspace leaf its relay.
func WithRelay(ctx context.Context, relay Relay) context.Context {
	return context.WithValue(ctx, relayKey{}, relay)
}

// RelayFrom is the relay the executable gave this invocation, if any.
func RelayFrom(ctx context.Context) (Relay, bool) {
	relay, ok := ctx.Value(relayKey{}).(Relay)
	return relay, ok && relay != nil
}

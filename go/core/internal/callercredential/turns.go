// Package callercredential serves Substrate's egress gateway the token of the
// person whose turn runs on an actor: the gateway asks per request, the
// controller answers from the turn the A2A gateway dispatched and exchanges
// the caller's token at the platform's token broker for the audience the
// credential names. Nothing is answered outside a turn.
package callercredential

import (
	"sync"
	"time"

	"github.com/kagent-dev/kagent/go/core/pkg/auth"
)

// Turns records the session of the turn running on each actor. Turns live in
// the memory of the controller replica that dispatched them, like the A2A
// gateway's own runs, so the provider must be served by that replica.
type Turns struct {
	mu    sync.Mutex
	turns map[string]*Turn
}

// NewTurns returns an empty registry.
func NewTurns() *Turns {
	return &Turns{turns: map[string]*Turn{}}
}

// Turn is one dispatched turn: the caller's session and the tokens exchanged
// for it. Its tokens are dropped when the turn ends.
type Turn struct {
	instanceID string
	session    auth.Session

	mu     sync.Mutex
	ended  bool
	tokens map[string]exchangedToken
}

type exchangedToken struct {
	value   string
	expires time.Time
}

// Begin records the turn the gateway dispatches to actor ("<atespace>/<actor>")
// and returns the function that ends it. A turn begun on an actor replaces the
// one before it; ending a replaced turn leaves its successor in place.
func (t *Turns) Begin(actor, instanceID string, session auth.Session) func() {
	turn := &Turn{instanceID: instanceID, session: session, tokens: map[string]exchangedToken{}}
	t.mu.Lock()
	if previous, ok := t.turns[actor]; ok {
		previous.end()
	}
	t.turns[actor] = turn
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		if t.turns[actor] == turn {
			delete(t.turns, actor)
		}
		t.mu.Unlock()
		turn.end()
	}
}

// Current returns the turn running on actor.
func (t *Turns) Current(actor string) (*Turn, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	turn, ok := t.turns[actor]
	return turn, ok
}

func (t *Turn) end() {
	t.mu.Lock()
	t.ended = true
	clear(t.tokens)
	t.mu.Unlock()
}

// Package callercredential serves Substrate's egress gateway the token of the
// person whose turn runs on an actor: the gateway asks per request, the
// controller answers from the turn the A2A gateway dispatched and exchanges
// the caller's token at the platform's token broker for the audience the
// credential names. Nothing is answered outside a turn.
package callercredential

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

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
// for it. Its tokens and refusals are dropped when the turn ends.
type Turn struct {
	instanceID string
	session    auth.Session
	// exchanges runs one broker exchange per audience at a time; concurrent
	// requests of the turn share its answer.
	exchanges singleflight.Group

	mu       sync.Mutex
	ended    bool
	tokens   map[string]exchangedToken
	refusals map[string]refusal
}

type exchangedToken struct {
	value   string
	expires time.Time
}

// refusal is a caller refusal answered again without asking the broker until
// it lapses, so a client's retries do not each cost an exchange.
type refusal struct {
	err   error
	until time.Time
}

// Begin records the turn the gateway dispatches to actor ("<atespace>/<actor>")
// and returns the function that ends it. A turn begun on an actor replaces the
// one before it; ending a replaced turn leaves its successor in place.
func (t *Turns) Begin(actor, instanceID string, session auth.Session) func() {
	turn := &Turn{instanceID: instanceID, session: session, tokens: map[string]exchangedToken{}, refusals: map[string]refusal{}}
	t.mu.Lock()
	previous := t.turns[actor]
	t.turns[actor] = turn
	t.mu.Unlock()
	// Outside t.mu: ending a turn waits for its own lock, and no registry
	// lookup waits on one turn.
	if previous != nil {
		previous.end()
	}
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
	clear(t.refusals)
	t.mu.Unlock()
}

// cached answers for audience from what the turn already holds: a token that
// is still fresh at now, a refusal that has not lapsed, or that the turn ended.
func (t *Turn) cached(audience string, now time.Time) (string, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return "", true, errTurnEnded
	}
	if token, ok := t.tokens[audience]; ok && now.Add(tokenExpiryMargin).Before(token.expires) {
		return token.value, true, nil
	}
	if refused, ok := t.refusals[audience]; ok && now.Before(refused.until) {
		return "", true, refused.err
	}
	return "", false, nil
}

// keep records a token, or a refusal, for audience. A turn that ended keeps
// nothing and reports false: what was exchanged for it is answered to no one.
func (t *Turn) keep(audience string, token exchangedToken, refused *refusal) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return false
	}
	if refused != nil {
		t.refusals[audience] = *refused
		return true
	}
	delete(t.refusals, audience)
	t.tokens[audience] = token
	return true
}

// Example insecure_opt_in demonstrates the guard on sending a bearer token over
// a plaintext connection -- see root DESIGN.md, "RULE: Credentials over
// insecure transport require an explicit opt-in".
//
// The failure this rule exists to prevent is mundane and common: a developer
// copies an insecure constructor out of a localhost example into a staging
// config, and a long-lived SpiceDB token -- a complete authorization bypass in
// anyone else's hands -- goes onto the wire in cleartext with nothing
// signalling that it happened. So NewPlaintext is loopback-only, and reaching a
// remote host over plaintext takes a second, separately-named option the caller
// cannot supply by accident: WithInsecureAllowRemoteHost.
//
// The sharpest case is the last one below. The rule requires the guard's answer
// to be *the transport's* answer -- the same parser the client dials with --
// rather than a hand-rolled string split. Given "127.0.0.1:443@evil.com", a
// last-colon split reads the host as "127.0.0.1" and waves it through, while a
// URI parser reads "127.0.0.1:443" as *userinfo* and connects to evil.com. A
// client that split the string would leak the token to evil.com while believing
// it was talking to loopback.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
)

func main() {
	endpoint := cmp.Or(os.Getenv("SPICEDB_ENDPOINT"), "localhost:50051")
	token := cmp.Or(os.Getenv("SPICEDB_TOKEN"), "somerandomkeyhere")

	// ── 1. Loopback plaintext needs no opt-in ────────────────────────────
	//
	// This is the case the rule deliberately leaves ergonomic: a token on a
	// loopback socket never leaves the machine, so requiring ceremony here
	// would only train developers to reach for the opt-in reflexively.
	c, err := client.NewPlaintext(endpoint, token)
	if err != nil {
		log.Fatalf("NewPlaintext against loopback %q should be allowed without any opt-in: %v", endpoint, err)
	}
	defer func() { _ = c.Close() }()

	// Prove the client is usable rather than merely constructed: NewPlaintext
	// is lazy, so a constructor that returned a client which could not talk to
	// anything would still satisfy the check above.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := c.WriteSchema(ctx, `definition user {}

definition document {
	relation viewer: user
	permission view = viewer
}`); err != nil {
		log.Fatalf("loopback plaintext client could not reach SpiceDB: %v", err)
	}
	fmt.Println("loopback plaintext: allowed with no opt-in, and works")

	// ── 2. Remote plaintext is refused ───────────────────────────────────
	//
	// No connection is attempted: the refusal happens during construction, so
	// the token never reaches a socket. Note this is NOT about whether the host
	// exists -- example.com is refused because it is not loopback, full stop.
	if _, err := client.NewPlaintext("example.com:50051", token); err == nil {
		log.Fatal("SECURITY: NewPlaintext sent a bearer token to a non-loopback host in cleartext with no opt-in")
	} else if !errors.Is(err, client.ErrInvalidArgument) {
		// The refusal is this client's own typed argument error, the same one a
		// filter the wire cannot express uses -- not the proto tier's type, and
		// not a bare message a caller would have to string-match. Root
		// DESIGN.md, "RULE: Credentials over insecure transport require an
		// explicit opt-in", clause 4.
		log.Fatalf("the refusal must match ErrInvalidArgument, got %v", err)
	} else {
		fmt.Printf("remote plaintext, no opt-in: refused (%v)\n", err)
	}

	// ── 3. ...unless the caller says so, by name ─────────────────────────
	//
	// Two options, not one: WithInsecure selects the plaintext transport, and
	// WithInsecureAllowRemoteHost accepts the credential exposure that follows.
	// Keeping them separate is the point -- a single boolean doing both jobs is
	// exactly what clause 1 forbids, because "I want plaintext for local dev"
	// and "I accept shipping this token in cleartext to a remote host" are
	// different decisions.
	remote, err := client.NewWithOpts("example.com:50051", token,
		client.WithInsecure(), client.WithInsecureAllowRemoteHost())
	if err != nil {
		log.Fatalf("the named opt-in should permit remote plaintext: %v", err)
	}
	_ = remote.Close()
	fmt.Println("remote plaintext, explicit opt-in: allowed")

	// ── 4. The spoof that a string split would wave through ──────────────
	//
	// Under URI parsing "127.0.0.1:443" here is userinfo and the real host is
	// evil.com, so this must be refused. Failing closed is what matters: the
	// rule does not promise every client agrees on loopback spellings, but it
	// does require that anything the guard calls loopback is somewhere the
	// transport actually dials on loopback.
	if _, err := client.NewPlaintext("127.0.0.1:443@evil.com", token); err == nil {
		log.Fatal("SECURITY: an endpoint whose real host is evil.com was accepted as loopback -- " +
			"the guard is splitting the string instead of asking the transport's parser")
	} else if !errors.Is(err, client.ErrInvalidArgument) {
		log.Fatalf("the refusal must match ErrInvalidArgument, got %v", err)
	} else {
		fmt.Printf("userinfo spoof 127.0.0.1:443@evil.com: refused (%v)\n", err)
	}

	fmt.Println("insecure_opt_in: all four cases behaved as the rule requires")
}

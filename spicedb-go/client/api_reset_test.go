package client

import (
	"context"
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func TestResetRejectsInvalidBeforeRPC(t *testing.T) {
	c := &Client{}
	if _, err := c.Check(context.Background(), PermissionCheck{}); err == nil {
		t.Fatal("zero check accepted")
	}
	if _, err := c.CheckBulk(context.Background(), nil, WithCheckTimeout(0)); err == nil {
		t.Fatal("empty bulk bypassed option validation")
	}
	obj, _ := rel.NewObject("document", "a")
	subobj, _ := rel.NewObject("user", "alice")
	sub, _ := rel.NewSubject(subobj, "")
	a := PermissionCheck{Resource: obj, Subject: sub, Permission: "view", Consistency: consistency.Full()}
	b := a
	b.Consistency = consistency.MinLatency()
	if _, err := c.CheckBulk(context.Background(), []PermissionCheck{a, b}); err == nil {
		t.Fatal("mixed consistency accepted")
	}
}

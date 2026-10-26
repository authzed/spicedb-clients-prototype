package client

import (
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func testObject(typ, id string) rel.ObjectRef {
	o, e := rel.NewObject(typ, id)
	if e != nil {
		panic(e)
	}
	return o
}
func testSubject(typ, id, relation string) rel.SubjectRef {
	s, e := rel.NewSubject(testObject(typ, id), relation)
	if e != nil {
		panic(e)
	}
	return s
}
func testFilter(typ string) rel.Filter {
	f, e := rel.NewFilter(typ)
	if e != nil {
		panic(e)
	}
	return f
}
func testTriple(rt, ri, rr, st, si, sr string) rel.Relationship {
	r, e := rel.NewRelationship(testObject(rt, ri), rr, testSubject(st, si, sr))
	if e != nil {
		panic(e)
	}
	return r
}
func testCheck(cs consistency.Strategy, permission string, r rel.Relationship) PermissionCheck {
	return PermissionCheck{Resource: r.Resource(), Subject: r.Subject(), Permission: permission, Consistency: cs}
}
func testRequest(id string) PermissionCheck {
	return PermissionCheck{Resource: testObject("document", id), Subject: testSubject("user", "alice", ""), Permission: "view", Consistency: consistency.Full()}
}

// Package exampleutil keeps error handling for constant example fixtures concise.
package exampleutil

import "github.com/authzed/spicedb-clients/spicedb-go/rel"

func Must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func Object(objectType, id string) rel.ObjectRef { return Must(rel.NewObject(objectType, id)) }
func Subject(object rel.ObjectRef, relation string) rel.SubjectRef {
	return Must(rel.NewSubject(object, relation))
}

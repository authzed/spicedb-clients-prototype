package main

import (
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/generator"
)

// Every generator documents its options, so --help never hides a flag.
func TestEveryGeneratorDocumentsItsOptions(t *testing.T) {
	if len(generator.Registry) == 0 {
		t.Fatal("no generators registered")
	}
	for name, g := range generator.Registry {
		d, ok := g.(generator.OptionDescriber)
		if !ok || len(d.Options()) == 0 {
			t.Errorf("%s generator does not describe its options", name)
			continue
		}
		for _, o := range d.Options() {
			if o.Name == "" || o.Value == "" || o.Description == "" {
				t.Errorf("%s: incomplete option description %+v", name, o)
			}
		}
	}
}

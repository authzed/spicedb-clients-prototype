package consistency_test

import (
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"reflect"
	"testing"
)

func TestTokenValueIsOpaqueAndLossless(t *testing.T) {
	for _, value := range []string{"", "opaque:+/===", "\x00 whitespace\n雪"} {
		token := consistency.NewZedToken(value)
		if token.String() != value {
			t.Fatalf("token changed: %q", token.String())
		}
		typ := reflect.TypeOf(token)
		if typ.Kind() != reflect.Struct {
			t.Fatal("token must have private representation")
		}
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				t.Fatal("exported token storage")
			}
		}
	}
}

package spicedbgoproto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestNewClient(t *testing.T) {
	client, err := NewClient("passthrough:///localhost:50051", "test-token", WithInsecure())
	require.NoError(t, err)

	require.NotNil(t, client.PermissionsServiceClient)
	require.NotNil(t, client.SchemaServiceClient)
	require.NotNil(t, client.WatchServiceClient)
	require.NotNil(t, client.ExperimentalServiceClient)
}

// TestClient_CloseIsIdempotent proves Close can be called more than once
// without error -- root DESIGN.md, "RULE: Abandoning a stream must release
// it" requires a deterministic release mechanism, and a Close that panics or
// errors on a second call is a footgun for any caller wrapping it in a
// defer alongside an explicit early Close.
func TestClient_CloseIsIdempotent(t *testing.T) {
	client, err := NewClient("passthrough:///localhost:50051", "test-token", WithInsecure())
	require.NoError(t, err)

	require.NoError(t, client.Close())
	require.NoError(t, client.Close())
	require.NoError(t, client.Close())
}

// TestClient_CloseOnHandBuiltClientIsANoOp proves Close does not panic on a
// Client that never dialed. conn is unexported, so a Client built literally
// from service stubs -- the documented way to point the idiomatic client at
// a test double -- has a nil conn NewClient could never produce. A `defer
// client.Close()` copied from production code would otherwise crash the
// test on a line that has nothing to do with what it is testing.
func TestClient_CloseOnHandBuiltClientIsANoOp(t *testing.T) {
	handBuilt := &Client{}
	require.NotPanics(t, func() {
		require.NoError(t, handBuilt.Close())
	})

	var nilClient *Client
	require.NotPanics(t, func() {
		require.NoError(t, nilClient.Close())
	})
}

func TestWithInsecure(t *testing.T) {
	cfg := &clientConfig{}
	WithInsecure()(cfg)
	require.True(t, cfg.insecure)
}

func TestWithDialOptions(t *testing.T) {
	cfg := &clientConfig{}
	opt := grpc.WithAuthority("custom-authority")
	WithDialOptions(opt)(cfg)
	require.Len(t, cfg.dialOptions, 1)
}

// jsonName / jsonRetryPolicy / jsonMethodConfig / jsonServiceConfig mirror
// (a minimal subset of) the shape grpc-go's own service_config.go parses,
// so this test asserts on retryServiceConfig's actual structure rather than
// string-matching the raw JSON.
type jsonName struct {
	Service string `json:"service"`
	Method  string `json:"method"`
}

type jsonRetryPolicy struct {
	MaxAttempts          int      `json:"maxAttempts"`
	RetryableStatusCodes []string `json:"retryableStatusCodes"`
}

type jsonMethodConfig struct {
	Name        []jsonName       `json:"name"`
	RetryPolicy *jsonRetryPolicy `json:"retryPolicy"`
}

type jsonServiceConfig struct {
	MethodConfig []jsonMethodConfig `json:"methodConfig"`
}

// nonIdempotentMethods must exactly match the seven method-level "name"
// entries in retryServiceConfig's second methodConfig entry -- the RPCs
// DESIGN.md's "Automatic retry is for idempotent operations only" forbids
// retrying: WriteRelationships/DeleteRelationships/ImportBulkRelationships
// (proto's OPERATION_CREATE/preconditions hazard), WriteSchema, the two
// relationship-counter register/unregister calls, and BulkImportRelationships
// -- ImportBulkRelationships' deprecated predecessor, still reachable via the
// exported ExperimentalServiceClient and just as non-idempotent a bulk write.
var nonIdempotentMethods = []jsonName{
	{Service: "authzed.api.v1.PermissionsService", Method: "WriteRelationships"},
	{Service: "authzed.api.v1.PermissionsService", Method: "DeleteRelationships"},
	{Service: "authzed.api.v1.PermissionsService", Method: "ImportBulkRelationships"},
	{Service: "authzed.api.v1.SchemaService", Method: "WriteSchema"},
	{Service: "authzed.api.v1.ExperimentalService", Method: "ExperimentalRegisterRelationshipCounter"},
	{Service: "authzed.api.v1.ExperimentalService", Method: "ExperimentalUnregisterRelationshipCounter"},
	{Service: "authzed.api.v1.ExperimentalService", Method: "BulkImportRelationships"},
}

// TestRetryServiceConfig_MutationsHaveNoRetryPolicy proves the per-method
// split: the seven non-idempotent RPCs above have a "name" entry with no
// "retryPolicy" at all, and (per grpc-go's own getMethodConfig -- an exact
// "/service/method" match always wins over a "/service/" wildcard match)
// this overrides the broader per-service entry that DOES retry. Regression
// guard for finding 12 ("Unsafe retry"): every method here used to inherit
// the service-wide retryPolicy, so a WriteRelationships whose response was
// lost after it committed would be silently retried and surface
// ALREADY_EXISTS to a caller who in fact succeeded.
func TestRetryServiceConfig_MutationsHaveNoRetryPolicy(t *testing.T) {
	var cfg jsonServiceConfig
	require.NoError(t, json.Unmarshal([]byte(retryServiceConfig), &cfg))
	require.Len(t, cfg.MethodConfig, 2, "expected exactly two methodConfig entries")

	mutationEntry := cfg.MethodConfig[1]
	require.Nil(t, mutationEntry.RetryPolicy, "the mutation methodConfig entry must carry no retryPolicy")
	require.ElementsMatch(t, nonIdempotentMethods, mutationEntry.Name)

	for _, n := range mutationEntry.Name {
		require.NotEmpty(t, n.Method, "every name in the mutation entry must be method-specific, not a service wildcard")
	}
}

// TestRetryServiceConfig_ReadsRetryButNotResourceExhausted proves the
// service-wide entry (methodConfig[0], matched by every RPC not
// method-overridden above) retries UNAVAILABLE and ABORTED, but no longer
// RESOURCE_EXHAUSTED -- which SpiceDB uses for memory load-shed or a
// deterministic MaxDepthExceeded, neither of which retrying helps.
func TestRetryServiceConfig_ReadsRetryButNotResourceExhausted(t *testing.T) {
	var cfg jsonServiceConfig
	require.NoError(t, json.Unmarshal([]byte(retryServiceConfig), &cfg))
	require.Len(t, cfg.MethodConfig, 2)

	readEntry := cfg.MethodConfig[0]
	require.NotNil(t, readEntry.RetryPolicy)
	require.Contains(t, readEntry.RetryPolicy.RetryableStatusCodes, "UNAVAILABLE")
	require.Contains(t, readEntry.RetryPolicy.RetryableStatusCodes, "ABORTED")
	require.NotContains(t, readEntry.RetryPolicy.RetryableStatusCodes, "RESOURCE_EXHAUSTED")

	for _, n := range readEntry.Name {
		require.Empty(t, n.Method, "the read entry must be a service-level wildcard, not method-specific")
	}
}

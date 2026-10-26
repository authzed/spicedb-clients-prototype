package client

import (
	"time"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

type TracePermissionType uint8

const (
	TracePermissionUnspecified TracePermissionType = iota
	TraceRelation
	TracePermission
)

type CaveatEvaluationResult uint8

const (
	CaveatEvaluationUnspecified CaveatEvaluationResult = iota
	CaveatUnevaluated
	CaveatFalse
	CaveatTrue
	CaveatMissingContext
)

type TraceResolutionKind uint8

const (
	TraceResolutionUnspecified TraceResolutionKind = iota
	TraceCached
	TraceSubproblems
)

type DebugInformation struct {
	SchemaUsed string
	Check      *CheckDebugTrace
}
type CaveatEvaluation struct {
	Expression     string
	Result         CaveatEvaluationResult
	Context        map[string]any
	MissingContext []string
	CaveatName     string
}

// CheckDebugTrace preserves diagnostic presence, including a present cached=false.
type CheckDebugTrace struct {
	Resource         rel.ObjectRef
	Permission       string
	PermissionType   TracePermissionType
	Subject          rel.SubjectRef
	Result           Permissionship
	CaveatEvaluation *CaveatEvaluation
	Duration         *time.Duration
	Resolution       TraceResolutionKind
	WasCachedResult  *bool
	Subproblems      []CheckDebugTrace
	Expiration       *time.Time
	TraceOperationID string
	Source           string
}

func debugInformation(p *v1.DebugInformation) *DebugInformation {
	if p == nil {
		return nil
	}
	return &DebugInformation{SchemaUsed: p.GetSchemaUsed(), Check: debugTrace(p.GetCheck())}
}
func debugTrace(p *v1.CheckDebugTrace) *CheckDebugTrace {
	if p == nil {
		return nil
	}
	r := &CheckDebugTrace{Resource: nativeObject(p.GetResource()), Permission: p.GetPermission(), Subject: nativeSubject(p.GetSubject()), TraceOperationID: p.GetTraceOperationId(), Source: p.GetSource()}
	switch p.GetPermissionType() {
	case v1.CheckDebugTrace_PERMISSION_TYPE_RELATION:
		r.PermissionType = TraceRelation
	case v1.CheckDebugTrace_PERMISSION_TYPE_PERMISSION:
		r.PermissionType = TracePermission
	}
	switch p.GetResult() {
	case v1.CheckDebugTrace_PERMISSIONSHIP_NO_PERMISSION:
		r.Result = PermissionshipNoPermission
	case v1.CheckDebugTrace_PERMISSIONSHIP_HAS_PERMISSION:
		r.Result = PermissionshipHasPermission
	case v1.CheckDebugTrace_PERMISSIONSHIP_CONDITIONAL_PERMISSION:
		r.Result = PermissionshipConditionalPermission
	}
	if d := p.GetDuration(); d != nil {
		duration := d.AsDuration()
		r.Duration = &duration
	}
	if exp := p.GetOptionalExpiresAt(); exp != nil {
		t := exp.AsTime()
		r.Expiration = &t
	}
	switch resolution := p.GetResolution().(type) {
	case *v1.CheckDebugTrace_WasCachedResult:
		r.Resolution = TraceCached
		b := resolution.WasCachedResult
		r.WasCachedResult = &b
	case *v1.CheckDebugTrace_SubProblems_:
		r.Resolution = TraceSubproblems
		r.Subproblems = make([]CheckDebugTrace, 0, len(resolution.SubProblems.GetTraces()))
		for _, child := range resolution.SubProblems.GetTraces() {
			if d := debugTrace(child); d != nil {
				r.Subproblems = append(r.Subproblems, *d)
			}
		}
	}
	if cav := p.GetCaveatEvaluationInfo(); cav != nil {
		c := &CaveatEvaluation{Expression: cav.GetExpression(), Context: cav.GetContext().AsMap(), MissingContext: append([]string(nil), cav.GetPartialCaveatInfo().GetMissingRequiredContext()...), CaveatName: cav.GetCaveatName()}
		switch cav.GetResult() {
		case v1.CaveatEvalInfo_RESULT_UNEVALUATED:
			c.Result = CaveatUnevaluated
		case v1.CaveatEvalInfo_RESULT_FALSE:
			c.Result = CaveatFalse
		case v1.CaveatEvalInfo_RESULT_TRUE:
			c.Result = CaveatTrue
		case v1.CaveatEvalInfo_RESULT_MISSING_SOME_CONTEXT:
			c.Result = CaveatMissingContext
		}
		r.CaveatEvaluation = c
	}
	return r
}

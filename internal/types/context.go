package types

import "context"

type stageContextKey struct{}

// StageContext carries pipeline stage information through context.Context.
type StageContext struct {
	Stage Stage
	Agent string // resolved agent name for this stage
}

// WithStage attaches stage information to a context.
func WithStage(ctx context.Context, stage Stage, agent string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, stageContextKey{}, StageContext{Stage: stage, Agent: agent})
}

// StageFromContext returns the stage context attached to the context, if any.
func StageFromContext(ctx context.Context) (StageContext, bool) {
	if ctx == nil {
		return StageContext{}, false
	}
	sc, ok := ctx.Value(stageContextKey{}).(StageContext)
	return sc, ok
}

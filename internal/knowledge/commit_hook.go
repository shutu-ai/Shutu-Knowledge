package knowledge

import (
	"context"
	"database/sql"
)

// CommitHook runs inside the short transaction that publishes a document
// business effect. It keeps Knowledge independent from the Operations package
// while allowing an application-level marker to share the same commit.
type CommitHook func(*sql.Tx) error

// ItemCommitHookFactory creates a commit hook for one logical batch item.
// It is kept separate from CommitHook so a batch can assign a stable marker
// key without making Knowledge depend on Operations.
type ItemCommitHookFactory func(itemKey string) CommitHook

type commitHookContextKey struct{}
type itemCommitHookContextKey struct{}

// WithCommitHook attaches a bounded SQL callback to a business mutation.
// The callback must use the supplied transaction and must not start another
// transaction.
func WithCommitHook(ctx context.Context, hook CommitHook) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if hook == nil {
		return ctx
	}
	return context.WithValue(ctx, commitHookContextKey{}, hook)
}

// WithItemCommitHook attaches a per-item commit hook factory to a batch
// mutation. Each factory result runs inside the item's business transaction.
func WithItemCommitHook(ctx context.Context, factory ItemCommitHookFactory) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if factory == nil {
		return ctx
	}
	return context.WithValue(ctx, itemCommitHookContextKey{}, factory)
}

func commitHookFromContext(ctx context.Context) CommitHook {
	if ctx == nil {
		return nil
	}
	hook, _ := ctx.Value(commitHookContextKey{}).(CommitHook)
	return hook
}

func itemCommitHookFactoryFromContext(ctx context.Context) ItemCommitHookFactory {
	if ctx == nil {
		return nil
	}
	factory, _ := ctx.Value(itemCommitHookContextKey{}).(ItemCommitHookFactory)
	return factory
}

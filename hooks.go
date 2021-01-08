package lambdahooks

import (
	"context"
)

// PreHook is a hook that's invoked before the handler is executed
type PreHook interface {

	// BeforeExecution executes before the handler is executed
	// You may modify the context at this point and the modified
	// context will be passed on to the handler
	BeforeExecution(
		ctx context.Context,
		payload []byte,
	) context.Context
}

// AddPreHook adds a pre hook to be notified before execution
func AddPreHook(hook PreHook) {
	preHooks = append(preHooks, hook)
}

// RemovePreHook removes a pre hook
func RemovePreHook(hook PreHook) {
	var hookIndex int = -1
	for i, h := range preHooks {
		if h == hook {
			hookIndex = i
			break
		}
	}

	if hookIndex == -1 {
		return
	}

	preHooks = append(preHooks[:hookIndex], preHooks[hookIndex+1:]...)
}

// PostHook is a hook that's invoked after the handler has executed
type PostHook interface {

	// AfterExecution executes once handler has executed
	AfterExecution(
		ctx context.Context,
		payload []byte,
		returnValue interface{},
		err interface{},
	)
}

// AddPostHook adds a post hook to be notified after execution
func AddPostHook(hook PostHook) {
	postHooks = append(postHooks, hook)
}

// RemovePostHook removes a post hook
func RemovePostHook(hook PostHook) {
	var hookIndex int = -1
	for i, h := range postHooks {
		if h == hook {
			hookIndex = i
			break
		}
	}

	if hookIndex == -1 {
		return
	}

	postHooks = append(postHooks[:hookIndex], postHooks[hookIndex+1:]...)
}

// runPreHooks executes all pre hooks in the order they are registered
func runPreHooks(ctx context.Context, payload []byte) context.Context {
	for _, hook := range preHooks {
		ctx = hook.BeforeExecution(ctx, payload)
	}

	return ctx
}

// runPostHooks executes all post hooks in the order they are registered
func runPostHooks(
	ctx context.Context,
	payload []byte,
	returnValue interface{},
	err interface{},
) {
	for _, hook := range postHooks {
		hook.AfterExecution(ctx, payload, returnValue, err)
	}
}

package lambdahooks

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
)

const (
	// MaxDeadlineCushion is the timeout limit for deadline cushion.
	// Deadline cushion should not exceed lambda timeout
	// https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html
	// But setting the cushion at this limit effectively signals timeout
	// to post hooks by default
	MaxDeadlineCushion = 15 * time.Minute

	// DefaultTimeout is the default timeout limit
	// https://docs.aws.amazon.com/lambda/latest/dg/configuration-console.html
	DefaultTimeout = 3 * time.Second
)

var (
	deadlineCushion = (time.Duration)(0.2 * float64(DefaultTimeout))
	preHooks        = []PreHook{}
	postHooks       = []PostHook{}
	starterFunc     = lambda.StartHandler
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

// lambdaHandler is the generic function type
type lambdaHandler func(context.Context, []byte) (interface{}, error)

// Invoke calls the handler, and serializes the response.
// If the underlying handler returned an error, or an error occurs during serialization, error is returned.
func (handler lambdaHandler) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	response, err := handler(ctx, payload)
	if err != nil {
		return nil, err
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	return responseBytes, nil
}

// Option is an option to override defaults
type Option func() error

// Init sets up the hooks before use
// Overrides defaults as needed
func Init(options ...Option) error {
	for _, opt := range options {
		if err := opt(); err != nil {
			return err
		}
	}

	return nil
}

// WithDeadlineCushion overrides the default deadline cushion with given cushion
func WithDeadlineCushion(d time.Duration) Option {
	return func() error {
		if deadlineCushion > MaxDeadlineCushion {
			return fmt.Errorf("deadlineCushion cannot exceed %v", MaxDeadlineCushion)
		}

		deadlineCushion = d
		return nil
	}
}

// WithStarterFunc overrides the default starter function with given function
func WithStarterFunc(fn func(lambda.Handler)) Option {
	return func() error {
		if fn == nil {
			return fmt.Errorf("fn cannot be nil")
		}

		starterFunc = fn
		return nil
	}
}

// WithPreHooks adds a list of pre hooks in the order they are provided
func WithPreHooks(hooks ...PreHook) Option {
	return func() error {
		for _, hook := range hooks {
			AddPreHook(hook)
		}
		return nil
	}
}

// WithPostHooks adds a list of post hooks in the order they are provided
func WithPostHooks(hooks ...PostHook) Option {
	return func() error {
		for _, hook := range hooks {
			AddPostHook(hook)
		}
		return nil
	}
}

// Start wraps the handler and starts the AWS lambda handler
func Start(handler interface{}) {
	starterFunc(Wrap(handler))
}

// Wrap wraps the handler so the agent can intercept and record events
// Processes the handler according to the lambda rules below:
// Rules:
//
// 	* handler must be a function
// 	* handler may take between 0 and two arguments.
// 	* if there are two arguments, the first argument must satisfy the "context.Context" interface.
// 	* handler may return between 0 and two arguments.
// 	* if there are two return values, the second argument must be an error.
// 	* if there is one return value it must be an error.
//
// Valid function signatures:
//
// 	func ()
// 	func () error
// 	func (TIn) error
// 	func () (TOut, error)
// 	func (TIn) (TOut, error)
// 	func (context.Context) error
// 	func (context.Context, TIn) error
// 	func (context.Context) (TOut, error)
// 	func (context.Context, TIn) (TOut, error)
//
// Where "TIn" and "TOut" are types compatible with the "encoding/json" standard library.
// See https://golang.org/pkg/encoding/json/#Unmarshal for how deserialization behaves
func Wrap(handlerFunc interface{}) lambda.Handler {
	if handlerFunc == nil {
		return errorHandler(fmt.Errorf("handler is nil"))
	}

	handler := reflect.ValueOf(handlerFunc)
	handlerType := reflect.TypeOf(handlerFunc)
	if handlerType.Kind() != reflect.Func {
		return errorHandler(fmt.Errorf("handler kind %s is not %s", handlerType.Kind(), reflect.Func))
	}

	takesContext, err := validateArguments(handlerType)
	if err != nil {
		return errorHandler(err)
	}

	if err := validateReturns(handlerType); err != nil {
		return errorHandler(err)
	}

	return lambdaHandler(
		func(ctx context.Context, payload []byte) (interface{}, error) {
			defer func() {
				if err := recover(); err != nil {
					// Run post hooks in case of panic
					runPostHooks(ctx, payload, nil, err)
					panic(err)
				}
			}()

			go handleTimeout(ctx, payload)

			var args []reflect.Value
			var eventValue reflect.Value

			if (handlerType.NumIn() == 1 && !takesContext) || handlerType.NumIn() == 2 {
				// Deserialize the last input argument and pass that on to the handler
				eventType := handlerType.In(handlerType.NumIn() - 1)
				event := reflect.New(eventType)

				if err := json.Unmarshal(payload, event.Interface()); err != nil {
					return nil, err
				}

				eventValue = event.Elem()
			}

			ctx = runPreHooks(ctx, payload)
			if takesContext {
				args = append(args, reflect.ValueOf(ctx))
			}

			if eventValue.IsValid() {
				args = append(args, eventValue)
			}

			response := handler.Call(args)

			var err error
			if len(response) > 0 {
				// Convert last return argument to error
				if errVal, ok := response[len(response)-1].Interface().(error); ok {
					err = errVal
				}
			}

			var val interface{}
			if len(response) > 1 {
				val = response[0].Interface()
			}

			if err != nil {
				val = nil
			}

			runPostHooks(ctx, payload, &val, err)

			return val, err
		},
	)
}

// errorHandler returns a stand-in lambda function that
// handles error gracefully
func errorHandler(e error) lambdaHandler {
	return func(ctx context.Context, event []byte) (interface{}, error) {
		return nil, e
	}
}

// validateArguments validates the handler arguments comply
// to lambda handler signature:
// 	* handler may take between 0 and two arguments.
// 	* if there are two arguments, the first argument must satisfy the "context.Context" interface.
func validateArguments(handler reflect.Type) (bool, error) {
	handlerTakesContext := false
	if handler.NumIn() > 2 {
		return false, fmt.Errorf("handlers may not take more than two arguments, but handler takes %d", handler.NumIn())
	}

	if handler.NumIn() > 0 {
		contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
		argumentType := handler.In(0)
		handlerTakesContext = argumentType.Implements(contextType)
		if handler.NumIn() > 1 && !handlerTakesContext {
			return false, fmt.Errorf(
				"handler takes two arguments, but the first is not Context. got %s",
				argumentType.Kind(),
			)
		}
	}

	return handlerTakesContext, nil
}

// validateReturns ensures the return arguments are legal
// Return arguments rules:
// 	* handler may return between 0 and two arguments.
// 	* if there are two return values, the second argument must be an error.
// 	* if there is one return value it must be an error.
func validateReturns(handler reflect.Type) error {
	errorType := reflect.TypeOf((*error)(nil)).Elem()

	switch n := handler.NumOut(); {
	case n > 2:
		return fmt.Errorf("handler may not return more than two values")
	case n > 1:
		if !handler.Out(1).Implements(errorType) {
			return fmt.Errorf("handler returns two values, but the second does not implement error")
		}
	case n == 1:
		if !handler.Out(0).Implements(errorType) {
			return fmt.Errorf("handler returns a single value, but it does not implement error")
		}
	}

	return nil
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

// handleTimeout attempts to run post hooks if there's time left
// If the handler comes close to the deadline, try to run the post
// hooks to capture the timeout result. The choice of deadlineCushion
// is important. Too much cushion, the post hooks may prematurely fire before
// the handler completes in time. This causes the post hooks to get a
// signal conflicting with the actual result. The opposite may not leave
// enough time for all post hooks to run to completion. In general, the
// smaller the cushion the more accurate the signal
func handleTimeout(ctx context.Context, payload []byte) {
	deadline, _ := ctx.Deadline()
	if deadline.IsZero() {
		return
	}

	threshold := deadline.Add(-deadlineCushion)
	if time.Now().After(threshold) {
		return
	}

	timer := time.NewTimer(time.Until(threshold))
	timeoutc := timer.C
	select {
	case <-timeoutc:
		runPostHooks(ctx, payload, nil, timeoutError{})
		return
	case <-ctx.Done():
		timer.Stop()
		return
	}
}

// timeoutError indicates the handler timed out before it could complete
type timeoutError struct{}

// Error returns a string describing the timeout error
func (t timeoutError) Error() string {
	return fmt.Sprintf("Lambda has timed out")
}

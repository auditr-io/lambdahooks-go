package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/aws/aws-lambda-go/events"
	awslambda "github.com/aws/aws-lambda-go/lambda"
)

var (
	preHooks    []PreHook                       = []PreHook{}
	postHooks   []PostHook                      = []PostHook{}
	starterFunc func(handler awslambda.Handler) = awslambda.StartHandler
)

// PreHook is a hook that's invoked before the handler is executed
type PreHook interface {

	// BeforeExecution executes before the handler is executed
	BeforeExecution(
		ctx context.Context,
		payload []byte,
	) context.Context
}

// AddPreHook adds a pre hook to be notified after execution
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
		err error,
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

// lambdaFunction is old
type lambdaFunction func(context.Context, events.APIGatewayProxyRequest) (interface{}, error)

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
func Wrap(handlerFunc interface{}) awslambda.Handler {
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
			// TODO: run post hook on error
			// TODO: run post hook on timeout

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
func runPreHooks(
	ctx context.Context,
	payload []byte,
) context.Context {
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
	err error,
) {
	for _, hook := range postHooks {
		hook.AfterExecution(ctx, payload, returnValue, err)
	}
}

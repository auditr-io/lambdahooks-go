package lambda

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	awslambda "github.com/aws/aws-lambda-go/lambda"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type person struct {
	Name string
	Age  int
}

type recordHook struct {
	mock.Mock
}

type contextKey string

const ctxKey contextKey = "key"

func (h *recordHook) BeforeExecution(
	ctx context.Context,
	payload []byte,
) context.Context {
	h.Called(ctx, payload)
	ctx = context.WithValue(ctx, ctxKey, "value")

	return ctx
}

func (h *recordHook) AfterExecution(
	ctx context.Context,
	payload []byte,
	returnValue interface{},
	err error,
) {
	returnInterface := returnValue.(*interface{})
	p := (*returnInterface).(*person)
	h.Called(ctx, payload, p, err)
}

type mockStarterFunc struct {
	mock.Mock
}

func (m *mockStarterFunc) StartHandler(handler awslambda.Handler) {
	m.Called(handler)
}

func TestStart_StartsHandler(t *testing.T) {
	handler := func(ctx context.Context, e *person) (*person, error) {
		return e, nil
	}

	const (
		lambdaPort       = "_LAMBDA_SERVER_PORT"
		lambdaRuntimeAPI = "AWS_LAMBDA_RUNTIME_API"
	)

	os.Setenv(lambdaPort, "9000")
	os.Setenv(lambdaRuntimeAPI, "localhost")

	m := &mockStarterFunc{}
	m.
		On("StartHandler", mock.AnythingOfType("lambda.lambdaHandler")).
		Once()

	var origStarterFunc func(handler awslambda.Handler)
	starterFunc, origStarterFunc = m.StartHandler, starterFunc
	t.Cleanup(func() {
		os.Unsetenv(lambdaPort)
		os.Unsetenv(lambdaRuntimeAPI)
		starterFunc = origStarterFunc
	})

	Start(handler)
	assert.True(t, m.AssertExpectations(t))
}

func TestWrap_ReturnsInvocableHandler(t *testing.T) {
	handler := func(ctx context.Context, e *person) (*person, error) {
		return e, nil
	}

	wrappedHandler := Wrap(handler)
	assert.IsType(t, *new(lambdaHandler), wrappedHandler)
	assert.Implements(t, new(awslambda.Handler), wrappedHandler)

	pIn := &person{
		Name: "x",
		Age:  10,
	}
	payload, err := json.Marshal(pIn)
	assert.NoError(t, err)

	awslambdaHandler := wrappedHandler.(awslambda.Handler)
	resBytes, err := awslambdaHandler.Invoke(
		context.Background(),
		payload,
	)
	assert.NoError(t, err)

	var pOut *person
	err = json.Unmarshal(resBytes, &pOut)
	assert.Equal(t, pIn, pOut)
}

func TestWrap_RunsPreHook(t *testing.T) {
	handler := func(ctx context.Context, p *person) (*person, error) {
		return p, nil
	}

	record := &recordHook{}
	AddPreHook(record)
	t.Cleanup(func() {
		RemovePreHook(record)
	})

	wrappedHandler := Wrap(handler)
	assert.IsType(t, *new(lambdaHandler), wrappedHandler)
	assert.Implements(t, new(awslambda.Handler), wrappedHandler)

	pIn := &person{
		Name: "x",
		Age:  10,
	}
	payload, err := json.Marshal(pIn)
	assert.NoError(t, err)

	ctx := context.Background()
	record.
		On("BeforeExecution", ctx, payload).
		Once()

	awslambdaHandler := wrappedHandler.(awslambda.Handler)
	resBytes, err := awslambdaHandler.Invoke(
		ctx,
		payload,
	)
	assert.NoError(t, err)

	var pOut *person
	err = json.Unmarshal(resBytes, &pOut)
	assert.Equal(t, pIn, pOut)
}

func TestWrap_AppliesContextFromPreHook(t *testing.T) {
	handler := func(ctx context.Context, p *person) (*person, error) {
		assert.Equal(t, "value", ctx.Value(ctxKey))
		return p, nil
	}

	record := &recordHook{}
	AddPreHook(record)
	t.Cleanup(func() {
		RemovePreHook(record)
	})

	wrappedHandler := Wrap(handler)
	assert.IsType(t, *new(lambdaHandler), wrappedHandler)
	assert.Implements(t, new(awslambda.Handler), wrappedHandler)

	pIn := &person{
		Name: "x",
		Age:  10,
	}
	payload, err := json.Marshal(pIn)
	assert.NoError(t, err)

	ctx := context.Background()
	record.
		On("BeforeExecution", ctx, payload).
		Once()

	awslambdaHandler := wrappedHandler.(awslambda.Handler)
	resBytes, err := awslambdaHandler.Invoke(
		ctx,
		payload,
	)
	assert.NoError(t, err)

	var pOut *person
	err = json.Unmarshal(resBytes, &pOut)
	assert.Equal(t, pIn, pOut)
}

func TestWrap_RunsPostHook(t *testing.T) {
	handler := func(ctx context.Context, p *person) (*person, error) {
		return p, nil
	}

	record := &recordHook{}
	AddPostHook(record)
	t.Cleanup(func() {
		RemovePostHook(record)
	})

	wrappedHandler := Wrap(handler)
	assert.IsType(t, *new(lambdaHandler), wrappedHandler)
	assert.Implements(t, new(awslambda.Handler), wrappedHandler)

	pIn := &person{
		Name: "x",
		Age:  10,
	}
	payload, err := json.Marshal(pIn)
	assert.NoError(t, err)

	ctx := context.Background()
	record.
		On("AfterExecution", ctx, payload, pIn, nil).
		Once()

	awslambdaHandler := wrappedHandler.(awslambda.Handler)
	resBytes, err := awslambdaHandler.Invoke(
		ctx,
		payload,
	)
	assert.NoError(t, err)

	var pOut *person
	err = json.Unmarshal(resBytes, &pOut)
	assert.Equal(t, pIn, pOut)
}

func TestAddPreHook_AddsHook(t *testing.T) {
	record := &recordHook{}

	lenPreAdd := len(preHooks)
	AddPreHook(record)
	t.Cleanup(func() {
		RemovePreHook(record)
	})
	assert.Equal(t, lenPreAdd+1, len(preHooks))
}

func TestRemovePreHook_RemovesHook(t *testing.T) {
	record := &recordHook{}

	AddPreHook(record)
	lenPreRemove := len(preHooks)
	RemovePreHook(record)
	assert.Equal(t, lenPreRemove-1, len(preHooks))
}

func TestAddPostHook_AddsHook(t *testing.T) {
	record := &recordHook{}

	lenPreAdd := len(postHooks)
	AddPostHook(record)
	t.Cleanup(func() {
		RemovePostHook(record)
	})
	assert.Equal(t, lenPreAdd+1, len(postHooks))
}

func TestRemovePostHook_RemovesHook(t *testing.T) {
	record := &recordHook{}

	AddPostHook(record)
	lenPreRemove := len(postHooks)
	RemovePostHook(record)
	assert.Equal(t, lenPreRemove-1, len(postHooks))
}

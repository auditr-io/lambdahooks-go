package lambdahooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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

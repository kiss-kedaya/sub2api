package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOpsCaptureWriter_NilInnerWriter_NoPanic(t *testing.T) {
	w := &opsCaptureWriter{}
	w.ResponseWriter = nil

	assert.NotPanics(t, func() {
		assert.Equal(t, 0, w.Status())
	}, "Status() on released writer must not panic")

	assert.NotPanics(t, func() {
		assert.Equal(t, -1, w.Size())
	}, "Size() on released writer must not panic")

	assert.NotPanics(t, func() {
		assert.False(t, w.Written())
	}, "Written() on released writer must not panic")

	assert.NotPanics(t, func() {
		n, err := w.Write([]byte("test"))
		assert.Equal(t, 0, n)
		assert.NoError(t, err)
	}, "Write() on released writer must not panic")

	assert.NotPanics(t, func() {
		n, err := w.WriteString("test")
		assert.Equal(t, 0, n)
		assert.NoError(t, err)
	}, "WriteString() on released writer must not panic")
}

//go:build unit

package handler

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestIsRegisterEmailTaken(t *testing.T) {
	require.True(t, isRegisterEmailTaken(service.ErrEmailExists))
	require.True(t, isRegisterEmailTaken(errors.Join(errors.New("wrapped"), service.ErrEmailExists)))
	require.False(t, isRegisterEmailTaken(nil))
	require.False(t, isRegisterEmailTaken(service.ErrRegDisabled))
	require.False(t, isRegisterEmailTaken(errors.New("smtp down")))
}

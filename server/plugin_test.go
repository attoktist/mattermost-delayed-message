package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPluginID(t *testing.T) {
	require.Equal(t, "com.attoktist.scheduled-posts", pluginID)
}

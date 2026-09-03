package sk8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOutputVolumeName(t *testing.T) {
	assert.Equal(t, getOutputVolumeName("abc/def%blah😀"), "sk8s-out-abc_def_blah_")
}

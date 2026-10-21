package rules_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/rules"
)

func TestTurnsAndTurnAreDescribedInTheGeneratedTypes(t *testing.T) {
	decl := rules.Declarations(config.ShippedRules())

	assert.Contains(t, decl, "/** A turn the worker's current Claude session ended. */\n  export interface Turn {")
	assert.Contains(t, decl, "    readonly turns: Turn[];")
	for _, field := range reflect.VisibleFields(reflect.TypeFor[rules.Turn]()) {
		t.Run(field.Name, func(t *testing.T) {
			assert.NotEmpty(t, field.Tag.Get("doc"))
			assert.Equal(t, field.Tag.Get("js"), field.Tag.Get("json"))
		})
	}
	turns, _ := reflect.TypeFor[rules.Worker]().FieldByName("Turns")
	assert.NotEmpty(t, turns.Tag.Get("doc"))
}

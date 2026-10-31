package harnesstest_test

// Package exception: this package is a fake of a harness, so every test here is a contract test of the fake.

import (
	"testing"

	"github.com/kapetan-io/dispatch/internal/harnesstest"
)

func TestFakeHarness(t *testing.T) {
	for _, scenario := range harnesstest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			scenario.Run(t, harnesstest.Subject{
				Harness:     harnesstest.New(),
				StopInput:   harnesstest.StopInput,
				PromptInput: harnesstest.PromptInput,
			})
		})
	}
}

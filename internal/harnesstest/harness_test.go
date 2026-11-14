package harnesstest_test

// Package exception: this package is a fake of a harness, so every test here is a contract test of the fake.

import (
	"testing"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harnesstest"
)

func TestFakeHarness(t *testing.T) {
	for _, scenario := range harnesstest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			fake := harnesstest.New()
			scenario.Run(t, harnesstest.Subject{
				Harness: fake,
				OpenSession: func(*testing.T) harnesstest.Session {
					address := fake.Open()
					return harnesstest.Session{
						Env:       environment.Environment{harnesstest.InboxEnv + "=" + address},
						Address:   address,
						Delivered: func() []string { return fake.Delivered(address) },
						Exit:      func() { fake.Exit(address) },
					}
				},
				StopInput:  harnesstest.StopInput,
				StopSaying: harnesstest.StopSaying,
				Transcript: func(t *testing.T, taken ...int64) string {
					ref := t.Name()
					for _, at := range taken {
						fake.Take(ref, at)
					}
					return ref
				},
				PromptInput: harnesstest.PromptInput,
			})
		})
	}
}

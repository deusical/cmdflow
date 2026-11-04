package cmdflow_test

import (
	"fmt"
	"strings"

	"github.com/deusical/cmdflow"
)

// A host for a tiny greeting bot: one trigger, a pure module, and a "say"
// effect that only happens through the host.
type greetCtx struct{}

type greetHost struct{ said []string }

func (h *greetHost) Effect(s *cmdflow.Step) error {
	h.said = append(h.said, s.Text("text"))
	return nil
}
func (h *greetHost) Halt(*cmdflow.Step, cmdflow.Value) error { return nil }
func (h *greetHost) Save() error                             { return nil }
func (h *greetHost) Fail(string) error                       { return nil }

func Example() {
	e := cmdflow.New[greetCtx](cmdflow.Policy{DefaultTrigger: "hello", RequireEffect: true, EffectLast: true})
	e.RegisterType(cmdflow.Type{ID: "text", Title: "Text"})
	e.RegisterTrigger(cmdflow.Trigger{ID: "hello", Title: "Someone says hello",
		Outputs: []cmdflow.Port{{Name: "name", Title: "Their name", Types: []string{"text"}}}})
	e.Register(cmdflow.Module[greetCtx]{
		Definition: cmdflow.Definition{ID: "greet", Version: 1, Title: "Greet", Effect: cmdflow.Pure, Immediate: true,
			Inputs:  []cmdflow.Port{{Name: "name", Title: "Name", Types: []string{"text"}, Required: true}},
			Outputs: []cmdflow.Port{{Name: "text", Title: "Greeting", Types: []string{"text"}}}},
		Accept: func(_ greetCtx, s *cmdflow.Step) (cmdflow.Outcome, error) {
			s.Out("text", cmdflow.Value{Type: "text", Text: "Hi, " + strings.TrimSpace(s.Text("name")) + "!"})
			return cmdflow.Done(), nil
		},
	})
	e.Register(cmdflow.Module[greetCtx]{
		Definition: cmdflow.Definition{ID: "say", Version: 1, Title: "Say", Effect: cmdflow.Sink,
			Inputs: []cmdflow.Port{{Name: "text", Title: "Text", Types: []string{"text"}, Required: true}}},
		Accept: func(greetCtx, *cmdflow.Step) (cmdflow.Outcome, error) { return cmdflow.Done(), nil },
	})

	// The recipe is configuration, checked once when it's saved.
	recipe := cmdflow.Pipeline{Steps: []cmdflow.StepConfig{
		{ID: "greeting", Module: "greet", Inputs: map[string]string{"name": "trigger.name"}},
		{ID: "out", Module: "say", Inputs: map[string]string{"text": "greeting.text"}},
	}}
	if err := e.Validate(&recipe, nil); err != nil {
		panic(err)
	}

	// Each event snapshots the recipe. Accept works out the greeting but
	// nothing acts yet: the host stores the accepted run, then Resume hands
	// the reply's intent to the host.
	run := cmdflow.Snapshot(recipe, map[string]cmdflow.Value{"name": {Type: "text", Text: "Ada"}})
	host := &greetHost{}
	accepted, _ := e.Accept(greetCtx{}, run, host)
	fmt.Println(accepted, host.said)
	stopped, _ := e.Resume(greetCtx{}, run, host)
	fmt.Println(stopped, host.said)
	// Output:
	// ready []
	// false [Hi, Ada!]
}

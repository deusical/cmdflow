package main

import (
	"encoding/json"
	"testing"

	"github.com/deusical/cmdflow"
)

func TestCommandsAreValid(t *testing.T) {
	e := engine()
	var commands map[string]cmdflow.Pipeline
	if err := json.Unmarshal(commandsJSON, &commands); err != nil {
		t.Fatal(err)
	}
	for name, p := range commands {
		if err := e.Validate(&p, nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if _, halt, err := e.Simulate(&ctx{}, cmdflow.Snapshot(p, map[string]cmdflow.Value{"args": text("hi")})); err != nil || halt != nil {
			t.Errorf("%s: preview %v %v", name, halt, err)
		}
	}
}

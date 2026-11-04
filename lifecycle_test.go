package cmdflow

import (
	"encoding/json"
	"errors"
	"testing"
)

// flakyHost's Effect fails while down is set.
type flakyHost struct {
	toyHost
	down bool
}

func (h *flakyHost) Effect(s *Step) error {
	if h.down {
		return errors.New("transport down")
	}
	return h.toyHost.Effect(s)
}

func mustValidate(t *testing.T, e *Engine[*toyCtx], p Pipeline) Pipeline {
	t.Helper()
	if err := e.Validate(&p, nil); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFailedEffectIsRetriedWithoutRunningTheModuleAgain(t *testing.T) {
	e := toyEngine(Policy{})
	runs := 0
	e.Register(Module[*toyCtx]{
		Definition: Definition{ID: "send", Version: 1, Title: "Send", Effect: Sink, Inputs: []Port{{Name: "text", Types: []string{"text"}, Required: true}}},
		Run:        func(*toyCtx, *Step) (Outcome, error) { runs++; return Done(), nil },
	})
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{
		step("wait", "slow", map[string]string{"in": "trigger.word"}, ""),
		step("say", "send", map[string]string{"text": "wait.out"}, ""),
	}})
	run := Snapshot(p, map[string]Value{"word": text("hi")})
	c, h := &toyCtx{slowReady: true}, &flakyHost{down: true}
	e.Accept(c, run, h)
	if stopped, err := e.Resume(c, run, h); !stopped || err == nil || run.Steps[2].Status != StatusIntent {
		t.Fatal("a failed effect was treated as done", stopped, err, run.Steps[2].Status)
	}
	h.down = false
	if stopped, err := e.Resume(c, run, h); stopped || err != nil || len(h.effects) != 1 || h.effects[0] != "hi." || runs != 1 {
		t.Fatal("intent not delivered on retry, or module ran twice", stopped, err, h.effects, runs)
	}
}

func TestNothingActsAtAcceptance(t *testing.T) {
	e := toyEngine(Policy{})
	e.Register(Module[*toyCtx]{
		Definition: Definition{ID: "no", Version: 1, Title: "No", Effect: Pure},
		Accept:     func(*toyCtx, *Step) (Outcome, error) { return Decline(), nil },
	})
	// A sink that finishes at acceptance, then a step that declines.
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{step("say", "print", map[string]string{"text": "trigger.word"}, ""), step("n", "no", nil, "")}})
	h := &toyHost{}
	if got, err := e.Accept(&toyCtx{}, Snapshot(p, map[string]Value{"word": text("hi")}), h); got != Declined || err != nil || len(h.effects) != 0 {
		t.Fatal("acted before the run was accepted", got, err, h.effects)
	}
	// A sink that finishes at acceptance, then a step that halts: the run
	// ends before anything acts.
	q := mustValidate(t, e, Pipeline{Steps: []StepConfig{step("say", "print", map[string]string{"text": "trigger.word"}, ""), step("loud", "shout", map[string]string{"in": "trigger.word"}, "")}})
	run := Snapshot(q, map[string]Value{"word": text("")})
	if got, _ := e.Accept(&toyCtx{}, run, h); got != Halted || run.Steps[1].Status != StatusSkipped {
		t.Fatal("halt at acceptance kept the earlier intent", got, run.Steps[1].Status)
	}
	if stopped, err := e.Resume(&toyCtx{}, run, h); stopped || err != nil || len(h.effects) != 0 {
		t.Fatal("a halted run acted", stopped, err, h.effects)
	}
}

func TestAcceptOnceAndResumeOnlyAccepted(t *testing.T) {
	e := toyEngine(Policy{})
	p := mustValidate(t, e, shoutThenPrint.Clone())
	run := Snapshot(p, map[string]Value{"word": text("hi")})
	h := &toyHost{}
	if stopped, err := e.Resume(&toyCtx{}, run, h); !stopped || !errors.Is(err, ErrNotAccepted) || len(h.effects) != 0 {
		t.Fatal("resumed a run that was never accepted", stopped, err)
	}
	e.Accept(&toyCtx{}, run, h)
	if _, err := e.Accept(&toyCtx{}, run, h); !errors.Is(err, ErrAccepted) {
		t.Fatal("accepted twice", err)
	}
	e.Resume(&toyCtx{}, run, h)
	if len(h.effects) != 1 {
		t.Fatal("effects", h.effects)
	}
	run.Format = 2
	if _, err := e.Resume(&toyCtx{}, run, h); !errors.Is(err, ErrFormat) {
		t.Fatal("unknown run format resumed", err)
	}
}

func TestEffectWaitsForEarlierSteps(t *testing.T) {
	e := toyEngine(Policy{})
	// The sink's input is known at acceptance, but the slow step before it
	// hasn't run: the effect waits for it.
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{
		step("w", "slow", map[string]string{"in": "trigger.word"}, ""),
		step("say", "print", map[string]string{"text": "trigger.word"}, ""),
	}})
	run := Snapshot(p, map[string]Value{"word": text("hi")})
	c, h := &toyCtx{}, &toyHost{}
	e.Accept(c, run, h)
	if stopped, _ := e.Resume(c, run, h); !stopped || len(h.effects) != 0 {
		t.Fatal("the effect ran ahead of an earlier step", h.effects)
	}
	c.slowReady = true
	if stopped, _ := e.Resume(c, run, h); stopped || len(h.effects) != 1 {
		t.Fatal("effect not delivered", h.effects)
	}
}

func TestYieldSavesStateAndTakeSurvivesIt(t *testing.T) {
	e := toyEngine(Policy{})
	e.Register(Module[*toyCtx]{
		Definition: Definition{ID: "blob", Version: 1, Title: "Blob", Effect: Pure, Outputs: []Port{{Name: "b", Types: []string{"text"}}}},
		Run: func(_ *toyCtx, s *Step) (Outcome, error) {
			s.Out("b", Value{Type: "text", Data: json.RawMessage(`"big"`)})
			return Done(), nil
		},
	})
	var seen []string
	e.Register(Module[*toyCtx]{
		Definition: Definition{ID: "upload", Version: 1, Title: "Upload", Effect: Local,
			Inputs: []Port{{Name: "in", Types: []string{"text"}, Required: true}}, Outputs: []Port{{Name: "out", Types: []string{"text"}}}},
		Run: func(_ *toyCtx, s *Step) (Outcome, error) {
			var tries int
			_ = s.Load(&tries)
			v, _ := s.Take("in")
			seen = append(seen, string(v.Data))
			if tries++; tries == 1 {
				return Yield(), s.Save(tries)
			}
			s.Out("out", text("ok"))
			return Done(), nil
		},
	})
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{step("b", "blob", nil, ""), step("u", "upload", map[string]string{"in": "b.b"}, ""), step("say", "print", map[string]string{"text": "u.out"}, "")}})
	run := Snapshot(p, nil)
	h := &toyHost{}
	e.Accept(&toyCtx{}, run, h)
	e.Resume(&toyCtx{}, run, h)
	if h.saves != 2 || string(run.Steps[2].State) != "1" {
		t.Fatal("a yield wasn't saved with the step's state", h.saves, string(run.Steps[2].State))
	}
	e.Resume(&toyCtx{}, run, h)
	if len(seen) != 2 || seen[1] != `"big"` || run.Steps[1].Outputs["b"].Data != nil || len(h.effects) != 1 {
		t.Fatal("take lost the data across a yield, or kept it after", seen, h.effects)
	}
}

func TestRuntimeOutputsAndRequiredInputsAreChecked(t *testing.T) {
	e := toyEngine(Policy{})
	e.Register(Module[*toyCtx]{
		Definition: Definition{ID: "liar", Version: 1, Title: "Liar", Effect: Pure, Outputs: []Port{{Name: "out", Types: []string{"text"}}}},
		Run:        func(_ *toyCtx, s *Step) (Outcome, error) { s.Out("out", Value{Type: "number"}); return Done(), nil },
	})
	e.Register(Module[*toyCtx]{
		Definition: Definition{ID: "quiet", Version: 1, Title: "Quiet", Effect: Pure, Outputs: []Port{{Name: "out", Types: []string{"text"}}}},
		Run:        func(*toyCtx, *Step) (Outcome, error) { return Done(), nil },
	})
	for _, module := range []string{"liar", "quiet"} {
		p := mustValidate(t, e, Pipeline{Steps: []StepConfig{step("l", module, nil, ""), step("say", "print", map[string]string{"text": "l.out"}, "")}})
		run := Snapshot(p, nil)
		h := &toyHost{}
		e.Accept(&toyCtx{}, run, h)
		if stopped, _ := e.Resume(&toyCtx{}, run, h); !stopped || h.failed != CodeIncomplete || len(h.effects) != 0 {
			t.Errorf("%s: effect acted on a missing or mistyped value (%q, %v)", module, h.failed, h.effects)
		}
	}
}

func TestValidateChangesNothingWhenItFails(t *testing.T) {
	e := toyEngine(Policy{})
	p := Pipeline{Steps: []StepConfig{step("w", "slow", map[string]string{"in": "trigger.word"}, ""), step("bad", "nope", nil, "")}}
	if e.Validate(&p, nil) == nil || p.Steps[0].Version != 0 || p.Steps[0].Settings != nil || p.Format != 0 {
		t.Fatal("a failed Validate changed the pipeline", p.Steps[0])
	}
	q := mustValidate(t, e, shoutThenPrint.Clone())
	if q.Format != FormatVersion {
		t.Fatal("format not set", q.Format)
	}
	q.Format = 7
	if e.Validate(&q, nil) == nil {
		t.Fatal("unknown format accepted")
	}
}

var errTooBig = errors.New("too big")

func TestSettingsErrorsKeepTheirIdentity(t *testing.T) {
	e := toyEngine(Policy{})
	e.Register(Module[*toyCtx]{Definition: Definition{ID: "z", Version: 1, Title: "Z", Effect: Pure},
		Settings: func(raw json.RawMessage, _ any) (json.RawMessage, error) {
			if string(raw) == `{"port":1}` {
				return nil, Errorf("", "size", "too large")
			}
			return nil, errTooBig
		},
		Run: func(*toyCtx, *Step) (Outcome, error) { return Done(), nil }})
	p := Pipeline{Steps: []StepConfig{step("z", "z", nil, "")}}
	var perr *Error
	if err := e.Validate(&p, nil); !errors.Is(err, errTooBig) || !errors.As(err, &perr) || perr.Step != "z" {
		t.Fatal("settings error lost", err)
	}
	p = Pipeline{Steps: []StepConfig{step("z", "z", nil, `{"port":1}`)}}
	if err := e.Validate(&p, nil); !errors.As(err, &perr) || perr.Step != "z" || perr.Port != "size" {
		t.Fatal("settings error lost its port", err)
	}
}

func TestSimulateLeavesTheRunAndReportsFailures(t *testing.T) {
	e := toyEngine(Policy{})
	e.Register(Module[*toyCtx]{
		Definition: Definition{ID: "f", Version: 1, Title: "F", Effect: Pure, Outputs: []Port{{Name: "out", Types: []string{"text"}}}},
		Run:        func(*toyCtx, *Step) (Outcome, error) { return Done(), nil },
		Simulate:   func(*toyCtx, *Step) (Outcome, error) { return Fail("boom"), nil },
	})
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{step("f", "f", nil, ""), step("say", "print", map[string]string{"text": "f.out"}, "")}})
	run := Snapshot(p, nil)
	var serr *StepError
	if _, _, err := e.Simulate(&toyCtx{}, run); !errors.As(err, &serr) || serr.Step != "f" || serr.Code != "boom" {
		t.Fatal("simulated failure not reported", err)
	}
	if run.Steps[1].Status != StatusPending {
		t.Fatal("Simulate changed the run")
	}
	q := mustValidate(t, e, shoutThenPrint.Clone())
	trace, _, err := e.Simulate(&toyCtx{}, Snapshot(q, map[string]Value{"word": text("hi")}))
	if err != nil || len(trace) != 2 || trace[1].Effect != Sink || trace[1].Inputs["text"].Text != "HI!" {
		t.Fatal("trace doesn't show the intent", trace, err)
	}
}

func TestRegisterRejectsProgrammingErrors(t *testing.T) {
	run := func(*toyCtx, *Step) (Outcome, error) { return Done(), nil }
	cases := map[string]func(e *Engine[*toyCtx]){
		"no id": func(e *Engine[*toyCtx]) {
			e.Register(Module[*toyCtx]{Definition: Definition{Version: 1, Effect: Pure}, Run: run})
		},
		"duplicate": func(e *Engine[*toyCtx]) {
			e.Register(Module[*toyCtx]{Definition: Definition{ID: "shout", Version: 1, Effect: Pure}, Run: run})
		},
		"no version": func(e *Engine[*toyCtx]) {
			e.Register(Module[*toyCtx]{Definition: Definition{ID: "x", Effect: Pure}, Run: run})
		},
		"misspelled sink": func(e *Engine[*toyCtx]) {
			e.Register(Module[*toyCtx]{Definition: Definition{ID: "x", Version: 1, Effect: "Sink"}, Run: run})
		},
		"nothing to call": func(e *Engine[*toyCtx]) {
			e.Register(Module[*toyCtx]{Definition: Definition{ID: "x", Version: 1, Effect: Pure}})
		},
		"immediate no acc": func(e *Engine[*toyCtx]) {
			e.Register(Module[*toyCtx]{Definition: Definition{ID: "x", Version: 1, Effect: Pure, Immediate: true}, Run: run})
		},
		"duplicate port": func(e *Engine[*toyCtx]) {
			e.Register(Module[*toyCtx]{Definition: Definition{ID: "x", Version: 1, Effect: Pure, Inputs: []Port{{Name: "a", Types: []string{"text"}}, {Name: "a", Types: []string{"text"}}}}, Run: run})
		},
		"duplicate type":    func(e *Engine[*toyCtx]) { e.RegisterType(Type{ID: "text"}) },
		"duplicate trigger": func(e *Engine[*toyCtx]) { e.RegisterTrigger(Trigger{ID: "word"}) },
	}
	for name, register := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: registered", name)
				}
			}()
			register(toyEngine(Policy{}))
		}()
	}
}

func TestAcceptOnlyModulesNeedNotBeImmediate(t *testing.T) {
	e := toyEngine(Policy{})
	e.Register(Module[*toyCtx]{Definition: Definition{ID: "ao", Version: 1, Title: "AO", Effect: Pure, Outputs: []Port{{Name: "out", Types: []string{"text"}}}},
		Accept: func(_ *toyCtx, s *Step) (Outcome, error) { s.Out("out", text("x")); return Done(), nil }})
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{step("a", "ao", nil, ""), step("say", "print", map[string]string{"text": "a.out"}, "")}})
	if m, _ := e.Module("ao"); !m.AcceptOnly {
		t.Fatal("a module without Run isn't AcceptOnly")
	}
	// Its input must still be known at acceptance.
	q := Pipeline{Steps: []StepConfig{step("w", "slow", map[string]string{"in": "trigger.word"}, ""), step("loud", "shout", map[string]string{"in": "w.out"}, ""), step("say", "print", map[string]string{"text": "loud.out"}, "")}}
	e.Register(Module[*toyCtx]{Definition: Definition{ID: "ao2", Version: 1, Title: "AO2", Effect: Pure, Inputs: []Port{{Name: "in", Types: []string{"text"}}}},
		Accept: func(*toyCtx, *Step) (Outcome, error) { return Done(), nil }})
	q.Steps = append(q.Steps[:2], step("late", "ao2", map[string]string{"in": "w.out"}, ""), q.Steps[2])
	if e.Validate(&q, nil) == nil {
		t.Fatal("an accept-only step read a value known only later")
	}
	run := Snapshot(p, nil)
	h := &toyHost{}
	if got, _ := e.Accept(&toyCtx{}, run, h); got != Ready {
		t.Fatal(got)
	}
	if stopped, err := e.Resume(&toyCtx{}, run, h); stopped || err != nil || len(h.effects) != 1 {
		t.Fatal(stopped, err, h.effects)
	}
}

func TestIntentSurvivesStorage(t *testing.T) {
	e := toyEngine(Policy{})
	p := mustValidate(t, e, shoutThenPrint.Clone())
	run := Snapshot(p, map[string]Value{"word": text("hi")})
	h := &toyHost{}
	e.Accept(&toyCtx{}, run, h)
	raw, _ := json.Marshal(run)
	var stored Run
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Steps[2].Status != StatusIntent || !stored.Accepted {
		t.Fatal("intent lost in storage", err, stored.Steps[2].Status)
	}
}

package cmdflow

import (
	"errors"
	"strings"
	"testing"
)

// downHost's Halt and Fail fail while down is set.
type downHost struct {
	toyHost
	down         bool
	halts, fails int
}

func (h *downHost) Halt(s *Step, v Value) error {
	h.halts++
	if h.down {
		return errors.New("transport down")
	}
	return h.toyHost.Halt(s, v)
}

func (h *downHost) Fail(code string) error {
	h.fails++
	if h.down {
		return errors.New("transport down")
	}
	return h.toyHost.Fail(code)
}

func TestFailedHaltIsRetried(t *testing.T) {
	e := toyEngine(Policy{})
	// The empty word reaches shout only on Resume, through slow.
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{
		step("w", "slow", map[string]string{"in": "trigger.word"}, `{"suffix":""}`),
		step("loud", "shout", map[string]string{"in": "w.out"}, ""),
		step("say", "print", map[string]string{"text": "loud.out"}, ""),
	}})
	run := Snapshot(p, map[string]Value{"word": text("")})
	c, h := &toyCtx{slowReady: true}, &downHost{down: true}
	e.Accept(c, run, h)
	if stopped, err := e.Resume(c, run, h); !stopped || err == nil || run.Halted() {
		t.Fatal("a failed Halt marked the run halted", stopped, err)
	}
	h.down = false
	if stopped, err := e.Resume(c, run, h); stopped || err != nil || h.halts != 2 || h.halted != "say something" || !run.Halted() {
		t.Fatal("Halt not retried", stopped, err, h.halts, h.halted)
	}
}

func TestFailedHaltAtAcceptanceLeavesTheRunUnaccepted(t *testing.T) {
	e := toyEngine(Policy{})
	p := mustValidate(t, e, shoutThenPrint.Clone())
	run := Snapshot(p, map[string]Value{"word": text("")})
	h := &downHost{down: true}
	if got, err := e.Accept(&toyCtx{}, run, h); got != Declined || err == nil || run.Accepted || run.Halted() {
		t.Fatal("a failed Halt at acceptance changed the run", got, err)
	}
	h.down = false
	if got, err := e.Accept(&toyCtx{}, run, h); got != Halted || err != nil || h.halted != "say something" {
		t.Fatal(got, err, h.halted)
	}
}

func TestFailedFailIsRetried(t *testing.T) {
	e := toyEngine(Policy{})
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{
		step("n", "count", map[string]string{"in": "trigger.word"}, ""),
		step("say", "print", map[string]string{"text": "trigger.word"}, ""),
	}})
	run := Snapshot(p, map[string]Value{"word": text("hi")})
	h := &downHost{down: true}
	e.Accept(&toyCtx{}, run, h)
	if _, err := e.Resume(&toyCtx{}, run, h); err == nil {
		t.Fatal("Fail error not returned")
	}
	if _, _, failed := run.Failed(); failed {
		t.Fatal("run marked failed before the host recorded it")
	}
	h.down = false
	if stopped, err := e.Resume(&toyCtx{}, run, h); !stopped || err != nil || h.fails != 2 || h.failed != "count_broken" {
		t.Fatal("Fail not retried", stopped, err, h.fails, h.failed)
	}
	if step, code, failed := run.Failed(); !failed || step != "n" || code != "count_broken" {
		t.Fatal(step, code, failed)
	}
}

func TestFailWithoutCodeIsIncomplete(t *testing.T) {
	e := toyEngine(Policy{})
	e.Register(Module[*toyCtx]{Definition: Definition{ID: "mute", Version: 1, Title: "Mute", Effect: Pure},
		Run:      func(*toyCtx, *Step) (Outcome, error) { return Fail(""), nil },
		Simulate: func(*toyCtx, *Step) (Outcome, error) { return Fail(""), nil }})
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{step("m", "mute", nil, ""), step("say", "print", map[string]string{"text": "trigger.word"}, "")}})
	run := Snapshot(p, map[string]Value{"word": text("hi")})
	h := &toyHost{}
	e.Accept(&toyCtx{}, run, h)
	e.Resume(&toyCtx{}, run, h)
	if _, code, _ := run.Failed(); code != CodeIncomplete || h.failed != CodeIncomplete {
		t.Fatal(code, h.failed)
	}
	var se *StepError
	if _, _, err := e.Simulate(&toyCtx{}, Snapshot(p, map[string]Value{"word": text("hi")})); !errors.As(err, &se) || se.Code != CodeIncomplete {
		t.Fatal(err)
	}
}

func TestIntentMissingRequiredInputFails(t *testing.T) {
	e := toyEngine(Policy{})
	// quiet finishes without setting its output.
	e.Register(Module[*toyCtx]{Definition: Definition{ID: "quiet", Version: 1, Title: "Quiet", Effect: Pure, Outputs: []Port{{Name: "out", Types: []string{"text"}}}},
		Run: func(*toyCtx, *Step) (Outcome, error) { return Done(), nil }})
	// eager finishes at acceptance whatever its inputs.
	e.Register(Module[*toyCtx]{Definition: Definition{ID: "eager", Version: 1, Title: "Eager", Effect: Sink, Inputs: []Port{{Name: "text", Types: []string{"text"}, Required: true}}},
		Accept: func(*toyCtx, *Step) (Outcome, error) { return Done(), nil },
		Run:    func(*toyCtx, *Step) (Outcome, error) { return Done(), nil }})
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{step("q", "quiet", nil, ""), step("say", "eager", map[string]string{"text": "q.out"}, "")}})
	run := Snapshot(p, nil)
	h := &toyHost{}
	if got, err := e.Accept(&toyCtx{}, run, h); got != Ready || err != nil || run.Steps[2].Status != StatusIntent {
		t.Fatal(got, err, run.Steps[2].Status)
	}
	if stopped, err := e.Resume(&toyCtx{}, run, h); !stopped || err != nil || len(h.effects) != 0 || h.failed != CodeIncomplete {
		t.Fatal("an intent missing a required input was delivered", stopped, err, h.effects, h.failed)
	}
}

func TestAcceptChangesTheRunOnlyWhenItAccepts(t *testing.T) {
	e := toyEngine(Policy{})
	admits, calls := 0, 0
	e.Register(Module[*toyCtx]{Definition: Definition{ID: "gate", Version: 1, Title: "Gate", Effect: Pure},
		Admit: func(*toyCtx, *Step) (bool, error) { admits++; return true, nil },
		Accept: func(*toyCtx, *Step) (Outcome, error) {
			if calls++; calls == 1 {
				return Outcome{}, errors.New("database down")
			}
			return Done(), nil
		}})
	p := mustValidate(t, e, Pipeline{Steps: []StepConfig{
		step("loud", "shout", map[string]string{"in": "trigger.word"}, ""),
		step("g", "gate", nil, ""),
		step("say", "print", map[string]string{"text": "loud.out"}, ""),
	}})
	run := Snapshot(p, map[string]Value{"word": text("hi")})
	h := &toyHost{}
	if _, err := e.Accept(&toyCtx{}, run, h); err == nil {
		t.Fatal("Accept error not returned")
	}
	if run.Steps[1].Status != StatusPending || run.Steps[1].Outputs != nil || run.Accepted {
		t.Fatal("a failed Accept changed the run", run.Steps[1])
	}
	if got, err := e.Accept(&toyCtx{}, run, h); got != Ready || err != nil || admits != 2 {
		t.Fatal("retry didn't admit every step again", got, err, admits)
	}
}

func TestUnvalidatedPipelinesAreRefused(t *testing.T) {
	e := toyEngine(Policy{})
	p := shoutThenPrint.Clone()
	for i := range p.Steps {
		p.Steps[i].Version = 1 // looks complete, but was never validated
	}
	run := Snapshot(p, map[string]Value{"word": text("hi")})
	if _, err := e.Accept(&toyCtx{}, run, &toyHost{}); !errors.Is(err, ErrNotValidated) {
		t.Fatal(err)
	}
	if _, _, err := e.Simulate(&toyCtx{}, run); !errors.Is(err, ErrNotValidated) {
		t.Fatal(err)
	}
}

func TestTriggerValuesAreTypeChecked(t *testing.T) {
	e := toyEngine(Policy{})
	p := mustValidate(t, e, shoutThenPrint.Clone())
	bad := map[string]Value{"word": {Type: "number", Text: "4"}}
	if _, err := e.Accept(&toyCtx{}, Snapshot(p, bad), &toyHost{}); !errors.Is(err, ErrTriggerValue) {
		t.Fatal(err)
	}
	if _, _, err := e.Simulate(&toyCtx{}, Snapshot(p, bad)); !errors.Is(err, ErrTriggerValue) {
		t.Fatal(err)
	}
	// A type that counts as the input's type is fine.
	if got, err := e.Accept(&toyCtx{}, Snapshot(p, map[string]Value{"word": {Type: "shout", Text: "HI"}}), &toyHost{}); got != Ready || err != nil {
		t.Fatal(got, err)
	}
}

func TestErrorsNameWhatIsUnknown(t *testing.T) {
	e := toyEngine(Policy{})
	for name, c := range map[string]struct {
		p    Pipeline
		want string
	}{
		"trigger": {Pipeline{Trigger: "clock", Steps: shoutThenPrint.Steps}, `unknown trigger "clock"`},
		"module":  {Pipeline{Steps: []StepConfig{step("x", "rm", nil, "")}}, `unknown module "rm"`},
		"step id": {Pipeline{Steps: []StepConfig{step("9x", "print", nil, "")}}, "start with a lowercase letter"},
	} {
		if err := e.Validate(&c.p, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCatalogueAndHelpers(t *testing.T) {
	e := toyEngine(Policy{RequireEffect: true})
	var ids []string
	for _, d := range e.Definitions() {
		ids = append(ids, d.ID)
	}
	if strings.Join(ids, ",") != "count,print,shout,slow" {
		t.Fatal(ids)
	}
	if ts := e.Types(); len(ts) != 3 || ts[0].ID != "number" || ts[2].ID != "text" {
		t.Fatal(ts)
	}
	if tr := e.Triggers(); len(tr) != 1 || tr[0].ID != "word" {
		t.Fatal(tr)
	}
	p := shoutThenPrint.Clone()
	if !p.Has("print") || p.Has("slow") || p.StepByID("loud") == nil || p.StepByID("nope") != nil {
		t.Fatal("Has or StepByID")
	}
	q := Pipeline{Steps: []StepConfig{step("loud", "shout", map[string]string{"in": "trigger.word"}, "")}}
	if err := e.Validate(&q, nil); err == nil || err.Error() != "invalid pipeline step loud: the last step must be Print" {
		t.Fatal(err)
	}
	if got := Errorf("", "", "x").Error(); got != "invalid pipeline: x" {
		t.Fatal(got)
	}
	if got := Errorf("s", "in", "x").Error(); got != "invalid pipeline step s.in: x" {
		t.Fatal(got)
	}
	if got := (&StepError{Step: "s", Code: "c"}).Error(); got != "cmdflow: step s failed: c" {
		t.Fatal(got)
	}
	run := Snapshot(mustValidate(t, e, p), nil)
	s := &Step{StepRun: &run.Steps[2], run: run}
	if !s.Bound("text") || s.Bound("other") || len(s.Steps()) != 3 {
		t.Fatal("Bound or Steps")
	}
}

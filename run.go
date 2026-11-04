package cmdflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Run is an accepted command: the snapshot it started with and each step's
// progress. It is one JSON document the host stores (encrypted, if it holds
// anything private) and passes back to Resume.
type Run struct {
	// Format is the run format version, FormatVersion for runs this package
	// makes.
	Format int `json:"format"`
	// Accepted is set once Accept has accepted the run.
	Accepted bool      `json:"accepted,omitempty"`
	Steps    []StepRun `json:"steps"`
}

// Status is where one step of a run stands.
type Status string

// Step statuses.
const (
	// StatusPending: the step hasn't finished; it runs on Resume.
	StatusPending Status = ""
	// StatusIntent: a sink step finished and its intent waits for
	// Host.Effect. Resume delivers it, and retries it if Effect fails,
	// without running the module again.
	StatusIntent Status = "intent"
	// StatusDone: the step finished.
	StatusDone Status = "done"
	// StatusHalted: the step ended the run early with a value.
	StatusHalted Status = "halted"
	// StatusSkipped: the step never ran because the run halted.
	StatusSkipped Status = "skipped"
	// StatusFailed: the step failed with Code; the run ends here.
	StatusFailed Status = "failed"
)

// StepRun is one step's snapshot and state. The step with id TriggerStep
// holds the trigger's outputs.
type StepRun struct {
	ID       string            `json:"id"`
	Module   string            `json:"module"`
	Version  int               `json:"version"`
	Inputs   map[string]string `json:"inputs,omitempty"`
	Settings json.RawMessage   `json:"settings,omitempty"`
	Status   Status            `json:"status,omitempty"`
	// Code is the failure code of a failed step.
	Code    string           `json:"code,omitempty"`
	Outputs map[string]Value `json:"outputs,omitempty"`
	// State is module-private progress, kept with the run.
	State json.RawMessage `json:"state,omitempty"`
}

// Finished reports whether the step's outputs are final.
func (s *StepRun) Finished() bool {
	return s.Status == StatusDone || s.Status == StatusIntent || s.Status == StatusHalted
}

// Halted reports whether a step ended the run early.
func (r *Run) Halted() bool {
	for i := range r.Steps {
		if r.Steps[i].Status == StatusHalted {
			return true
		}
	}
	return false
}

// Failed reports the step that failed the run, and its code.
func (r *Run) Failed() (step, code string, failed bool) {
	for i := range r.Steps {
		if s := &r.Steps[i]; s.Status == StatusFailed {
			return s.ID, s.Code, true
		}
	}
	return "", "", false
}

// Clone copies a run so a caller can change it without affecting the
// original.
func (r *Run) Clone() *Run {
	c := *r
	c.Steps = append([]StepRun(nil), r.Steps...)
	for i := range c.Steps {
		s := &c.Steps[i]
		s.Inputs = copyInputs(s.Inputs)
		s.Settings = copyRaw(s.Settings)
		s.State = copyRaw(s.State)
		s.Outputs = copyValues(s.Outputs)
	}
	return &c
}

func copyValues(in map[string]Value) map[string]Value {
	if in == nil {
		return nil
	}
	out := make(map[string]Value, len(in))
	for k, v := range in {
		v.Data = copyRaw(v.Data)
		out[k] = v
	}
	return out
}

// Value is one typed value passed between steps. Text carries text-like
// values; Handle names something the host stores (never a path); Data is any
// other host-defined content.
type Value struct {
	Type   string          `json:"type"`
	Text   string          `json:"text,omitempty"`
	Handle string          `json:"handle,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// Step is a step being accepted, run or simulated, with its inputs resolved
// against the run's earlier steps.
type Step struct {
	*StepRun
	run *Run
	// def and accepts check outputs; nil for steps the host builds itself.
	def     *Definition
	accepts func(Port, string) bool
	taken   []taken
	bad     string // the first undeclared or mistyped output set
}

type taken struct {
	from *StepRun
	name string
}

// In returns the value connected to port, if its step has finished.
func (s *Step) In(port string) (Value, bool) {
	from, name, ok := s.source(port)
	if !ok || !from.Finished() {
		return Value{}, false
	}
	v, ok := from.Outputs[name]
	return v, ok
}

// Take returns the value connected to port. Once this step finishes, the
// value's Data is cleared in the producing step, so large content is stored
// once. If this step yields or fails instead, the Data stays.
func (s *Step) Take(port string) (Value, bool) {
	from, name, ok := s.source(port)
	if !ok || !from.Finished() {
		return Value{}, false
	}
	v, ok := from.Outputs[name]
	if ok {
		s.taken = append(s.taken, taken{from, name})
	}
	return v, ok
}

// finish completes the step's Takes.
func (s *Step) finish() {
	for _, t := range s.taken {
		v := t.from.Outputs[t.name]
		v.Data = nil
		t.from.Outputs[t.name] = v
	}
	s.taken = nil
}

func (s *Step) source(port string) (*StepRun, string, bool) {
	binding := s.Inputs[port]
	if binding == "" {
		return nil, "", false
	}
	from, name, _ := strings.Cut(binding, ".")
	for i := range s.run.Steps {
		if s.run.Steps[i].ID == from {
			return &s.run.Steps[i], name, true
		}
	}
	return nil, "", false
}

// Bound reports whether port is connected, known yet or not.
func (s *Step) Bound(port string) bool { return s.Inputs[port] != "" }

// Text returns the text of the value connected to port, or "".
func (s *Step) Text(port string) string {
	v, _ := s.In(port)
	return v.Text
}

// Out sets an output. The port must be one the module declares, and the
// value's type must count as the port's type; otherwise the step fails with
// CodeIncomplete when it finishes.
func (s *Step) Out(port string, v Value) {
	if s.def != nil {
		p, ok := portOf(s.def.Outputs, port)
		if !ok || !s.accepts(Port{Types: p.Types[:1]}, v.Type) {
			if s.bad == "" {
				s.bad = port
			}
			return
		}
	}
	if s.Outputs == nil {
		s.Outputs = map[string]Value{}
	}
	s.Outputs[port] = v
}

// Decode reads the step's (normalized) settings.
func (s *Step) Decode(v any) error { return json.Unmarshal(s.Settings, v) }

// Load reads the step's private state; nothing saved leaves v unchanged.
func (s *Step) Load(v any) error {
	if len(s.State) == 0 {
		return nil
	}
	return json.Unmarshal(s.State, v)
}

// Save stores the step's private state in the run. The engine has the host
// save the run when the step yields or finishes.
func (s *Step) Save(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.State = raw
	return nil
}

// Steps is the run's steps, for hosts that read across steps.
func (s *Step) Steps() []StepRun { return s.run.Steps }

// inputs resolves the step's known inputs.
func (s *Step) inputs() map[string]Value {
	out := map[string]Value{}
	for port := range s.Inputs {
		if v, ok := s.In(port); ok {
			out[port] = v
		}
	}
	return out
}

type outcomeKind int

const (
	kindDone outcomeKind = iota
	kindPending
	kindHalt
	kindDecline
	kindYield
	kindFail
)

// Outcome is what a module call reports.
type Outcome struct {
	kind  outcomeKind
	value Value
	code  string
}

// Done: the step finished; its outputs are set.
func Done() Outcome { return Outcome{kind: kindDone} }

// Pending: (at acceptance) the step runs later.
func Pending() Outcome { return Outcome{kind: kindPending} }

// Halt: the run ends here with v, which the host receives.
func Halt(v Value) Outcome { return Outcome{kind: kindHalt, value: v} }

// Decline: (at admission or acceptance) the run is not accepted.
func Decline() Outcome { return Outcome{kind: kindDecline} }

// Yield: the run pauses; the module has arranged with the host how and when
// it resumes.
func Yield() Outcome { return Outcome{kind: kindYield} }

// Fail: the run ends with a failure code, which the host records.
func Fail(code string) Outcome { return Outcome{kind: kindFail, code: code} }

// Host is how the engine reaches the host during a run. A Host value is
// usually made for one run, so Save knows what to store.
type Host interface {
	// Effect carries out (or refuses) a sink step's intent: its connected
	// inputs. It is called only during Resume, never during Accept, and not
	// for modules the policy lets act directly. Delivery is at least once:
	// if the host stops after Effect returns but before the following Save,
	// Effect is called again for the same step on the next Resume, so a host
	// that must not act twice keys its effects by run and step id.
	// Returning an error leaves the intent for the next Resume.
	Effect(s *Step) error
	// Halt receives the value a step ended the run with. Like Effect, it can
	// be called again if the host stops before the following Save.
	Halt(s *Step, v Value) error
	// Save persists the run during Resume: after a step finishes, yields or
	// fails, and after an effect is delivered.
	Save() error
	// Fail records that the run failed with code. The run records it too.
	Fail(code string) error
}

// Snapshot fixes a validated pipeline into a new run, with the trigger's
// outputs. The run shares nothing with p, so later edits to p never reach it.
func Snapshot(p Pipeline, trigger map[string]Value) *Run {
	run := &Run{Format: FormatVersion, Steps: make([]StepRun, 0, len(p.Steps)+1)}
	run.Steps = append(run.Steps, StepRun{ID: TriggerStep, Module: TriggerStep, Version: 1, Status: StatusDone, Outputs: copyValues(trigger)})
	if run.Steps[0].Outputs == nil {
		run.Steps[0].Outputs = map[string]Value{}
	}
	for _, s := range p.Steps {
		run.Steps = append(run.Steps, StepRun{ID: s.ID, Module: s.Module, Version: s.Version, Inputs: copyInputs(s.Inputs), Settings: copyRaw(s.Settings)})
	}
	return run
}

// Failure codes the engine reports through Host.Fail.
const (
	// CodeUnavailable: a step names a module or version that is not
	// registered, or one that cannot run on Resume.
	CodeUnavailable = "module_unavailable"
	// CodeIncomplete: a module returned an outcome it may not, set an output
	// it doesn't declare (or of the wrong type), or a step's required input
	// has no value.
	CodeIncomplete = "step_incomplete"
)

// Errors the engine returns.
var (
	// ErrUnavailable means a run names a module (or version) not registered.
	ErrUnavailable = errors.New("cmdflow: module unavailable")
	// ErrFormat means a run's format is not FormatVersion.
	ErrFormat = errors.New("cmdflow: unsupported run format")
	// ErrAccepted means Accept was called on a run already accepted.
	ErrAccepted = errors.New("cmdflow: run already accepted")
	// ErrNotAccepted means Resume was called on a run Accept never accepted.
	ErrNotAccepted = errors.New("cmdflow: run not accepted")
	// ErrFailed means Resume was called on a run that already failed.
	ErrFailed = errors.New("cmdflow: run failed")
	// ErrNoPreview means a step can't be previewed: its module has no
	// Simulate, or Simulate returned an outcome other than Done, Halt or Fail.
	ErrNoPreview = errors.New("cmdflow: step can't be previewed")
)

// StepError reports a step that failed with a code during Simulate.
type StepError struct {
	Step, Code string
}

func (e *StepError) Error() string { return "cmdflow: step " + e.Step + " failed: " + e.Code }

func (e *Engine[C]) step(run *Run, i int) (*Step, Module[C], error) {
	s := &Step{StepRun: &run.Steps[i], run: run}
	m, ok := e.modules[s.Module]
	if !ok || m.Version != s.Version {
		return s, m, ErrUnavailable
	}
	s.def = &m.Definition
	s.accepts = e.Accepts
	return s, m, nil
}

// finished is the status of a step whose module reported Done: a gated sink
// holds its intent for Host.Effect.
func (e *Engine[C]) finished(m Module[C]) Status {
	if m.Effect == Sink && (e.Policy.Direct == nil || !e.Policy.Direct(m.ID)) {
		return StatusIntent
	}
	return StatusDone
}

// haltAt marks step i halted and every other unfinished step skipped.
func haltAt(run *Run, i int) {
	run.Steps[i].Status = StatusHalted
	for j := range run.Steps {
		if st := run.Steps[j].Status; st == StatusPending || st == StatusIntent {
			run.Steps[j].Status = StatusSkipped
		}
	}
}

// Accepted is the result of accepting a run.
type Accepted int

// String names the result, for logs.
func (a Accepted) String() string {
	switch a {
	case Declined:
		return "declined"
	case Ready:
		return "ready"
	case Halted:
		return "halted"
	}
	return "unknown"
}

const (
	// Declined: the run is not accepted at all. Discard it.
	Declined Accepted = iota
	// Ready: the run is accepted. Store it, then Resume it: pending steps
	// run, and intents fixed at acceptance are delivered.
	Ready
	// Halted: the run is accepted and already ended with a step's value.
	// Nothing acted.
	Halted
)

// Accept admits and accepts a new run: every step's Admit first, then each
// step's Accept in order. Steps that finish here are done in the run; a sink
// step that finishes here holds its intent until Resume delivers it, so
// nothing acts until the host has stored the accepted run. A step that halts
// here ends the run before anything acts. Accept never calls Host.Effect or
// Host.Save.
func (e *Engine[C]) Accept(c C, run *Run, h Host) (Accepted, error) {
	if run.Format != FormatVersion {
		return Declined, ErrFormat
	}
	if run.Accepted {
		return Declined, ErrAccepted
	}
	for i := range run.Steps {
		if run.Steps[i].Status != StatusPending {
			continue
		}
		s, m, err := e.step(run, i)
		if err != nil {
			return Declined, err
		}
		if m.Admit != nil {
			if ok, err := m.Admit(c, s); err != nil || !ok {
				return Declined, err
			}
		}
	}
	for i := range run.Steps {
		if run.Steps[i].Status != StatusPending {
			continue
		}
		s, m, err := e.step(run, i)
		if err != nil {
			return Declined, err
		}
		if m.Accept == nil {
			continue
		}
		out, err := m.Accept(c, s)
		if err != nil {
			return Declined, err
		}
		switch out.kind {
		case kindDecline:
			return Declined, nil
		case kindHalt:
			s.finish()
			haltAt(run, i)
			run.Accepted = true
			return Halted, h.Halt(s, out.value)
		case kindDone:
			if s.bad != "" {
				return Declined, fmt.Errorf("cmdflow: step %s set undeclared or mistyped output %s", s.ID, s.bad)
			}
			s.finish()
			s.Status = e.finished(m)
		case kindPending:
		default:
			return Declined, fmt.Errorf("cmdflow: step %s returned an invalid outcome at acceptance", s.ID)
		}
	}
	run.Accepted = true
	return Ready, nil
}

// Resume runs an accepted run's remaining steps in order, delivering each
// sink step's intent to Host.Effect when its turn comes. stopped is true
// when a step yielded or failed, or Resume returned an error; otherwise
// every step is done, or the run halted.
//
// An error from a module's Run, Host.Effect or Host.Save leaves the run
// where it was, so the host can call Resume again.
func (e *Engine[C]) Resume(c C, run *Run, h Host) (stopped bool, err error) {
	if run.Format != FormatVersion {
		return true, ErrFormat
	}
	if !run.Accepted {
		return true, ErrNotAccepted
	}
	if _, _, failed := run.Failed(); failed {
		return true, ErrFailed
	}
	for i := range run.Steps {
		st := &run.Steps[i]
		switch st.Status {
		case StatusDone, StatusSkipped:
			continue
		case StatusHalted:
			return false, nil
		case StatusIntent:
			if err := e.deliver(run, i, h); err != nil {
				return true, err
			}
			continue
		}
		s, m, err := e.step(run, i)
		if err != nil || m.Run == nil {
			return true, fail(st, CodeUnavailable, h)
		}
		for _, in := range m.Inputs {
			if _, ok := s.In(in.Name); in.Required && !ok {
				return true, fail(st, CodeIncomplete, h)
			}
		}
		out, err := m.Run(c, s)
		if err != nil {
			return true, err
		}
		switch out.kind {
		case kindYield:
			return true, h.Save()
		case kindFail:
			return true, fail(st, out.code, h)
		case kindHalt:
			s.finish()
			haltAt(run, i)
			if err := h.Halt(s, out.value); err != nil {
				return true, err
			}
			if err := h.Save(); err != nil {
				return true, err
			}
			return false, nil
		case kindDone:
			if s.bad != "" {
				return true, fail(st, CodeIncomplete, h)
			}
			s.finish()
			st.Status = e.finished(m)
			if err := h.Save(); err != nil {
				return true, err
			}
			if st.Status == StatusIntent {
				if err := e.deliver(run, i, h); err != nil {
					return true, err
				}
			}
		default:
			return true, fail(st, CodeIncomplete, h)
		}
	}
	return false, nil
}

// deliver hands step i's intent to the host and saves the run once it is
// carried out.
func (e *Engine[C]) deliver(run *Run, i int, h Host) error {
	s := &Step{StepRun: &run.Steps[i], run: run}
	if err := h.Effect(s); err != nil {
		return err
	}
	s.Status = StatusDone
	return h.Save()
}

func fail(st *StepRun, code string, h Host) error {
	st.Status, st.Code = StatusFailed, code
	if err := h.Fail(code); err != nil {
		return err
	}
	return h.Save()
}

// Traced is one simulated step. Inputs are its known inputs: for a sink
// step, the intent it would hand the host.
type Traced struct {
	ID      string
	Module  string
	Effect  Effect
	Inputs  map[string]Value
	Outputs map[string]Value
}

// Simulate previews a run offline, step by step, until it ends or a step
// halts it, without changing run. halt is the halting value, if any. A step
// that fails returns a *StepError.
func (e *Engine[C]) Simulate(c C, run *Run) (trace []Traced, halt *Value, err error) {
	run = run.Clone()
	for i := range run.Steps {
		if run.Steps[i].Status != StatusPending {
			continue
		}
		s, m, err := e.step(run, i)
		if err != nil {
			return trace, nil, err
		}
		if m.Simulate == nil {
			return trace, nil, fmt.Errorf("step %s: %w", s.ID, ErrNoPreview)
		}
		out, err := m.Simulate(c, s)
		if err != nil {
			return trace, nil, err
		}
		switch out.kind {
		case kindDone, kindHalt:
		case kindFail:
			return trace, nil, &StepError{Step: s.ID, Code: out.code}
		default:
			return trace, nil, fmt.Errorf("step %s: %w", s.ID, ErrNoPreview)
		}
		if s.bad != "" {
			return trace, nil, &StepError{Step: s.ID, Code: CodeIncomplete}
		}
		s.finish()
		s.Status = StatusDone
		trace = append(trace, Traced{ID: s.ID, Module: s.Module, Effect: m.Effect, Inputs: s.inputs(), Outputs: s.Outputs})
		if out.kind == kindHalt {
			v := out.value
			return trace, &v, nil
		}
	}
	return trace, nil, nil
}

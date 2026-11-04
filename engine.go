package cmdflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// TriggerStep is the reserved step id whose outputs are the trigger's.
const TriggerStep = "trigger"

// FormatVersion is the version of the Pipeline and Run JSON formats this
// package reads and writes.
const FormatVersion = 1

// Effect is a module's effect class: what running it does.
type Effect string

// Effect classes. Only Sink changes what the engine does: a sink step's
// intent goes to Host.Effect. The others are declarations for the host and
// for anyone reading a command.
const (
	// Pure modules compute outputs from inputs and settings.
	Pure Effect = "pure"
	// Local modules do work on the host, such as downloading a file.
	Local Effect = "local"
	// Paid modules make requests that cost money, such as AI calls.
	Paid Effect = "paid"
	// Sink modules act on the outside world. Unless the policy lets a module
	// act directly, its step's connected inputs are an intent the host's
	// Effect carries out or refuses.
	Sink Effect = "sink"
)

func (f Effect) valid() bool { return f == Pure || f == Local || f == Paid || f == Sink }

// Type is a value type steps exchange. Is lists other types a value of this
// type also counts as: an input accepting "file" accepts a "video" whose Is
// includes "file".
type Type struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Is    []string `json:"is,omitempty"`
}

// Port is one named, typed input or output of a module or trigger. An input
// accepts any of its Types; an output produces its first type.
type Port struct {
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	Types    []string `json:"types"`
	Required bool     `json:"required,omitempty"`
}

// Trigger is a kind of event that starts a run, with the outputs it provides.
type Trigger struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Outputs []Port `json:"outputs"`
}

// Schema describes a module's settings as a small form clients can render:
// an object of properties, each a string, number, boolean or enum with
// optional bounds. EffectiveMaximum is the highest value the host currently
// honours when it is below Maximum. UI is a free-form rendering hint.
// The engine never interprets a Schema; a module's Settings func is the
// authority on what is valid.
type Schema struct {
	Type             string            `json:"type"`
	Properties       map[string]Schema `json:"properties,omitempty"`
	Title            string            `json:"title,omitempty"`
	UI               string            `json:"ui,omitempty"`
	Default          any               `json:"default,omitempty"`
	Minimum          *float64          `json:"minimum,omitempty"`
	EffectiveMaximum *float64          `json:"effective_maximum,omitempty"`
	Maximum          *float64          `json:"maximum,omitempty"`
	Enum             []string          `json:"enum,omitempty"`
	Unit             string            `json:"unit,omitempty"`
}

// Bound returns a pointer to v, for Schema's Minimum and Maximum.
func Bound(v float64) *float64 { return &v }

// Definition is what clients (and anything composing commands) see of a module.
type Definition struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Inputs      []Port `json:"inputs"`
	Outputs     []Port `json:"outputs"`
	// Effect is one of Pure, Local, Paid or Sink.
	Effect Effect `json:"effect"`
	// RetryClass and ResourceClass are free-form hints for the host, such
	// as whether a failed step may be retried or which worker pool runs it.
	RetryClass    string `json:"retry_class,omitempty"`
	ResourceClass string `json:"resource_class,omitempty"`
	// Immediate modules finish when the run is accepted, from values known
	// then. Their outputs are fixed in the accepted run.
	Immediate bool `json:"immediate,omitempty"`
	// LongRunning is a hint for the host, such as to give the run a longer
	// deadline.
	LongRunning bool   `json:"long_running,omitempty"`
	Schema      Schema `json:"settings"`
}

// Module is a registered capability. C is the host's per-run context, passed
// to every call, so modules reach the host's state without the engine
// knowing it.
type Module[C any] struct {
	Definition
	// Settings decodes raw settings strictly, applies defaults and validates
	// them, returning the normalized form saved with the command. env is
	// whatever the host passes to Validate.
	Settings func(raw json.RawMessage, env any) (json.RawMessage, error)
	// AcceptOnly modules work only at acceptance, so every input must be
	// known then. A module without Run is AcceptOnly.
	AcceptOnly bool
	// Admit, when set, runs for every step before any step is accepted;
	// false declines the run.
	Admit func(c C, s *Step) (bool, error)
	// Accept runs when the run is accepted: Done, Pending (runs later),
	// Halt or Decline. Without Accept, the step is pending.
	Accept func(c C, s *Step) (Outcome, error)
	// Run runs a pending step: Done, Yield, Halt or Fail. Returning an error
	// instead leaves the step pending, for the host to retry.
	Run func(c C, s *Step) (Outcome, error)
	// Simulate previews the step offline: Done or Halt.
	Simulate func(c C, s *Step) (Outcome, error)
}

// Pipeline is a command's recipe as configured. It holds only what runs it:
// a client that lays steps out (positions, colours) stores that separately.
//
// Steps are a list today, and each input connects to the trigger or an
// earlier step. Settings are fixed when the command is saved; values that
// change from run to run travel only through ports.
type Pipeline struct {
	// Format is the pipeline format version; Validate sets it, and 0 means
	// FormatVersion.
	Format int `json:"format,omitempty"`
	// Trigger names the trigger kind; empty means the policy's default.
	Trigger string `json:"trigger,omitempty"`
	// ExecutionMinutes is for the host, such as a deadline for long runs.
	ExecutionMinutes int          `json:"execution_minutes,omitempty"`
	Steps            []StepConfig `json:"steps"`
}

// StepConfig is one configured step: a module, its settings and its connections
// (port to "trigger.<output>" or "<step>.<output>").
type StepConfig struct {
	ID       string            `json:"id"`
	Module   string            `json:"module"`
	Version  int               `json:"version,omitempty"`
	Inputs   map[string]string `json:"inputs,omitempty"`
	Settings json.RawMessage   `json:"settings,omitempty"`
}

// Clone copies a pipeline so a caller can change it without affecting the
// original.
func (p Pipeline) Clone() Pipeline {
	p.Steps = append([]StepConfig(nil), p.Steps...)
	for i := range p.Steps {
		p.Steps[i].Inputs = copyInputs(p.Steps[i].Inputs)
		p.Steps[i].Settings = copyRaw(p.Steps[i].Settings)
	}
	return p
}

func copyInputs(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// Has reports whether any step runs the module.
func (p Pipeline) Has(module string) bool {
	for _, s := range p.Steps {
		if s.Module == module {
			return true
		}
	}
	return false
}

// StepByID returns the configured step with the id, or nil.
func (p Pipeline) StepByID(id string) *StepConfig {
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return &p.Steps[i]
		}
	}
	return nil
}

// Policy holds the host's rules for valid pipelines and effects.
type Policy struct {
	// MaxSteps limits the steps after the trigger; 0 means no limit.
	MaxSteps int
	// MaxEffects limits sink steps; 0 means no limit.
	MaxEffects int
	// EffectLast requires every sink step to be the last step. Like
	// RequireEffect, it is defined by position in the list of steps.
	EffectLast bool
	// RequireEffect requires the last step to be a sink.
	RequireEffect bool
	// Direct reports sink modules that act themselves when they run, instead
	// of through the host's Effect.
	Direct func(module string) bool
	// DefaultTrigger is used when a pipeline names none.
	DefaultTrigger string
	// Check adds host rules after the structural checks pass.
	Check func(p *Pipeline, info Info) error
}

// Info describes a structurally valid pipeline, for Policy.Check.
type Info struct {
	// Immediate reports steps (and "trigger") whose outputs are known at
	// acceptance.
	Immediate map[string]bool
	// Consumers maps each connected binding to the steps reading it.
	Consumers map[string][]string
}

// Error explains why a pipeline is invalid, naming the step and port. Err is
// the underlying error, such as one a module's Settings returned.
type Error struct {
	Step, Port, Reason string
	Err                error
}

// Unwrap returns the underlying error, if any.
func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Error() string {
	where := e.Step
	if e.Port != "" {
		where += "." + e.Port
	}
	if where == "" {
		return "invalid pipeline: " + e.Reason
	}
	return "invalid pipeline step " + where + ": " + e.Reason
}

// Errorf builds an Error, for modules and Policy.Check.
func Errorf(step, port, format string, args ...any) error {
	return &Error{Step: step, Port: port, Reason: fmt.Sprintf(format, args...)}
}

var stepPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)

// Engine holds the registered modules, types, triggers and upgrades.
//
// Register everything before use. After that, an Engine is safe for
// concurrent use: Validate, Accept, Resume and Simulate only read it.
// Each Run must be used by one goroutine at a time.
type Engine[C any] struct {
	Policy   Policy
	modules  map[string]Module[C]
	types    map[string]Type
	triggers map[string]Trigger
	upgrades []func(*Pipeline)
}

// New makes an engine with the host's policy.
func New[C any](policy Policy) *Engine[C] {
	return &Engine[C]{Policy: policy, modules: map[string]Module[C]{}, types: map[string]Type{}, triggers: map[string]Trigger{}}
}

// Register adds a module. A module without Accept runs on Resume; one
// without Run works only at acceptance. Register panics on programming
// errors: a duplicate or empty id, a version below 1, an unknown effect
// class, neither Accept nor Run, an Immediate or AcceptOnly module without
// Accept, or a duplicate or untyped port.
func (e *Engine[C]) Register(m Module[C]) {
	switch {
	case m.ID == "":
		panic("cmdflow: module without an id")
	case e.modules[m.ID].ID != "":
		panic("cmdflow: duplicate module " + m.ID)
	case m.Version < 1:
		panic("cmdflow: module " + m.ID + " needs a version of 1 or more")
	case !m.Effect.valid():
		panic("cmdflow: module " + m.ID + " has unknown effect class " + strings.TrimSpace(string(m.Effect)))
	case m.Accept == nil && m.Run == nil:
		panic("cmdflow: module " + m.ID + " has neither Accept nor Run")
	}
	if m.Run == nil {
		m.AcceptOnly = true
	}
	if (m.Immediate || m.AcceptOnly) && m.Accept == nil {
		panic("cmdflow: module " + m.ID + " works at acceptance but has no Accept")
	}
	mustPorts(m.ID, m.Inputs)
	mustPorts(m.ID, m.Outputs)
	if m.Settings == nil {
		m.Settings = NoSettings
	}
	e.modules[m.ID] = m
}

// RegisterType adds a value type. Registering an id twice panics.
func (e *Engine[C]) RegisterType(t Type) {
	if _, exists := e.types[t.ID]; exists {
		panic("cmdflow: duplicate type " + t.ID)
	}
	e.types[t.ID] = t
}

// RegisterTrigger adds a trigger kind. Registering an id twice, or a
// duplicate or untyped output, panics.
func (e *Engine[C]) RegisterTrigger(t Trigger) {
	if _, exists := e.triggers[t.ID]; exists {
		panic("cmdflow: duplicate trigger " + t.ID)
	}
	mustPorts(t.ID, t.Outputs)
	e.triggers[t.ID] = t
}

func mustPorts(owner string, ports []Port) {
	seen := map[string]bool{}
	for _, p := range ports {
		if len(p.Types) == 0 {
			panic("cmdflow: port " + owner + "." + p.Name + " has no type")
		}
		if seen[p.Name] {
			panic("cmdflow: duplicate port " + owner + "." + p.Name)
		}
		seen[p.Name] = true
	}
}

// RegisterUpgrade adds a function that rewrites old configuration before it
// is validated, such as a module split into two. Upgrades run, in the order
// registered, every time Validate runs, so each must leave configuration it
// has already upgraded unchanged.
func (e *Engine[C]) RegisterUpgrade(f func(*Pipeline)) { e.upgrades = append(e.upgrades, f) }

// Module returns a registered module.
func (e *Engine[C]) Module(id string) (Module[C], bool) {
	m, ok := e.modules[id]
	return m, ok
}

// Definitions lists the modules in a stable order.
func (e *Engine[C]) Definitions() []Definition {
	out := []Definition{}
	for _, m := range e.modules {
		out = append(out, m.Definition)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Types lists the value types in a stable order.
func (e *Engine[C]) Types() []Type {
	out := []Type{}
	for _, t := range e.types {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Triggers lists the trigger kinds in a stable order.
func (e *Engine[C]) Triggers() []Trigger {
	out := []Trigger{}
	for _, t := range e.triggers {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Accepts reports whether a port takes a value of type typ, directly or
// through the types it counts as.
func (e *Engine[C]) Accepts(port Port, typ string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(t string) bool {
		if seen[t] {
			return false
		}
		seen[t] = true
		for _, want := range port.Types {
			if want == t {
				return true
			}
		}
		for _, parent := range e.types[t].Is {
			if walk(parent) {
				return true
			}
		}
		return false
	}
	return walk(typ)
}

func portOf(ports []Port, name string) (Port, bool) {
	for _, p := range ports {
		if p.Name == name {
			return p, true
		}
	}
	return Port{}, false
}

// Validate applies upgrades, checks a pipeline's structure and the policy,
// and normalizes its format, settings and versions. env is passed to
// modules' Settings and is otherwise the host's. p changes only when it is
// valid.
func (e *Engine[C]) Validate(p *Pipeline, env any) error {
	q := p.Clone()
	if err := e.validate(&q, env); err != nil {
		return err
	}
	*p = q
	return nil
}

func (e *Engine[C]) validate(p *Pipeline, env any) error {
	for _, up := range e.upgrades {
		up(p)
	}
	if p.Format == 0 {
		p.Format = FormatVersion
	}
	if p.Format != FormatVersion {
		return Errorf("", "", "unsupported format %d", p.Format)
	}
	trigger, ok := e.triggers[e.triggerOf(*p)]
	if !ok {
		return Errorf("", "", "unknown trigger")
	}
	if len(p.Steps) == 0 {
		return Errorf("", "", "add at least one step")
	}
	if e.Policy.MaxSteps > 0 && len(p.Steps) > e.Policy.MaxSteps {
		return Errorf("", "", "at most %d steps", e.Policy.MaxSteps)
	}
	outputs := map[string]map[string]string{TriggerStep: {}}
	immediate := map[string]bool{TriggerStep: true}
	for _, o := range trigger.Outputs {
		outputs[TriggerStep][o.Name] = o.Types[0]
	}
	consumers := map[string][]string{}
	effects := 0
	for i := range p.Steps {
		s := &p.Steps[i]
		if !stepPattern.MatchString(s.ID) || s.ID == TriggerStep {
			return Errorf(s.ID, "", "step ids are 1–24 lowercase letters, digits or _")
		}
		if _, dup := outputs[s.ID]; dup {
			return Errorf(s.ID, "", "duplicate step id")
		}
		m, ok := e.modules[s.Module]
		if !ok {
			return Errorf(s.ID, "", "unknown module")
		}
		if s.Version == 0 {
			s.Version = m.Version
		}
		if s.Version != m.Version {
			return Errorf(s.ID, "", "unsupported module version")
		}
		last := i == len(p.Steps)-1
		if m.Effect == Sink {
			if effects++; e.Policy.MaxEffects > 0 && effects > e.Policy.MaxEffects {
				return Errorf(s.ID, "", "at most %d %s", e.Policy.MaxEffects, plural(e.Policy.MaxEffects, "step that acts", "steps that act"))
			}
			if e.Policy.EffectLast && !last {
				return Errorf(s.ID, "", "%s must be the last step", m.Title)
			}
		}
		if last && e.Policy.RequireEffect && m.Effect != Sink {
			return Errorf(s.ID, "", "the last step must be %s", e.sinkTitles())
		}
		for port := range s.Inputs {
			if _, ok := portOf(m.Inputs, port); !ok {
				return Errorf(s.ID, port, "unknown input")
			}
		}
		known := true // every connected input is known at acceptance
		for _, in := range m.Inputs {
			binding := s.Inputs[in.Name]
			if binding == "" {
				if in.Required {
					return Errorf(s.ID, in.Name, "required input is not connected")
				}
				delete(s.Inputs, in.Name)
				continue
			}
			from, port, ok := strings.Cut(binding, ".")
			typ, exists := outputs[from][port]
			if !ok || !exists {
				return Errorf(s.ID, in.Name, "connect it to the trigger or an earlier step's output")
			}
			if !e.Accepts(in, typ) {
				return Errorf(s.ID, in.Name, "a %s output can't connect to this input", typ)
			}
			known = known && immediate[from]
			consumers[binding] = append(consumers[binding], s.ID)
		}
		settings, err := m.Settings(s.Settings, env)
		if err != nil {
			var perr *Error
			if errors.As(err, &perr) && perr.Step == "" {
				return &Error{Step: s.ID, Port: perr.Port, Reason: perr.Reason, Err: err}
			}
			return &Error{Step: s.ID, Reason: err.Error(), Err: err}
		}
		s.Settings = settings
		outputs[s.ID] = map[string]string{}
		for _, o := range m.Outputs {
			outputs[s.ID][o.Name] = o.Types[0]
		}
		if m.AcceptOnly && !known {
			return Errorf(s.ID, "", "this step needs inputs that are known when the command arrives")
		}
		immediate[s.ID] = known && (m.Immediate || m.AcceptOnly)
	}
	if e.Policy.Check != nil {
		return e.Policy.Check(p, Info{Immediate: immediate, Consumers: consumers})
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (e *Engine[C]) sinkTitles() string {
	titles := []string{}
	for _, d := range e.Definitions() {
		if d.Effect == Sink {
			titles = append(titles, d.Title)
		}
	}
	if len(titles) == 0 {
		return "a step that acts"
	}
	return strings.Join(titles, " or ")
}

func (e *Engine[C]) triggerOf(p Pipeline) string {
	if p.Trigger != "" {
		return p.Trigger
	}
	return e.Policy.DefaultTrigger
}

// StrictSettings decodes raw settings into v, rejecting unknown fields and
// trailing data. Empty settings leave v's defaults.
func StrictSettings(raw json.RawMessage, v any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if raw[0] != '{' {
		return errors.New("settings must be an object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid settings")
	}
	return nil
}

// NoSettings accepts only empty settings.
func NoSettings(raw json.RawMessage, _ any) (json.RawMessage, error) {
	var v struct{}
	if err := StrictSettings(raw, &v); err != nil {
		return nil, err
	}
	return nil, nil
}

// Command chatbot is a small chat bot whose commands are configuration.
//
// It reads lines from standard input. A line such as "!shout hello" runs the
// command named "shout" with "hello" as its argument. Commands are defined in
// commands.json as pipelines of registered modules; the bot never runs code
// from configuration, and replies happen only through the host's Effect.
//
//	go run ./examples/chatbot
//	> !shout hello
//	bot: HELLO!
//	> !slowshout hi
//	(working…)
//	bot: HI!!
package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/deusical/cmdflow"
)

//go:embed commands.json
var commandsJSON []byte

// ctx is the per-run context modules receive.
type ctx struct {
	// workDone reports whether the slow "work" a step waits on is finished.
	workDone bool
}

// host carries out effects and stores the run it is driving. A real host
// would persist the run in a database (encrypted, if it holds anything
// private) instead of keeping the JSON in memory.
type host struct {
	run    *cmdflow.Run
	stored []byte
	failed bool
}

func (h *host) Effect(s *cmdflow.Step) error {
	fmt.Println("bot:", s.Text("text"))
	return nil
}

func (h *host) Halt(_ *cmdflow.Step, v cmdflow.Value) error {
	fmt.Println("bot:", v.Text)
	return nil
}

func (h *host) Save() (err error) {
	h.stored, err = json.Marshal(h.run)
	return err
}

// load reads the stored run back, as a host would after a restart.
func (h *host) load() error {
	h.run = &cmdflow.Run{}
	return json.Unmarshal(h.stored, h.run)
}

func (h *host) Fail(code string) error {
	fmt.Println("bot: sorry, that failed:", code)
	h.failed = true
	return nil
}

func text(s string) cmdflow.Value { return cmdflow.Value{Type: "text", Text: s} }

func engine() *cmdflow.Engine[*ctx] {
	e := cmdflow.New[*ctx](cmdflow.Policy{
		MaxSteps:       8,
		MaxEffects:     1,
		EffectLast:     true,
		RequireEffect:  true,
		DefaultTrigger: "message",
	})
	e.RegisterType(cmdflow.Type{ID: "text", Title: "Text"})
	e.RegisterTrigger(cmdflow.Trigger{ID: "message", Title: "A chat command",
		Outputs: []cmdflow.Port{{Name: "args", Title: "Arguments", Types: []string{"text"}}}})

	in := []cmdflow.Port{{Name: "text", Title: "Text", Types: []string{"text"}, Required: true}}
	out := []cmdflow.Port{{Name: "text", Title: "Text", Types: []string{"text"}}}

	// upper is pure and immediate: it finishes when the command arrives.
	upper := func(_ *ctx, s *cmdflow.Step) (cmdflow.Outcome, error) {
		v, ok := s.In("text")
		if !ok {
			return cmdflow.Pending(), nil
		}
		if strings.TrimSpace(v.Text) == "" {
			return cmdflow.Halt(text("say something to shout")), nil
		}
		var set struct {
			Suffix string `json:"suffix"`
		}
		if err := s.Decode(&set); err != nil {
			return cmdflow.Outcome{}, err
		}
		s.Out("text", text(strings.ToUpper(v.Text)+set.Suffix))
		return cmdflow.Done(), nil
	}
	e.Register(cmdflow.Module[*ctx]{
		Definition: cmdflow.Definition{ID: "text.upper", Version: 1, Title: "Uppercase",
			Effect: cmdflow.Pure, Immediate: true, Inputs: in, Outputs: out,
			Schema: cmdflow.Schema{Type: "object", Properties: map[string]cmdflow.Schema{
				"suffix": {Type: "string", Title: "Suffix", Default: "!"}}}},
		Settings: func(raw json.RawMessage, _ any) (json.RawMessage, error) {
			v := struct {
				Suffix string `json:"suffix"`
			}{Suffix: "!"}
			if err := cmdflow.StrictSettings(raw, &v); err != nil {
				return nil, err
			}
			if len(v.Suffix) > 3 {
				return nil, fmt.Errorf("suffix is at most 3 characters")
			}
			return json.Marshal(v)
		},
		Accept: upper, Run: upper, Simulate: upper,
	})

	// wait stands for slow work, such as a download: it yields until the
	// host says the work is done, and the run resumes from here.
	e.Register(cmdflow.Module[*ctx]{
		Definition: cmdflow.Definition{ID: "work.wait", Version: 1, Title: "Wait for work",
			Effect: cmdflow.Local, LongRunning: true, Inputs: in, Outputs: out},
		Run: func(c *ctx, s *cmdflow.Step) (cmdflow.Outcome, error) {
			if !c.workDone {
				fmt.Println("(working…)")
				return cmdflow.Yield(), nil
			}
			s.Out("text", text(s.Text("text")))
			return cmdflow.Done(), nil
		},
		Simulate: func(_ *ctx, s *cmdflow.Step) (cmdflow.Outcome, error) {
			s.Out("text", text(s.Text("text")))
			return cmdflow.Done(), nil
		},
	})

	// reply is the only module that acts. Its step's input is an intent the
	// host's Effect carries out.
	done := func(_ *ctx, s *cmdflow.Step) (cmdflow.Outcome, error) {
		if _, ok := s.In("text"); !ok {
			return cmdflow.Pending(), nil
		}
		return cmdflow.Done(), nil
	}
	e.Register(cmdflow.Module[*ctx]{
		Definition: cmdflow.Definition{ID: "reply", Version: 1, Title: "Reply",
			Effect: cmdflow.Sink, Inputs: in},
		Accept: done, Run: done, Simulate: done,
	})
	return e
}

func main() {
	e := engine()

	// Commands are configuration, checked once when they're loaded.
	var commands map[string]cmdflow.Pipeline
	if err := json.Unmarshal(commandsJSON, &commands); err != nil {
		log.Fatal(err)
	}
	for name, p := range commands {
		if err := e.Validate(&p, nil); err != nil {
			log.Fatalf("command %s: %v", name, err)
		}
		commands[name] = p
	}

	fmt.Println("Try: !shout hello, !slowshout hi, !shout (empty). Ctrl+D to quit.")
	in := bufio.NewScanner(os.Stdin)
	for fmt.Print("> "); in.Scan(); fmt.Print("> ") {
		name, args, _ := strings.Cut(strings.TrimSpace(in.Text()), " ")
		p, ok := commands[strings.TrimPrefix(name, "!")]
		if !strings.HasPrefix(name, "!") || !ok {
			fmt.Println("bot: unknown command")
			continue
		}
		h := &host{run: cmdflow.Snapshot(p, map[string]cmdflow.Value{"args": text(args)})}
		accepted, err := e.Accept(&ctx{}, h.run, h)
		if err != nil {
			log.Fatal(err)
		}
		if accepted != cmdflow.Ready {
			continue
		}
		// Store the accepted run, then resume it from storage: first while
		// the slow work is still going (the run yields), then once it's done.
		if err := h.Save(); err != nil {
			log.Fatal(err)
		}
		for _, workDone := range []bool{false, true} {
			if err := h.load(); err != nil {
				log.Fatal(err)
			}
			stopped, err := e.Resume(&ctx{workDone: workDone}, h.run, h)
			if err != nil {
				log.Fatal(err)
			}
			if !stopped || h.failed {
				break
			}
		}
	}
}

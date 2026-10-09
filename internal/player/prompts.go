package player

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

// Prompts live next to the code that uses them, as templates, not as string literals
// interleaved with logic. Each prompt file defines a "system" and a "user" block; shared
// blocks live in _shared.tmpl, which defines neither.
//
//go:embed prompts/*.tmpl
var promptFS embed.FS

var funcs = template.FuncMap{"join": strings.Join}

type prompt struct{ t *template.Template }

func mustPrompt(file string) prompt {
	t := template.New(file).Funcs(funcs)
	return prompt{t: template.Must(t.ParseFS(promptFS, "prompts/"+file, "prompts/_shared.tmpl"))}
}

func (p prompt) render(name string, data any) (string, error) {
	var b bytes.Buffer
	if err := p.t.ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("render %s/%s: %w", p.t.Name(), name, err)
	}
	return b.String(), nil
}

// system renders the block that takes no data; failing here is a build-time bug.
func (p prompt) system() string {
	s, err := p.render("system", nil)
	if err != nil {
		panic(err)
	}
	return s
}

var (
	proposePrompt = mustPrompt("propose.tmpl")
	solvePrompt   = mustPrompt("solve.tmpl")
	pickPrompt    = mustPrompt("pick.tmpl")
	assessPrompt  = mustPrompt("assess.tmpl")
	reportPrompt  = mustPrompt("game.md.tmpl")
)

// Proposal is the small model's typed output.
type Proposal struct {
	Commands []string `json:"commands" jsonschema:"minItems=1,maxItems=5" jsonschema_description:"Valid game commands, most promising first."`
}

// Pick is the large model's typed choice among candidates.
type Pick struct {
	Reasoning string `json:"reasoning"`
	Command   string `json:"command" jsonschema_description:"Exactly one of the candidates, verbatim."`
}

// RiddleAnswer is the large model's typed output.
type RiddleAnswer struct {
	Reasoning string `json:"reasoning"`
	Answer    string `json:"answer" jsonschema_description:"The word or short phrase to type after 'answer'."`
}

// proposeView adds to State what the propose prompt needs and the state does not carry.
type proposeView struct {
	*State
	Unexplored []string // exits nobody has walked through yet, as room/direction
	Refused    []string // unexplored exits code judged too risky, with the numbers
}

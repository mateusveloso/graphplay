package triage

import (
	"bytes"
	"embed"
	"fmt"
	"slices"
	"strings"
	"text/template"
)

// Prompts live next to the code that uses them, as templates, not as string literals
// interleaved with logic. Each file defines a "system" and a "user" block.
//
//go:embed prompts/*.tmpl
var promptFS embed.FS

var funcs = template.FuncMap{
	"join":   strings.Join,
	"labels": labels,
	"rejections": func(reviews []Review) []Review {
		return slices.DeleteFunc(slices.Clone(reviews), func(r Review) bool { return r.Approved })
	},
	"rubric": renderRubric,
}

// prompt is one parsed template file.
type prompt struct{ t *template.Template }

func mustPrompt(file string) prompt {
	// Every prompt is parsed with _shared.tmpl, which holds blocks used by more than
	// one file and deliberately defines no "system" or "user" of its own.
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

// system and user are the two halves of a generative call.
func (p prompt) system() string {
	s, err := p.render("system", nil)
	if err != nil {
		panic(err) // a system block takes no data; failing here is a build-time bug
	}
	return s
}

var (
	plannerPrompt = mustPrompt("planner.tmpl")
	writerPrompt  = mustPrompt("writer.tmpl")
	criticPrompt  = mustPrompt("critic.tmpl")
	checkPrompt   = mustPrompt("check.tmpl")
	reportPrompt  = mustPrompt("triage.md.tmpl")
)

func labels(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

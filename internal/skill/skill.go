// Package skill discovers reusable prompts stored as markdown files and exposes
// each as a slash command. Invoking a skill's command submits the skill body
// (with any arguments appended) to the model.
//
// Conceptual mapping: a markdown file under a skills directory becomes a Skill,
// and Register adapts each Skill onto the command.Command contract.
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/command"
)

// Skill is a reusable prompt loaded from a markdown file.
type Skill struct {
	// Name is the slash-command word: the file name without ".md", lowercased.
	Name string
	// Description is a one-line summary, from front matter or the first heading.
	Description string
	// Body is the prompt text submitted to the model when the skill is invoked.
	Body string
}

// Discover reads every *.md file in each existing directory in dirs and parses
// it into a Skill. Directories are processed in the given order; within a
// directory files are sorted by name for determinism. A missing directory is
// skipped rather than treated as an error; a file read error is wrapped with
// its path.
func Discover(dirs []string) ([]Skill, error) {
	var skills []Skill
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading skills dir %s: %w", dir, err)
		}

		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.ToLower(filepath.Ext(e.Name())) != ".md" {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)

		for _, name := range names {
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("reading skill %s: %w", path, err)
			}
			skills = append(skills, parse(name, string(data)))
		}
	}
	return skills, nil
}

// parse turns a file name and its contents into a Skill. The command name is the
// file name without its ".md" extension, lowercased. If the content begins with
// a "---" line, the block up to the next "---" line is treated as front matter
// (simple "key: value" lines, "description" recognized) and the body is what
// follows. Otherwise the description is the first non-empty line with leading
// '#' and spaces stripped, and the body is the entire content.
func parse(fileName, content string) Skill {
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	s := Skill{Name: strings.ToLower(base)}

	if desc, body, ok := parseFrontMatter(content); ok {
		s.Description = desc
		s.Body = strings.TrimSpace(body)
		return s
	}

	s.Description = firstHeading(content)
	s.Body = strings.TrimSpace(content)
	return s
}

// parseFrontMatter extracts the description and body from a file that opens with
// a "---" delimiter. It reports ok=false when there is no opening delimiter or
// no matching closing delimiter, so the caller can fall back to heading parsing.
func parseFrontMatter(content string) (description, body string, ok bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", false
	}

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			for _, fm := range lines[1:i] {
				key, value, found := strings.Cut(fm, ":")
				if !found {
					continue
				}
				if strings.ToLower(strings.TrimSpace(key)) == "description" {
					description = strings.TrimSpace(value)
				}
			}
			body = strings.Join(lines[i+1:], "\n")
			return description, body, true
		}
	}
	// Opening "---" with no closing delimiter: not valid front matter.
	return "", "", false
}

// firstHeading returns the first non-empty line of content with any leading '#'
// characters and surrounding spaces stripped, or "" if there is none.
func firstHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	}
	return ""
}

// Register adds a command.Command adapter for each skill to reg, so each skill
// becomes an invocable slash command.
func Register(reg *command.Registry, skills []Skill) {
	for _, s := range skills {
		reg.Register(adapter{skill: s})
	}
}

// adapter exposes a Skill as a command.Command. Running it submits the skill
// body to the model, appending any arguments.
type adapter struct {
	skill Skill
}

// Name returns the skill's command name.
func (a adapter) Name() string { return a.skill.Name }

// Description returns the skill's one-line description.
func (a adapter) Description() string { return a.skill.Description }

// Run returns a SubmitPrompt result carrying the skill body, with args appended
// after a blank line when non-empty. It never errors.
func (a adapter) Run(args string) (command.Result, error) {
	text := a.skill.Body
	if args != "" {
		text += "\n\n" + args
	}
	return command.Result{Action: command.SubmitPrompt, Text: text}, nil
}

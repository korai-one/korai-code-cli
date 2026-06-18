package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/command"
	"github.com/Nevaero/korai-code-cli/internal/skill"
)

// writeFile writes content to name inside dir, failing the test on error.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestDiscover(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []skill.Skill
	}{
		{
			name: "front matter",
			files: map[string]string{
				"Review.md": "---\ndescription: Review a diff\n---\nReview the following code carefully.\n",
			},
			want: []skill.Skill{
				{Name: "review", Description: "Review a diff", Body: "Review the following code carefully."},
			},
		},
		{
			name: "no front matter uses heading",
			files: map[string]string{
				"explain.md": "# Explain this\nDescribe what the code does.",
			},
			want: []skill.Skill{
				{Name: "explain", Description: "Explain this", Body: "# Explain this\nDescribe what the code does."},
			},
		},
		{
			name: "multiple files sorted",
			files: map[string]string{
				"beta.md":  "# Beta\nbeta body",
				"alpha.md": "# Alpha\nalpha body",
			},
			want: []skill.Skill{
				{Name: "alpha", Description: "Alpha", Body: "# Alpha\nalpha body"},
				{Name: "beta", Description: "Beta", Body: "# Beta\nbeta body"},
			},
		},
		{
			name: "non-md files ignored",
			files: map[string]string{
				"keep.md":    "# Keep\nkeep body",
				"ignore.txt": "not a skill",
				"notes.json": "{}",
				"README":     "no extension",
			},
			want: []skill.Skill{
				{Name: "keep", Description: "Keep", Body: "# Keep\nkeep body"},
			},
		},
		{
			name: "front matter without closing falls back to heading",
			files: map[string]string{
				"broken.md": "---\n# Heading\nbody line",
			},
			want: []skill.Skill{
				{Name: "broken", Description: "---", Body: "---\n# Heading\nbody line"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFile(t, dir, name, content)
			}
			got, err := skill.Discover([]string{dir})
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Discover mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiscoverMissingDirSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.md", "# One\nbody")

	missing := filepath.Join(dir, "does-not-exist")
	got, err := skill.Discover([]string{missing, dir})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []skill.Skill{{Name: "one", Description: "One", Body: "# One\nbody"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Discover mismatch (-want +got):\n%s", diff)
	}
}

func TestDiscoverEmpty(t *testing.T) {
	got, err := skill.Discover([]string{filepath.Join(t.TempDir(), "nope")})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d skills, want 0", len(got))
	}
}

func TestRegisterAndRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Greet.md", "---\ndescription: Say hello\n---\nGreet the user warmly.")

	skills, err := skill.Discover([]string{dir})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	reg := command.NewRegistry()
	skill.Register(reg, skills)

	cmd, ok := reg.Get("greet")
	if !ok {
		t.Fatalf("command %q not registered", "greet")
	}
	if cmd.Description() != "Say hello" {
		t.Errorf("Description = %q, want %q", cmd.Description(), "Say hello")
	}

	t.Run("with args appended", func(t *testing.T) {
		got, err := cmd.Run("in French")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		want := command.Result{
			Action: command.SubmitPrompt,
			Text:   "Greet the user warmly.\n\nin French",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Run mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("empty args returns body only", func(t *testing.T) {
		got, err := cmd.Run("")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		want := command.Result{
			Action: command.SubmitPrompt,
			Text:   "Greet the user warmly.",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Run mismatch (-want +got):\n%s", diff)
		}
	})
}

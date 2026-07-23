package agenteval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/edit"
	"github.com/Nevaero/korai-code-cli/internal/tools/readfile"
	"github.com/Nevaero/korai-code-cli/internal/tools/write"
)

// BuiltinLiveScenarios is the default live smoke set: three small, unambiguous
// tasks scored purely by deterministic checks (file result or exact text
// echo). Kept deliberately thin — the offline scenario suite is the substance;
// this layer only proves a real model can drive the loop end to end.
func BuiltinLiveScenarios() []LiveScenario {
	return []LiveScenario{
		{
			Name:   "create_file",
			Prompt: "Create a file named hello.txt whose entire content is exactly this single line: hello korai eval",
			Tools: func() []tool.Tool {
				return []tool.Tool{write.New(), readfile.New()}
			},
			Check: func(workDir, _ string) error {
				return checkFileTrimmed(workDir, "hello.txt", "hello korai eval")
			},
		},
		{
			Name:   "read_codeword",
			Prompt: "Read notes.txt and reply with the codeword it contains, exactly as written.",
			Files:  map[string]string{"notes.txt": "The codeword is TANGERINE-47.\n"},
			Tools: func() []tool.Tool {
				return []tool.Tool{readfile.New()}
			},
			Check: func(_, finalText string) error {
				if !strings.Contains(finalText, "TANGERINE-47") {
					return fmt.Errorf("final text does not echo the codeword: %.200q", finalText)
				}
				return nil
			},
		},
		{
			Name:   "edit_replace",
			Prompt: "In config.txt, change the mode value from development to production. Do not change anything else.",
			Files:  map[string]string{"config.txt": "mode = development\nretries = 3\n"},
			Tools: func() []tool.Tool {
				return []tool.Tool{readfile.New(), edit.New()}
			},
			Check: func(workDir, _ string) error {
				return checkFileTrimmed(workDir, "config.txt", "mode = production\nretries = 3")
			},
		},
	}
}

// checkFileTrimmed compares a workdir file's content to want, tolerating only
// trailing whitespace (models legitimately differ on a final newline).
func checkFileTrimmed(workDir, rel, want string) error {
	data, err := os.ReadFile(filepath.Join(workDir, rel))
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	got := strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), " \t\n")
	if got != strings.TrimRight(want, " \t\n") {
		return fmt.Errorf("%s content mismatch: got %.300q", rel, got)
	}
	return nil
}

package scan

import (
	"testing"

	"github.com/tlmcguire/aisai/internal/model"
)

func TestAnchorLines(t *testing.T) {
	chunk := model.Chunk{
		StartLine: 100,
		EndLine:   106,
		Content:   "def run(cmd):\n    x = 1\n\n    os.system(cmd)\n    y = 2\n    return cmd\n",
	}
	cases := []struct {
		name         string
		start, end   int
		snippet      string
		wantS, wantE int
	}{
		{"correct line is kept", 103, 103, "os.system(cmd)", 103, 103},
		{"snippet-relative number moves into the file", 4, 4, "", 103, 103},
		{"quote wins over a drifted number", 101, 101, "os.system(cmd)", 103, 103},
		{"hallucinated number is clamped", 900, 905, "", 106, 106},
		{"relative number with a quote", 4, 4, "os.system(cmd)", 103, 103},
	}
	for _, c := range cases {
		gotS, gotE := anchorLines(chunk, c.start, c.end, c.snippet)
		if gotS != c.wantS || gotE != c.wantE {
			t.Errorf("%s: got %d-%d, want %d-%d", c.name, gotS, gotE, c.wantS, c.wantE)
		}
	}
}

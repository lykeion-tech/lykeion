package flow

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/review"
	db "lykeion/server/pkg/db/generated"
)

// protocolMentions are the ways a text can tell an agent about the lyk
// protocol rather than its hand-in. They are written to stay clear of
// science prose:
//   - any lyk mention, with or without a backtick (lyk_door is a word of its
//     own and does not match);
//   - a record key: r, c, x or o then digits, lower case, standing alone
//     between spaces, quotes, backticks or brackets, so "R2", "O2" or "c1q"
//     never match (a lower-case "r2" for R² would; no shipped text uses it);
//   - a cite prefix glued to its key, `call:KEY` or `run:<key>` ("what was
//     run: X" has a space and does not match);
//   - a record kind given as an instruction: "record put", a kind in
//     backticks followed by record or item, or a kind that only exists as a
//     record (no_qualifying, withdrawn, source, limitation, explanation,
//     plan, result, audit) followed by record.
var protocolMentions = map[string]*regexp.Regexp{
	"lyk":        regexp.MustCompile("`?\\blyk\\b"),
	"record key": regexp.MustCompile("(?:^|[\\s`\"'(\\[,])[rcxo][0-9]+(?:$|[\\s`\"'),.;:\\]])"),
	"cite":       regexp.MustCompile(`\b(?:call|run):[<A-Za-z0-9]`),
	"record kind": regexp.MustCompile("(?i)\\brecords? put\\b" +
		"|`(?:paper|dataset|no_qualifying|hypothesis|outputs|withdrawn|source|limitation|explanation|plan|result|audit|value|summary)`\\s+(?:record|item)s?\\b" +
		"|\\b(?:no_qualifying|withdrawn|source|limitation|explanation|plan|result|audit)\\s+records?\\b"),
}

func protocolIn(text string) []string {
	var found []string
	for name, re := range protocolMentions {
		if m := re.FindString(text); m != "" {
			found = append(found, name+": "+strings.TrimSpace(m))
		}
	}
	return found
}

// handInTexts is every text a study run reads that Lykeion writes: its prompt and role skill for each role, the
// shipped Teams' instructions and skills, a redo note, and the step cap
// answer ask_member gives a leader.
func handInTexts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	m := &pack.Manifest{Members: []pack.Member{{Name: "x", Role: "Does x."}}}
	files := []db.Attachment{{DiskName: "data.csv", RecordKey: "c1"}}
	comments := []db.Comment{{Body: "please look at mice"}}
	for _, col := range board.Stages {
		for _, role := range []string{"leader", "agent", "auditor", "x"} {
			c := db.Card{Number: 1, BoardColumn: col, Question: "q?", ScopeFrom: board.Grounding, GroundingCriteria: []byte(`["adults"]`)}
			out[col+"/"+role] = prompt(c, role, comments, "a lab", files) + Protocol(role, col, board.Grounding, m)
		}
	}
	for _, name := range []string{"Grounding Team", "Hypotheses Team", "Experimentation Team", "Validation Team"} {
		_, p, err := pack.ReadShipped(pack.Shipped(), name)
		if err != nil {
			t.Fatal(err)
		}
		for _, mem := range append([]pack.Member{p.Manifest.Leader, p.Manifest.Auditor}, p.Manifest.Members...) {
			out[name+"/"+mem.Name] = mem.Agent.Instructions
		}
		err = fs.WalkDir(pack.Shipped(), name+"/skills", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			body, err := fs.ReadFile(pack.Shipped(), path)
			out[path] = string(body)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	out["redo note"] = review.RedoNote(review.Result{
		Items:  []review.Item{{Key: "r4", Kind: review.KindPaper, Label: "doi:10.1/a", Identity: "doi:10.1/a", Status: review.Fail, Checks: []review.Check{{ID: "exists", Status: review.Fail}}}},
		Missed: []review.Item{{Kind: review.KindPaper, Label: "doi:10.1/m", Status: review.Fail}},
	})
	out["step cap"] = ErrStepCap.Error()
	for mode, line := range modeLines {
		out["Validation mode "+mode] = line
	}
	// The server's own failure details reach a redo note too.
	for name, declared := range map[string][]db.Record{"none": nil, "unsaved": {{Key: "r9", Kind: review.KindOutputs, Body: []byte(`{"files":["a.csv"]}`)}}} {
		res, err := review.Experimentation(context.Background(), nil, review.ExperimentationInput{Declared: declared})
		if err != nil {
			t.Fatal(err)
		}
		out["redo note, outputs "+name] = review.RedoNote(res)
	}
	return out
}

func TestNoProtocolInHandInPrompts(t *testing.T) {
	for where, text := range handInTexts(t) {
		if found := protocolIn(text); len(found) > 0 {
			t.Errorf("%s names the protocol (%s):\n%s", where, strings.Join(found, "; "), text)
		}
	}
}

// Each pattern catches the mechanics it is for, and none fires on the
// science prose around it.
func TestProtocolMentionsCatchEachKind(t *testing.T) {
	for line, want := range map[string]string{
		"Save it with lyk output put.":                    "lyk",
		"Run `lyk tools \"x\"`.":                          "lyk",
		"Cite the Grounding record (r3) it rests on.":     "record key",
		"cites [\"c12\"]":                                 "record key",
		"Each answer starts with call:<key>.":             "cite",
		"Cite the value as run:x2.":                       "cite",
		"Write it with record put limitation.":            "record kind",
		"Declare it as a `paper` item.":                   "record kind",
		"If nothing fits, write a no_qualifying record.":  "record kind",
		"Record your reasoning as an explanation record.": "record kind",
	} {
		if found := protocolIn(line); len(found) != 1 || !strings.HasPrefix(found[0], want+":") {
			t.Errorf("%q: found %v, want %s", line, found, want)
		}
	}
	for _, prose := range []string{
		"R2 of 0.8 in C57BL/6 mice, O2 at 21%, the c1q gene and the lyk_door server.",
		"State what was run: the script, its data and its seed.",
		"Every paper must have come back from Lykeion's sources; say why no source qualifies in no_qualifying.",
		"The run record and results.",
	} {
		if found := protocolIn(prose); len(found) > 0 {
			t.Errorf("%q: found %v", prose, found)
		}
	}
	// The guard reads every text it is given: one bad line per kind,
	// added to a real prompt, fails it.
	base := handInTexts(t)["grounding/leader"]
	for _, bad := range []string{"\nThen run lyk team done.", "\nCite r3.", "\nCite call:c3.", "\nWrite a `source` record."} {
		if len(protocolIn(base+bad)) == 0 {
			t.Errorf("the guard missed %q", bad)
		}
	}
}

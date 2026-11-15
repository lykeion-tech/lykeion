package flow

import (
	"strings"

	"lykeion/server/internal/pack"
)

// soloInstructions gives a shipped leader its members' work without delegation.
func soloInstructions(m pack.Manifest) string {
	leader := m.Leader.Agent.Instructions
	leader = strings.Replace(leader, "You lead the "+m.Name+".", "You work this stage alone.", 1)
	start := strings.Index(leader, "How you run the column")
	end := strings.Index(leader, "Stay in the column:")
	if end < 0 {
		end = strings.Index(leader, "Do the column's work and nothing else:")
	}
	if start >= 0 && end > start {
		leader = leader[:start] + leader[end:]
	}
	var lines []string
	for _, line := range strings.Split(leader, "\n") {
		if strings.Contains(line, "ask `") || strings.Contains(line, "hand the search to `") {
			continue
		}
		lines = append(lines, line)
	}
	steps := map[string]string{
		"grounding":       "Read the request and identify every condition on acceptable sources. Search with several distinct queries and fetch promising sources. Check each candidate against every condition using its text; only sources meeting every condition go into the hand-in. If none survives, search again using new angles; stop after two rounds find nothing new. Give the evidence brief, including what was searched, source identifiers and quotes, conflicts and gaps. Use no_qualifying when nothing qualifies.",
		"hypotheses":      "Read the request and earlier study work. Fetch the Grounding sources the hypotheses rely on and identify what they actually show, including direction and size. Use only narrow extra lookups for missing facts, never the study's own answer. Develop testable hypotheses and predictions, cite evidence that supports each prediction, consider rival explanations and controls, and state confidence and uncertainty.",
		"experimentation": "Read the request and earlier work. Follow any pre-stated analysis; otherwise make a concrete plan with data, method, outputs and checks. Find suitable public data if needed; never make it up. Write and run the analysis as a saved script, check its outputs and record each reported value as printed. If the run fails, use the error to try a fix. Save the files behind the results in outputs/ and report what could not be run.",
		"validation":      "Follow the study's stated mode. In run mode, name the measured values and tests from the study; Lykeion determines status from its rerun. In claim mode, list each claim and check it against the study's evidence; look up missing evidence through the sources server or rerun attached analyses when needed. Hand in only checked verdicts and errors, with exact sources and quotations; report uncertainty and missing evidence.",
	}
	var b strings.Builder
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\n" + steps[m.Stage])
	b.WriteString("\n\nWork alone: perform each relevant task below yourself. These are task instructions, not separate hand-ins; include all qualifying items in your own hand-in.\n")
	for _, member := range m.Members {
		text := member.Agent.Instructions
		text = strings.ReplaceAll(text, "the step the leader names", "the researcher's request")
		text = strings.ReplaceAll(text, "candidates the leader hands you", "the candidate sources you found")
		text = strings.ReplaceAll(text, "the reasoning step the leader names", "the reasoning the study needs")
		text = strings.ReplaceAll(text, "the claim the leader names", "the researcher's claim")
		text = strings.ReplaceAll(text, "the attached work the leader names", "the attached work")
		text = strings.ReplaceAll(text, "Leave `papers` and `datasets`\nempty: the verifier hands in the candidates that qualify.", "")
		text = strings.ReplaceAll(text, "Do not\nsearch for new candidates unless the leader asks;", "")
		text = strings.ReplaceAll(text, "Do not run anything.", "")
		b.WriteString("\n" + member.Role + ":\n" + text + "\n")
	}
	return b.String()
}

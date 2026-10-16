package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// stdinObject reads one JSON object from stdin, for records too nested for
// flags.
func stdinObject(e *env) (json.RawMessage, error) {
	b, err := io.ReadAll(e.in)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' || !json.Valid(b) {
		return nil, errors.New("stdin must hold one JSON object")
	}
	return json.RawMessage(b), nil
}

func hypCmd(e *env) *cobra.Command {
	h := &cobra.Command{Use: "hyp", Short: "Hypotheses role commands"}
	fromStdin := func(use, kind, short string) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := stdinObject(e)
			if err != nil {
				return err
			}
			return record(cmd, e, kind, body)
		}}
	}
	var gap, step, candidate, status, source, quote, a, b, winner, reason string
	var queries []string

	missing := &cobra.Command{Use: "missing-premise", Short: "A fact Grounding does not hold; the card returns to Grounding",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return record(cmd, e, "missing_premise", map[string]string{"gap": gap, "step": step})
		}}
	missing.Flags().StringVar(&gap, "gap", "", "the fact Grounding must establish")
	missing.Flags().StringVar(&step, "step", "", "the Grounding role that must find it (default literature-scout)")

	novelty := &cobra.Command{Use: "novelty", Short: "Record what the novelty search found for one candidate",
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := map[string]any{"candidate": candidate, "status": status, "queries": queries}
			if source != "" || quote != "" {
				body["closest"] = map[string]string{"source_id": source, "quote": quote}
			}
			return record(cmd, e, "novelty", body)
		}}
	novelty.Flags().StringVar(&candidate, "candidate", "", "a surviving candidate, H1…")
	novelty.Flags().StringVar(&status, "status", "", "already_stated, related or unknown")
	novelty.Flags().StringArrayVar(&queries, "query", nil, "a query you ran, exactly (repeat for each)")
	novelty.Flags().StringVar(&source, "source", "", "the closest source's id, from fetch_text")
	novelty.Flags().StringVar(&quote, "quote", "", "an exact span of its fetched text")

	compare := &cobra.Command{Use: "compare", Short: "Judge a pair of survivors Jev did not judge",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return record(cmd, e, "comparison", map[string]string{"a": a, "b": b, "winner": winner, "reason": reason})
		}}
	compare.Flags().StringVar(&a, "a", "", "one candidate")
	compare.Flags().StringVar(&b, "b", "", "the other candidate")
	compare.Flags().StringVar(&winner, "winner", "", "the better one to carry forward")
	compare.Flags().StringVar(&reason, "reason", "", "why")

	rank := &cobra.Command{Use: "rank", Short: "Have the server fit the ranking from the pairwise judgments, and print it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := roleClient()
			if err != nil {
				return err
			}
			var out struct {
				Body struct {
					Order  []string           `json:"order"`
					Scores map[string]float64 `json:"scores"`
				} `json:"body"`
			}
			if err := c.Do(cmd.Context(), "POST", "/api/role/records", map[string]any{"kind": "ranking", "body": map[string]any{}}, &out); err != nil {
				return err
			}
			for i, k := range out.Body.Order {
				fmt.Fprintf(e.out, "%d. %s %.4f\n", i+1, k, out.Body.Scores[k])
			}
			return nil
		}}

	cannot := &cobra.Command{Use: "cannot-proceed", Short: "No candidate can be measured: the card stops with no viable hypothesis",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return record(cmd, e, "cannot_proceed", map[string]string{"reason": reason})
		}}
	cannot.Flags().StringVar(&reason, "reason", "", "why")

	h.AddCommand(fromStdin("candidate", "candidate", "Record a candidate hypothesis (JSON on stdin)"), missing,
		fromStdin("decompose", "decomposition", "Record a candidate's assumptions, predictions and tests (JSON on stdin)"),
		novelty, compare, rank, fromStdin("matrix", "matrix", "Record the rivals and their measurement (JSON on stdin)"), cannot)
	return h
}

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// contentFlags adds the three ways a role command takes its text: inline,
// on stdin, or from a file.
func contentFlags(c *cobra.Command, what string) {
	c.Flags().String("content", "", what)
	c.Flags().Bool("content-stdin", false, "read the content from stdin")
	c.Flags().String("content-file", "", "read the content from a file")
}

// content is the text a command was given, from exactly one of its sources.
func content(cmd *cobra.Command, e *env) (string, error) {
	inline, _ := cmd.Flags().GetString("content")
	stdin, _ := cmd.Flags().GetBool("content-stdin")
	file, _ := cmd.Flags().GetString("content-file")
	n := 0
	for _, set := range []bool{inline != "", stdin, file != ""} {
		if set {
			n++
		}
	}
	switch {
	case n > 1:
		return "", errors.New("--content, --content-stdin and --content-file are mutually exclusive")
	case stdin:
		b, err := io.ReadAll(e.in)
		return string(b), err
	case file != "":
		b, err := os.ReadFile(file)
		return string(b), err
	}
	return inline, nil
}

func outputFlag(c *cobra.Command) {
	c.Flags().String("output", "text", "text or json")
}

// emit prints the server's answer as JSON under --output json, and text
// otherwise.
func emit(cmd *cobra.Command, e *env, v any, text string) error {
	if o, _ := cmd.Flags().GetString("output"); o == "json" {
		return json.NewEncoder(e.out).Encode(v)
	}
	if text != "" {
		fmt.Fprintln(e.out, text)
	}
	return nil
}

func teamStepCmd(e *env) *cobra.Command {
	var self bool
	c := &cobra.Command{Use: "step [MEMBER]", Short: "Name the member for the next step; you are woken when its work lands", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			rc, err := roleClient()
			if err != nil {
				return err
			}
			text, err := content(cmd, e)
			if err != nil {
				return err
			}
			in := map[string]any{"content": text}
			switch {
			case self && len(a) == 0:
				in["self"] = true
			case !self && len(a) == 1:
				in["member"] = a[0]
			default:
				return errors.New("name one member, or pass --self for a step you do yourself")
			}
			var out struct {
				Key        string `json:"key"`
				AgentRunID string `json:"agent_run_id,omitempty"`
			}
			if err := rc.Do(cmd.Context(), "POST", "/api/role/steps", in, &out); err != nil {
				return err
			}
			return emit(cmd, e, out, out.Key)
		}}
	c.Flags().BoolVar(&self, "self", false, "announce a step you do yourself")
	contentFlags(c, "what the member should do")
	outputFlag(c)
	return c
}

func teamDoneCmd(e *env) *cobra.Command {
	var cites []string
	c := &cobra.Command{Use: "done", Short: "Declare the column done with a summary naming the record keys it relies on", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := roleClient()
			if err != nil {
				return err
			}
			text, err := content(cmd, e)
			if err != nil {
				return err
			}
			var out struct {
				Key string `json:"key"`
			}
			if err := rc.Do(cmd.Context(), "POST", "/api/role/done", map[string]any{"summary": text, "cites": cites}, &out); err != nil {
				return err
			}
			return emit(cmd, e, out, out.Key)
		}}
	c.Flags().StringSliceVar(&cites, "cites", nil, "record keys the summary relies on")
	contentFlags(c, "the summary")
	outputFlag(c)
	return c
}

func recordCmd(e *env) *cobra.Command {
	r := &cobra.Command{Use: "record", Short: "Write your work as a record of the study"}
	var cites []string
	put := &cobra.Command{Use: "put KIND", Short: "Write one record; the content is a JSON object", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			text, err := content(cmd, e)
			if err != nil {
				return err
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(text), &body); err != nil || body == nil {
				return errors.New("the content must be a JSON object")
			}
			if len(cites) > 0 {
				body["cites"] = cites
			}
			return record(cmd, e, a[0], body)
		}}
	put.Flags().StringSliceVar(&cites, "cites", nil, "the recorded calls the record quotes, as call:KEY")
	contentFlags(put, "the record's JSON body")
	outputFlag(put)
	r.AddCommand(put)
	return r
}

func callsCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "calls", Short: "List the study's recorded database calls in your column", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := roleClient()
			if err != nil {
				return err
			}
			var calls []map[string]any
			if err := rc.Do(cmd.Context(), "GET", "/api/role/calls", nil, &calls); err != nil {
				return err
			}
			if o, _ := cmd.Flags().GetString("output"); o == "json" {
				return emit(cmd, e, calls, "")
			}
			for _, call := range calls {
				fmt.Fprintf(e.out, "%v %v %v\n", call["key"], call["server"], call["tool"])
			}
			return nil
		}}
	outputFlag(c)
	return c
}

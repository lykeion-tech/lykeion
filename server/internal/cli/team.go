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
	put.Flags().StringSliceVar(&cites, "cites", nil, "the recorded calls, runs and records the record rests on, as call:KEY, run:KEY or a record key such as r3")
	contentFlags(put, "the record's JSON body")
	outputFlag(put)
	r.AddCommand(put)
	return r
}

func runsCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "runs", Short: "List the study's recorded code runs in your column", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := roleClient()
			if err != nil {
				return err
			}
			var runs []map[string]any
			if err := rc.Do(cmd.Context(), "GET", "/api/role/runs", nil, &runs); err != nil {
				return err
			}
			if o, _ := cmd.Flags().GetString("output"); o == "json" {
				return emit(cmd, e, runs, "")
			}
			for _, run := range runs {
				path, _ := run["path"].(string)
				fmt.Fprintf(e.out, "%v %v %v %s\n", run["key"], run["tool"], run["language"], path)
			}
			return nil
		}}
	outputFlag(c)
	return c
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

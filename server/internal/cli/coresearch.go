package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/localfiles"
	"lykeion/server/internal/daemon/kernels"
)

const coresearchAPI = "/api/run/coresearch"

var coresearchPollInterval = 500 * time.Millisecond
var coresearchCellWait = kernels.CellTimeout + time.Minute
var coresearchRunWait = 2*time.Hour + time.Minute
var coresearchSetupWait = 2*time.Hour + time.Minute

type commandExit struct {
	code int
	err  error
}

func (e *commandExit) Error() string { return e.err.Error() }
func (e *commandExit) Unwrap() error { return e.err }

// ExitCode maps command errors to the process status used by lyk's entry point.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *commandExit
	if errors.As(err, &exit) {
		return exit.code
	}
	return 1
}
func coresearchStatusError(code int, text string) error { return &commandExit{code, errors.New(text)} }
func coresearchClient() (*apiclient.Client, error) {
	c, err := roleClient()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(c.Token, "ltk_") {
		return nil, errors.New("LYK_TOKEN must be an ltk_ agent run token")
	}
	return c, nil
}
func coresearchPreRun(*cobra.Command, []string) error { return nil }

type coresearchReply struct {
	ID            string          `json:"id"`
	Key           string          `json:"key"`
	Status        string          `json:"status"`
	Outputs       json.RawMessage `json:"outputs"`
	Facts         json.RawMessage `json:"facts"`
	Log           string          `json:"log"`
	FailureReason *string         `json:"failure_reason"`
}
type coresearchFileRef struct {
	Path   string `json:"path"`
	FileID string `json:"file_id"`
	N      int    `json:"n"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func waitCoresearch(cmd *cobra.Command, c *apiclient.Client, endpoint, kind string, limit time.Duration, row *coresearchReply) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), limit)
	defer cancel()
	timeout := func() error {
		if row.Status == "queued" {
			return coresearchStatusError(2, "the chat's machine did not run the "+kind)
		}
		return fmt.Errorf("the %s did not finish before the wait limit", kind)
	}
	for row.Status == "queued" || row.Status == "running" {
		timer := time.NewTimer(coresearchPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if cmd.Context().Err() != nil {
				return cmd.Context().Err()
			}
			return timeout()
		case <-timer.C:
		}
		var next coresearchReply
		if err := c.Do(ctx, "GET", coresearchAPI+endpoint, nil, &next); err != nil {
			if ctx.Err() != nil && cmd.Context().Err() == nil {
				return timeout()
			}
			return err
		}
		*row = next
	}
	switch row.Status {
	case "ok", "error", "failed", "interrupted", "cancelled":
		return nil
	default:
		return fmt.Errorf("unexpected %s status %q", kind, row.Status)
	}
}
func coresearchOutcome(row coresearchReply, kind string) error {
	if row.Status == "ok" {
		return nil
	}
	text := kind + " " + row.Status
	if row.FailureReason != nil && *row.FailureReason != "" {
		text += ": " + *row.FailureReason
	}
	return coresearchStatusError(1, text)
}
func cellCmd(e *env) *cobra.Command {
	group := &cobra.Command{Use: "cell", Short: "Run a cell in this Coresearch chat", PersistentPreRunE: coresearchPreRun}
	var kernel string
	run := &cobra.Command{Use: "run", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := coresearchClient()
		if err != nil {
			return err
		}
		source, err := io.ReadAll(io.LimitReader(e.in, (256<<10)+1))
		if err != nil {
			return err
		}
		if len(source) > 256<<10 {
			return errors.New("cell source can be at most 256 KiB")
		}
		var row coresearchReply
		if err = c.Do(cmd.Context(), "POST", coresearchAPI+"/cells", map[string]string{"kernel": kernel, "source": string(source)}, &row); err != nil {
			return err
		}
		if row.ID == "" {
			return errors.New("cell response has no id")
		}
		if err = waitCoresearch(cmd, c, "/cells/"+url.PathEscape(row.ID), "cell", coresearchCellWait, &row); err != nil {
			return err
		}
		if err = printCellOutputs(e.out, row.ID, row.Outputs); err != nil {
			return err
		}
		return coresearchOutcome(row, "cell")
	}}
	run.Flags().StringVar(&kernel, "kernel", "python3", "kernel name")
	group.AddCommand(run)
	return group
}
func printCellOutputs(w io.Writer, id string, raw json.RawMessage) error {
	var outputs []struct {
		Type      string   `json:"type"`
		Text      string   `json:"text"`
		MIME      string   `json:"mime"`
		Data      []byte   `json:"data"`
		EName     string   `json:"ename"`
		EValue    string   `json:"evalue"`
		Traceback []string `json:"traceback"`
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &outputs); err != nil {
		return err
	}
	for _, o := range outputs {
		switch o.Type {
		case "stream", "text":
			if _, err := fmt.Fprint(w, o.Text); err != nil {
				return err
			}
		case "image":
			if _, err := fmt.Fprintf(w, "[%s %d bytes: cell %s]\n", o.MIME, len(o.Data), id); err != nil {
				return err
			}
		case "error":
			if len(o.Traceback) == 0 {
				if _, err := fmt.Fprintf(w, "%s: %s\n", o.EName, o.EValue); err != nil {
					return err
				}
			}
			for _, line := range o.Traceback {
				if _, err := fmt.Fprintln(w, line); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown cell output type %q", o.Type)
		}
	}
	return nil
}
func coresearchRunCmd(e *env) *cobra.Command {
	var inputs []string
	var rerun string
	cmd := &cobra.Command{Use: "run SCRIPT", Short: "Run a saved script with pinned inputs", Args: cobra.ExactArgs(1), PersistentPreRunE: coresearchPreRun, RunE: func(cmd *cobra.Command, args []string) error {
		c, err := coresearchClient()
		if err != nil {
			return err
		}
		if inputs == nil {
			inputs = []string{}
		}
		var row coresearchReply
		body := struct {
			Script string   `json:"script"`
			Inputs []string `json:"inputs"`
			Rerun  string   `json:"rerun_of_key,omitempty"`
		}{args[0], inputs, rerun}
		if err = c.Do(cmd.Context(), "POST", coresearchAPI+"/runs", body, &row); err != nil {
			return err
		}
		if row.Key == "" {
			return errors.New("run response has no key")
		}
		if err = waitCoresearch(cmd, c, "/runs/"+url.PathEscape(row.Key), "run", coresearchRunWait, &row); err != nil {
			return err
		}
		if _, err = fmt.Fprintf(e.out, "%s %s\n", row.Key, row.Status); err != nil {
			return err
		}
		if len(row.Facts) > 0 && string(row.Facts) != "null" {
			if _, err = fmt.Fprintln(e.out, string(row.Facts)); err != nil {
				return err
			}
		}
		var outputs []coresearchFileRef
		if len(row.Outputs) > 0 {
			if err = json.Unmarshal(row.Outputs, &outputs); err != nil {
				return err
			}
		}
		sort.Slice(outputs, func(i, j int) bool { return outputs[i].Path < outputs[j].Path })
		for _, o := range outputs {
			if _, err = fmt.Fprintf(e.out, "%s v%d\n", o.Path, o.N); err != nil {
				return err
			}
		}
		return coresearchOutcome(row, "run")
	}}
	cmd.Flags().StringArrayVar(&inputs, "in", nil, "saved input path (repeatable)")
	cmd.Flags().StringVar(&rerun, "rerun", "", "repeat a successful run, e.g. R3")
	return cmd
}
func coresearchFolder() (*localfiles.Folder, error) {
	config := os.Getenv("LYK_CONFIG")
	if !filepath.IsAbs(config) || filepath.Base(config) != "config.json" || filepath.Base(filepath.Dir(config)) != ".lyk" {
		return nil, errors.New("LYK_CONFIG must be <chat folder>/.lyk/config.json")
	}
	return localfiles.Open(filepath.Dir(filepath.Dir(config)))
}
func coresearchFileCmd(e *env) *cobra.Command {
	group := &cobra.Command{Use: "file", Short: "Get or save a Coresearch project file", PersistentPreRunE: coresearchPreRun}
	var base, version int
	var force bool
	save := &cobra.Command{Use: "save PATH", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := coresearchClient()
		if err != nil {
			return err
		}
		name, err := coresearch.CleanPath(args[0])
		if err != nil {
			return err
		}
		if base < 0 {
			return errors.New("base must be nonnegative")
		}
		folder, err := coresearchFolder()
		if err != nil {
			return err
		}
		defer folder.Close()
		manifest, err := folder.LockManifest(cmd.Context())
		if err != nil {
			return err
		}
		defer manifest.Close()
		file, err := folder.Read(name)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.Size() > coresearch.MaxFileBytes {
			return errors.New("a saved file can be at most 50 MiB")
		}
		head := make([]byte, 8192)
		n, _ := file.ReadAt(head, 0)
		if coresearch.IsText(head[:n]) && info.Size() > coresearch.MaxTextBytes {
			return errors.New("a text file can be at most 1 MiB")
		}
		chosenBase := base
		if !cmd.Flags().Changed("base") {
			chosenBase = manifest.Entries[name].N
		}
		fields := map[string]string{"path": name, "base": strconv.Itoa(chosenBase)}
		if force {
			fields["force"] = "true"
		}
		var pin localfiles.Pin
		if err = c.Upload(cmd.Context(), coresearchAPI+"/files", fields, "file", path.Base(name), io.LimitReader(file, coresearch.MaxFileBytes+1), &pin); err != nil {
			var h *apiclient.HTTPError
			if errors.As(err, &h) && h.Status == http.StatusConflict {
				return &commandExit{3, err}
			}
			return err
		}
		if pin.N <= 0 || pin.SHA256 == "" {
			return errors.New("saved file response has no version or sha256")
		}
		manifest.Entries[name] = pin
		if err = manifest.Save(); err != nil {
			return err
		}
		_, err = fmt.Fprintf(e.out, "%s v%d\n", name, pin.N)
		return err
	}}
	save.Flags().IntVar(&base, "base", 0, "version to save over (default: local fill version)")
	save.Flags().BoolVar(&force, "force", false, "save over a newer version")
	get := &cobra.Command{Use: "get PATH", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := coresearchClient()
		if err != nil {
			return err
		}
		name, err := coresearch.CleanPath(args[0])
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("version") && version <= 0 {
			return errors.New("version must be positive")
		}
		folder, err := coresearchFolder()
		if err != nil {
			return err
		}
		defer folder.Close()
		target, err := folder.Prepare(name)
		if err != nil {
			return err
		}
		defer target.Close()
		manifest, err := folder.LockManifest(cmd.Context())
		if err != nil {
			return err
		}
		defer manifest.Close()
		chosenVersion := version
		expectedSHA := ""
		if !cmd.Flags().Changed("version") {
			var refs []coresearchFileRef
			if err = c.Do(cmd.Context(), "GET", coresearchAPI+"/files", nil, &refs); err != nil {
				return err
			}
			for _, ref := range refs {
				if ref.Path == name {
					chosenVersion = ref.N
					expectedSHA = ref.SHA256
					break
				}
			}
			if chosenVersion <= 0 {
				return fmt.Errorf("%s is not a saved project file", name)
			}
		}
		q := url.Values{"path": {name}, "n": {strconv.Itoa(chosenVersion)}}
		hash := sha256.New()
		err = target.Replace(func(w io.Writer) error {
			bounded := &coresearchLimitedWriter{writer: io.MultiWriter(w, hash), left: coresearch.MaxFileBytes}
			if err := c.Download(cmd.Context(), coresearchAPI+"/files/content?"+q.Encode(), bounded); err != nil {
				return err
			}
			if expectedSHA != "" && hex.EncodeToString(hash.Sum(nil)) != expectedSHA {
				return errors.New("downloaded file does not match the saved sha256")
			}
			return nil
		})
		if err != nil {
			return err
		}
		manifest.Entries[name] = localfiles.Pin{N: chosenVersion, SHA256: hex.EncodeToString(hash.Sum(nil))}
		if err = manifest.Save(); err != nil {
			return err
		}
		_, err = fmt.Fprintf(e.out, "%s v%d\n", name, chosenVersion)
		return err
	}}
	get.Flags().IntVar(&version, "version", 0, "saved version (default: current head)")
	group.AddCommand(save, get)
	return group
}

type coresearchLimitedWriter struct {
	writer io.Writer
	left   int64
}

func (w *coresearchLimitedWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.left {
		return 0, errors.New("a saved file can be at most 50 MiB")
	}
	n, err := w.writer.Write(b)
	w.left -= int64(n)
	return n, err
}
func coresearchEnvCmd(e *env) *cobra.Command {
	group := &cobra.Command{Use: "env", Short: "Set up this Coresearch project's environment", PersistentPreRunE: coresearchPreRun}
	var python string
	var conda []string
	setup := &cobra.Command{Use: "set-up", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := coresearchClient()
		if err != nil {
			return err
		}
		if conda == nil {
			conda = []string{}
		}
		var row coresearchReply
		body := struct {
			Python string   `json:"python,omitempty"`
			Conda  []string `json:"conda"`
		}{python, conda}
		if err = c.Do(cmd.Context(), "POST", coresearchAPI+"/environment", body, &row); err != nil {
			return err
		}
		if row.ID == "" {
			return errors.New("environment response has no id")
		}
		if err = waitCoresearch(cmd, c, "/environment/"+url.PathEscape(row.ID), "environment setup", coresearchSetupWait, &row); err != nil {
			return err
		}
		lines := strings.Split(strings.TrimRight(row.Log, "\n"), "\n")
		if len(lines) > 50 {
			lines = lines[len(lines)-50:]
		}
		if row.Log != "" {
			if _, err = fmt.Fprintln(e.out, strings.Join(lines, "\n")); err != nil {
				return err
			}
		}
		return coresearchOutcome(row, "environment setup")
	}}
	setup.Flags().StringVar(&python, "python", "", "Python minor version (default: project's existing version)")
	setup.Flags().StringArrayVar(&conda, "conda", nil, "conda package (repeatable)")
	group.AddCommand(setup)
	return group
}

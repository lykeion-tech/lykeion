package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"lykeion/server/internal/apiclient"
	"lykeion/server/pkg/agent"
)

// The server's limits, checked here too so a call that breaks them sends
// nothing.
const (
	maxOutputName     = 512
	maxOutputBytes    = 100 << 20
	maxRunOutputs     = 100
	maxRunOutputBytes = 500 << 20
)

type savedOutput struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

func outputCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "output", Short: "Save files your run wrote, or read your study's saved ones"}
	put := &cobra.Command{Use: "put PATH...", Short: "Save files or folders in the run folder as outputs of your study", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error { return putOutputs(cmd, e, a) }}
	outputFlag(put)
	var to string
	get := &cobra.Command{Use: "get KEY", Short: "Write a saved output to --to, or to stdout", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			rc, err := outputClient()
			if err != nil {
				return err
			}
			w := e.out
			if to != "" {
				f, err := os.Create(to)
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			if err := rc.Download(cmd.Context(), "/api/role/outputs/"+url.PathEscape(a[0]), w); err != nil {
				if to != "" {
					os.Remove(to)
				}
				return err
			}
			return nil
		}}
	get.Flags().StringVar(&to, "to", "", "file to write (default: stdout)")
	c.AddCommand(put, get)
	return c
}

// outputClient is the run's client without the usual whole-request timeout:
// a 100 MB file can take longer than that, and the run's own deadline still
// ends the call.
func outputClient() (*apiclient.Client, error) {
	rc, err := roleClient()
	if err != nil {
		return nil, err
	}
	rc.HTTP = &http.Client{}
	return rc, nil
}

func putOutputs(cmd *cobra.Command, e *env, args []string) error {
	rc, err := outputClient()
	if err != nil {
		return err
	}
	if os.Getenv("LYK_CONFIG") == "" {
		return errors.New("LYK_CONFIG must be set: outputs are saved from inside a Lykeion agent run")
	}
	folder, err := filepath.EvalSymlinks(filepath.Dir(os.Getenv("LYK_CONFIG")))
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(folder)
	if err != nil {
		return err
	}
	defer root.Close()
	files, err := collectOutputs(cmd, root, folder, args)
	if err != nil {
		return err
	}
	// Text output is one JSON object a line, printed as each file lands;
	// --output json prints the list once all have.
	list, _ := cmd.Flags().GetString("output")
	saved := []savedOutput{}
	for _, f := range files {
		s, err := sendOutput(cmd, rc, root, f)
		if err != nil {
			return err
		}
		saved = append(saved, s)
		if list != "json" {
			if err := json.NewEncoder(e.out).Encode(s); err != nil {
				return err
			}
		}
	}
	if list == "json" {
		return emit(cmd, e, saved, "")
	}
	return nil
}

type outputFile struct {
	name string
	info fs.FileInfo
}

// collectOutputs resolves every PATH to the regular files it names inside the
// run folder, refusing the whole call if any PATH or the total breaks a rule.
// Links are never followed; one met is skipped and named on stderr.
func collectOutputs(cmd *cobra.Command, root *os.Root, folder string, args []string) ([]outputFile, error) {
	var files []outputFile
	var total int64
	add := func(name string, info fs.FileInfo) error {
		if slices.ContainsFunc(files, func(f outputFile) bool { return f.name == name }) {
			return nil
		}
		if err := checkOutputName(name); err != nil {
			return err
		}
		if info.Size() > maxOutputBytes {
			return fmt.Errorf("%s: an output can be at most 100 MB", name)
		}
		total += info.Size()
		files = append(files, outputFile{name, info})
		if len(files) > maxRunOutputs {
			return fmt.Errorf("a run can save at most %d outputs", maxRunOutputs)
		}
		if total > maxRunOutputBytes {
			return errors.New("a run's outputs can be at most 500 MB in all")
		}
		return nil
	}
	skip := func(name string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "skipped %s: not a regular file or folder\n", name)
	}
	for _, a := range args {
		name, err := runFolderName(folder, a)
		if err != nil {
			return nil, err
		}
		if err := checkOutputName(name); err != nil {
			return nil, err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return nil, err
		}
		switch {
		case info.Mode().IsRegular():
			err = add(name, info)
		case info.IsDir():
			err = fs.WalkDir(root.FS(), name, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				if !d.Type().IsRegular() {
					skip(p)
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				return add(p, info)
			})
		default:
			skip(name)
		}
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// runFolderName is PATH, resolved against the working folder, as a
// slash-separated path relative to the run folder.
func runFolderName(folder, p string) (string, error) {
	if strings.Contains(p, "://") {
		return "", fmt.Errorf("%s: only files in the run folder can be saved, not URLs", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// The parent is resolved so a folder reached through a link (/tmp on
	// macOS) still compares equal; the last element stays unresolved.
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(folder, filepath.Join(parent, filepath.Base(abs)))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: only files in the run folder can be saved", p)
	}
	return filepath.ToSlash(rel), nil
}

// checkOutputName holds the server's rules for an output's name, and refuses
// the run folder's own configuration, sign-in and study files.
func checkOutputName(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || slices.Contains(strings.Split(p, "/"), "..") || path.Clean(p) == "." {
		return fmt.Errorf("%q: an output's path must be relative to the run folder, with no .. segment, backslash or NUL", p)
	}
	p = path.Clean(p)
	if len(p) > maxOutputName {
		return fmt.Errorf("%s: an output's path can be at most %d bytes", p, maxOutputName)
	}
	first, _, _ := strings.Cut(p, "/")
	if slices.ContainsFunc(agent.RunFolderConfig, func(c agent.RunFolderPath) bool { return c.Name == first }) {
		return fmt.Errorf("%s: the run folder's own configuration and study files can't be saved as outputs", p)
	}
	return nil
}

func sendOutput(cmd *cobra.Command, rc *apiclient.Client, root *os.Root, o outputFile) (savedOutput, error) {
	f, err := root.Open(o.name)
	if err != nil {
		return savedOutput{}, err
	}
	defer f.Close()
	// The open follows a link put in the file's place since the walk; the
	// file must still be the one the walk checked.
	if info, err := f.Stat(); err != nil || !os.SameFile(info, o.info) {
		return savedOutput{}, fmt.Errorf("%s changed while being saved", o.name)
	}
	var s savedOutput
	err = rc.Upload(cmd.Context(), "/api/role/outputs", map[string]string{"path": o.name}, "file", path.Base(o.name), f, &s)
	return s, err
}

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// A raw record is capped at 16 MiB; JSON escaping can expand it sixfold.
// Keep a bounded ordered cancellation tail and one complete overflow record.
const retainedMessageBytes = 256 << 20

type messageRetention struct {
	dir     string
	file    *os.File
	size    int
	started bool
	paths   []string
}

func (r *messageRetention) put(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if !r.started {
		r.file, err = os.CreateTemp(r.dir, ".undelivered-*.jsonl")
		if err != nil {
			return err
		}
		r.started = true
		r.paths = append(r.paths, r.file.Name())
	}
	if r.size+len(b) > retainedMessageBytes {
		p := r.file.Name() + ".overflow.json"
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(b)
		err = errors.Join(err, f.Sync(), f.Close())
		if err != nil {
			return err
		}
		r.paths = append(r.paths, p)
		return fmt.Errorf("cancellation evidence retention limit reached; current record retained in %s", p)
	}
	_, err = r.file.Write(b)

	r.size += len(b)
	return err
}
func (r *messageRetention) close() error {
	if r.file == nil {
		return nil
	}
	err := errors.Join(r.file.Sync(), r.file.Close())
	r.file = nil
	if err == nil {
		dir, e := os.Open(r.dir)
		if e != nil {
			return e
		}
		err = errors.Join(dir.Sync(), dir.Close())
	}
	return err
}

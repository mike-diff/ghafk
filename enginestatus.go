package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mike-diff/ghafk/internal/harness"
)

const maxTickLog = 1 << 20

type repoStatus struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type engineStatus struct {
	Time         string       `json:"time"`
	Error        string       `json:"error,omitempty"`
	Owner        string       `json:"owner,omitempty"`
	TokenExpires string       `json:"tokenExpires,omitempty"`
	Account      string       `json:"account,omitempty"`
	Repos        []repoStatus `json:"repos,omitempty"`
	Harnesses    []string     `json:"harnesses"`
}

func statusFile(home string) string {
	return filepath.Join(home, ".ghafk", "status.json")
}

func writeEngineStatus(home string, st engineStatus) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := statusFile(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func installedHarnesses(henv harness.Env) []string {
	names := []string{}
	for name, p := range henv.Profiles {
		if harnessOnPath(p.Command) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func startTickLog(home string) (func(), error) {
	path := filepath.Join(home, ".ghafk", "tick.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxTickLog {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(file, "--- tick %s\n", time.Now().Format(time.RFC3339))
	log := lockedWriter{mu: &sync.Mutex{}, w: file}
	origOut, origErr := os.Stdout, os.Stderr
	done := make(chan struct{}, 2)
	tee := func(orig *os.File) *os.File {
		r, w, err := os.Pipe()
		if err != nil {
			done <- struct{}{}
			return orig
		}
		go func() {
			io.Copy(io.MultiWriter(orig, log), r)
			r.Close()
			done <- struct{}{}
		}()
		return w
	}
	os.Stdout, os.Stderr = tee(origOut), tee(origErr)
	return func() {
		outW, errW := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = origOut, origErr
		if outW != origOut {
			outW.Close()
		}
		if errW != origErr {
			errW.Close()
		}
		for i := 0; i < 2; i++ {
			select {
			case <-done:
			case <-time.After(pipeGrace):
			}
		}
		file.Close()
	}, nil
}

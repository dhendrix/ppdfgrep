// SPDX-FileCopyrightText: 2026 David Hendricks <david.hendricks@gmail.com>
//
// SPDX-License-Identifier: BSD-3-Clause
//
// This is a wrapper for `pdfgrep` that will run concurrent instances for
// every PDF file specified or found in a directory hierarchy. Useful for
// pdfgrepping piles of datasheets.
//
// Like pdfgrep itself, it exits 0 when a match is found and 1 when no
// match was found (or pdfgrep failed on a file). Tool errors - bad
// usage, a missing pdfgrep, or unreadable input - exit 2.
//
// TODO: consider using a native PDF library such as rsc.io/pdf

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/h2non/filetype"
)

// pdfgrepPath is the external binary that does the actual matching; it is
// expected to be on the user's $PATH.
const pdfgrepPath = "pdfgrep"

// Exit codes, following pdfgrep's own convention (see `man pdfgrep`).
const (
	exitOK    = 0 // a match was found
	exitNoHit = 1 // no match found, or pdfgrep failed on a file
	exitError = 2 // tool error: bad usage, missing pdfgrep, unreadable input
)

// fileResult holds the outcome of grepping a single file.
type fileResult struct {
	output   []byte
	exitCode int
}

// splitArgs separates ppdfgrep's own options (currently only
// -r / --recursive) from the options forwarded to pdfgrep and the
// positional arguments (PATTERN and FILE...). Arguments are parsed by
// hand because the forwarded options belong to pdfgrep and should not be
// validated here.
func splitArgs(args []string) (recurse bool, flags, positional []string) {
	flags = []string{}
	positional = []string{}

	for _, arg := range args {
		switch {
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		case arg == "--recursive":
			recurse = true
		case !strings.HasPrefix(arg, "--") && strings.Contains(arg, "r"):
			// Short options may be combined, e.g. "-lr"; pull out "r".
			recurse = true
			arg = strings.ReplaceAll(arg, "r", "")
			if len(arg) > 1 {
				flags = append(flags, arg)
			}
		default:
			if len(arg) > 1 {
				flags = append(flags, arg)
			}
		}
	}
	return
}

// isPDF reports whether the file at path looks like a PDF by sniffing
// its magic bytes.
func isPDF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("checking %s: %v", path, err)
		return false
	}
	defer f.Close()

	// Only the first bytes are needed to identify the file type, so a
	// short read is fine.
	header := make([]byte, 261)
	n, err := f.Read(header)
	if n == 0 {
		if err != nil && err != io.EOF {
			log.Printf("reading %s: %v", path, err)
		}
		return false
	}
	return filetype.IsMIME(header[:n], "application/pdf")
}

// collectPDFs returns the PDF files found under each root. Directory
// roots are walked recursively only when recurse is set. Errors on
// individual entries (e.g. an unreadable subdirectory) are logged and
// ignored; a root that cannot even be stat'd is returned as an error.
func collectPDFs(recurse bool, roots []string) ([]string, error) {
	var files []string
	var walkErr error

	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if info == nil {
					// The root itself could not be stat'd; fail the walk.
					return err
				}
				// Soft error on a sub-entry; log and keep going.
				log.Printf("%s: %v", path, err)
				return nil
			}

			// Skip hidden files, but still descend into hidden
			// directories (e.g. when the root itself is one).
			if !info.IsDir() && strings.HasPrefix(info.Name(), ".") {
				return nil
			}

			if info.IsDir() {
				if path != root && !recurse {
					return filepath.SkipDir
				}
				return nil
			}

			if isPDF(path) {
				files = append(files, path)
			} else if strings.EqualFold(filepath.Ext(path), ".pdf") {
				log.Printf("file does not appear to be a PDF: %q", path)
			}
			return nil
		})
		if err != nil && walkErr == nil {
			walkErr = fmt.Errorf("walking %q: %w", root, err)
		}
	}

	return files, walkErr
}

// grepFile runs pdfgrep on a single file and returns its output and exit
// code. pdfgrep exits 0 on a match, 1 when no match was found, and 2 on
// error.
func grepFile(flags []string, pattern, path string) fileResult {
	args := append([]string{pdfgrepPath}, flags...)
	args = append(args, pattern, path)

	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			// Exit code 1 ("no matches") is a normal outcome; only 2
			// is worth logging.
			if exitErr.ExitCode() == 2 {
				if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
					log.Printf("pdfgrep failed on %s: %s", path, msg)
				} else {
					log.Printf("pdfgrep failed on %s: %v", path, exitErr)
				}
			}
			return fileResult{exitCode: exitErr.ExitCode()}
		}
		log.Printf("running %s: %v", pdfgrepPath, err)
		return fileResult{exitCode: 1}
	}

	return fileResult{output: out}
}

// grepFiles greps every path with at most parallelism pdfgrep processes
// running concurrently. Results are returned in the same order as paths.
func grepFiles(flags []string, pattern string, paths []string, parallelism int) []fileResult {
	results := make([]fileResult, len(paths))

	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup

	for i, p := range paths {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()

			sem <- struct{}{} // block until a worker slot is free
			defer func() { <-sem }()

			results[i] = grepFile(flags, pattern, p)
		}(i, p)
	}

	wg.Wait()
	return results
}

func main() {
	code, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitError)
	}
	os.Exit(code)
}

// run executes ppdfgrep and returns its exit code. A non-nil error
// signals a tool error, which main reports and exits with exitError.
func run() (int, error) {
	if _, err := exec.LookPath(pdfgrepPath); err != nil {
		return 0, errors.New("pdfgrep must be installed to use this tool")
	}

	recurse, flags, positional := splitArgs(os.Args[1:])
	if len(positional) < 2 {
		return 0, fmt.Errorf("Usage: %s [OPTION...] PATTERN [FILE...]", filepath.Base(os.Args[0]))
	}

	pattern := positional[0]
	files, err := collectPDFs(recurse, positional[1:])
	if err != nil {
		return 0, err
	}

	results := grepFiles(flags, pattern, files, runtime.NumCPU())

	w := bufio.NewWriter(os.Stdout)
	code := exitOK
	for _, r := range results {
		if r.exitCode != 0 {
			code = exitNoHit
		}
		if r.exitCode == 0 && len(r.output) > 0 {
			if _, err := w.Write(r.output); err != nil {
				return 0, err
			}
		}
	}
	if err := w.Flush(); err != nil {
		return 0, err
	}

	return code, nil
}

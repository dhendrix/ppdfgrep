// This is a wrapper for `pdfgrep` that will run parallel instances for
// every PDF file specified or found in a directory hierarchy. Useful for
// pdfgrepping piles of datasheets.
//
// TODOs:
// - Improve argument handling
// - Consider using a native PDF library such as rsc.io/pdf

package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/h2non/filetype"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type File struct {
	filename  string
	buf       []byte
	buflen    int
	processed bool
}

var pdfgrep string = "pdfgrep" // assumes pdfgrep is in user's $PATH
var availableThreads int
var wg sync.WaitGroup

var (
	help          = flag.Bool("h", false, "Show this help")
	with_filename = flag.Bool("H", false, "Print the file name for each match")
	insensitive   = flag.Bool("i", false, "Case-insensitive")
	recurse       = flag.Bool("r", false, "Recurse into subdirectories")
)

func incrementAvailableThreads() {
	availableThreads++
}

func decrementAvailableThreads() {
	availableThreads--
}

func doPdfgrepExit(files []File, i int) {
	files[i].processed = true
	incrementAvailableThreads()
	wg.Done()
}

func doPdfgrep(expr string, files []File, i int) error {
	var err error

	defer doPdfgrepExit(files, i)

	args := []string{"pdfgrep"}
	if *insensitive == true {
		args = append(args, "-i")
	}
	if *with_filename == true {
		args = append(args, "-H")
	}
	args = append(args, expr)
	args = append(args, files[i].filename)

	decrementAvailableThreads()
	cmd := exec.Command(args[0], args[1:]...)
	files[i].buf, err = cmd.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if exitError.ExitCode() == 1 {
				return nil // No match found, but otherwise fine
			} else if exitError.ExitCode() == 2 {
				// Generic error, can also occur if path is a
				// directory or something
				return nil
			}
		}
	}

	files[i].buflen = len(files[i].buf)
	return nil
}

func isPDF(path string) bool {
	// Following examples from
	// https://github.com/h2non/filetype#supported-types
	file, _ := os.Open(path)
	header := make([]byte, 261)
	file.Read(header)

	if filetype.IsArchive(header) != true {
		return false
	}

	kind, _ := filetype.Match(header)
	if kind == filetype.Unknown {
		return false
	}

	return filetype.IsMIME(header, "application/pdf")

}

func getFileList(root string, files *[]File) error {
	return filepath.Walk(root, func(path string, osfi os.FileInfo, err error) error {
		// Soft error. Useful when permissions are insufficient to
		// stat one of the files.
		if err != nil {
			return nil
		}

		file := filepath.Base(path)

		// Skip ".", "..", and hidden files (beginning in '.')
		if file[0] == '.' || file == ".." {
			return nil
		}

		s, err := os.Lstat(path)
		if err != nil {
			log.Printf("Failed to lstat \"%s\"\n", path)
			return err
		}

		// Skip directories when non-recursive.
		if s.Mode().IsDir() {
			if !*recurse {
				return filepath.SkipDir
			}
			if root == path {
				return nil
			}
		} else if !isPDF(path) {
			return nil
		} else {
			var f File
			f.filename = path
			f.buflen = 0
			f.processed = false
			*files = append(*files, f)
		}

		return nil
	})
	return nil
}

func printUsage() {
	fmt.Printf("usage: %s [OPTION]... PATTERN [FILE]...\n", os.Args[0])
	fmt.Printf("Options:\n")
	flag.PrintDefaults()
}

func main() {
	var expr string

	flag.Parse()
	if *help {
		printUsage()
		os.Exit(0)
	}

	availableThreads = runtime.NumCPU()

	nonflag_args := flag.Args()
	if len(nonflag_args) < 2 {
		printUsage()
		os.Exit(1)
	}

	expr = nonflag_args[0]
	filenames := nonflag_args[1:]

	files := make([]File, 0)
	for _, f := range filenames {
		getFileList(f, &files)
	}

	for i, _ := range files {
		for availableThreads <= 0 {
			time.Sleep(10 * time.Millisecond)
		}
		wg.Add(1)
		go doPdfgrep(expr, files, i)
	}

	for i := 0; i < len(files); i++ {
		f := files[i]
		for f.processed == false {
			time.Sleep(1 * time.Millisecond)
			f = files[i]
		}

		if f.buflen == 0 {
			continue
		}

		w := bufio.NewWriter(os.Stdout)
		w.Write(f.buf)
		w.Flush()
	}

	wg.Wait()
}

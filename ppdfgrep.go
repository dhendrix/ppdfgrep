// This is a wrapper for `pdfgrep` that will run parallel instances for
// every PDF file specified or found in a directory hierarchy. Useful for
// pdfgrepping piles of datasheets.
//
// TODOs:
// - Consider using a native PDF library such as rsc.io/pdf

package main

import (
	"bufio"
	"fmt"
	"github.com/h2non/filetype"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type File struct {
	filename  string
	buf       []byte
	buflen    int
	processed bool
	retval    int
}

var pdfgrep string = "pdfgrep" // assumes pdfgrep is in user's $PATH
var availableThreads int
var wg sync.WaitGroup

var (
	flagRecurse bool
	nonflagArgs []string
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

func doPdfgrep(flags []string, expr string, files []File, i int) error {
	var err error

	defer doPdfgrepExit(files, i)

	args := []string{"pdfgrep"}
	for _, v := range flags {
		args = append(args, v)
	}
	args = append(args, expr)
	args = append(args, files[i].filename)

	cmd := exec.Command(args[0], args[1:]...)
	files[i].buf, err = cmd.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			rc := exitError.ExitCode()
			// According to pdfgrep man page:
			// - If 1, no match found but otherwise fine
			// - If 2, an error occurred
			if rc == 2 {
				log.Printf("Error occurred while grepping %s\n", files[i].filename)
			}
			files[i].retval = rc
			return err
		}
	}

	files[i].buflen = len(files[i].buf)
	files[i].retval = 0
	return nil
}

func isPDF(path string) bool {
	// Following examples from
	// https://github.com/h2non/filetype#supported-types
	file, _ := os.Open(path)
	header := make([]byte, 261)
	file.Read(header)
	file.Close()

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
			log.Println(err)
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
			if !flagRecurse {
				return filepath.SkipDir
			}
			if root == path {
				return nil
			}
		} else if !isPDF(path) {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".pdf" {
				log.Printf("File does not appar to be a PDF: \"%s\"\n", path)
			}
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

func processArgs(args []string) ([]string, []string) {
	flags := make([]string, 0)
	nonflags := make([]string, 0)

	for _, v := range args {
		if strings.HasPrefix(v, "-") == false {
			nonflags = append(nonflags, v)
		} else if strings.HasPrefix(v, "--") {
			// longopt
			if strings.Compare(v, "--recursive") == 0 {
				flagRecurse = true
				continue
			}
			flags = append(flags, v)
		} else {
			// one or more shortopts
			if strings.Contains(v, "r") == true {
				flagRecurse = true
				v = strings.Replace(v, "r", "", -1)
			}

			if len(v) > 1 {
				// v contains more than just a hypen
				flags = append(flags, v)
			}
		}
	}

	return flags, nonflags
}
func main() {
	var expr string
	var ret int = 0

	availableThreads = runtime.NumCPU()

	flags, nonflags := processArgs(os.Args[1:])

	if len(nonflags) < 2 {
		fmt.Printf("Usage: %s [OPTION...] PATTERN [FILE...]\n", path.Base(os.Args[0]))
		os.Exit(1)
	}

	expr = nonflags[0]
	filenames := nonflags[1:]
	files := make([]File, 0)
	for _, f := range filenames {
		getFileList(f, &files)
	}

	for i := range files {
		for availableThreads <= 0 {
			time.Sleep(100 * time.Millisecond)
		}
		wg.Add(1)
		decrementAvailableThreads()
		go doPdfgrep(flags, expr, files, i)
	}

	for i := 0; i < len(files); i++ {
		f := files[i]
		for f.processed == false {
			time.Sleep(100 * time.Millisecond)
			f = files[i]
		}

		if f.retval != 0 {
			ret = 1
		}

		if f.buflen == 0 {
			continue
		}

		w := bufio.NewWriter(os.Stdout)
		w.Write(f.buf)
		w.Flush()
	}

	wg.Wait()
	os.Exit(ret)
}

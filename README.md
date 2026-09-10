# ppdfgrep

A wrapper around [pdfgrep](https://github.com/pdfgrep/pdfgrep) that runs a
parallel instance of it for every PDF file specified on the command line, or
found while walking a directory hierarchy. Useful for grepping piles of
datasheets.

Matches are buffered and printed in order of the PDFs being searched so that
matches from different PDFs do not appear interleaved on the screen.

## Installation

```console
$ go install github.com/dhendrix/ppdfgrep@latest
```

or, from a checkout of this repository:

```console
$ go build
```

## Requirements

- Go
- [pdfgrep](https://github.com/pdfgrep/pdfgrep) installed and available in
  your `$PATH`

## Usage

```
ppdfgrep [OPTION...] PATTERN [FILE...]
```

`PATTERN` is a regular expression as understood by `pdfgrep`. `FILE...` are one
or more PDF files and/or directories; a directory is searched recursively only
when `-r` is given.

Options are forwarded to pdfgrep (for example `-i` or `-l`), with one
exception: `-r` / `--recursive`, which ppdfgrep consumes itself.

Examples:

```console
# grep a single file
$ ppdfgrep "LM358" datasheet.pdf

# list PDFs in . that contain a match
$ ppdfgrep -l "0x4005" .

# recursive, case-insensitive search that prints filenames
# containing "errata" along with context
$ ppdfgrep -riH "errata" datasheets/
```

## Exit codes

Mirroring pdfgrep's own convention:

| Code | Meaning                                                        |
| ---- | -------------------------------------------------------------- |
| `0`  | A match was found                                              |
| `1`  | No match was found, or pdfgrep failed on one or more files     |
| `2`  | Tool error: bad usage, pdfgrep not installed, unreadable input |

## Notes

- At most `runtime.NumCPU()` pdfgrep processes run concurrently
- Only files whose magic bytes identify them as PDFs are grepped. A file with
  a `.pdf` extension that is not actually a PDF is skipped with a warning.
- Hidden files (names beginning with `.`) are skipped, but hidden
  directories are still descended into.
- Any short option containing `r` enables recursion, e.g. `-ri` both searches
  recursively and forwards `-i` to pdfgrep.

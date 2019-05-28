package main

import (
	"bufio"
	"flag"
	"fmt"
	"hash/adler32"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

const maxSize = int64(math.MaxInt32) // ~1.99 Gbyte

const defaultPkgName = "vfs"

const usage = `NAME

	govfs - generate a virtual file system by embedding files and directories

SYNOPSIS
    
	govfs [OPTIONS]... [PATTERN::TARGETDIR]...
	
DESCRIPTION

	Embed files and directories into a Go binary 
	by generating a mini virtual file system.

	PATTERN is a shell GLOB file name pattern. The syntax of
	the pattern is the same as in filepath.Match.
	(https://golang.org/pkg/path/filepath/#Match)

	If a PATTERN matches directories each directory 
	will be copied recursively.

	TARGETDIR is a virtual absolute slash separated path 
	in the generated virtual file system.

	Multiple PATTERN::TARGETDIR pairs can be specified.

	The virtual file system will only be generated if the 
	option -o is specified.

	The io.Reader, io.Seeker and io.ReadSeeker interfaces are 
	supported by a virtual file.

OPTIONS

`

func printUsage() {
	fmt.Fprintf(os.Stderr, usage)
	flag.PrintDefaults()
}

func main() {
	flag.Usage = printUsage
	var (
		output   string
		pkgName  string
		testFile string
	)

	flag.StringVar(&output, "o", "",
		"path to output file, for example vfs/vfs.go")
	flag.StringVar(&pkgName, "p", defaultPkgName,
		"custom package name")
	flag.StringVar(&testFile, "t", "",
		"path to a file in the virtual file system "+
			"that will be used to generate tests, "+
			"interpreted only if option -o is specified")
	flag.Parse()

	if len(flag.Args()) == 0 {
		fmt.Println("missing mapping operand")
		printUsage()
		os.Exit(2)
	}

	pkgName = strings.TrimSpace(pkgName)
	if pkgName == "" {
		pkgName = defaultPkgName
	}

	v := &vfs{
		mappings: resolveSources(flag.Args()),
		pkgName:  pkgName,
	}

	var w *bufio.Writer
	output = strings.TrimSpace(output)
	if output != "" {
		dir := filepath.Dir(output)
		err := os.MkdirAll(dir, 0755)
		handleError(err, "could not create: %s", dir)
		o, err := os.Create(output)
		handleError(err, "could not create: %s", output)
		defer o.Close()
		w = bufio.NewWriter(o)
		v.w = w
	}
	v.Generate()

	if w != nil {
		err := w.Flush()
		handleError(err, "could not write: %s", output)
	}

	testFile = strings.TrimSpace(testFile)
	testFile = path.Clean(testFile)
	if output != "" && testFile != "" {
		if v.processed[testFile] == nil {
			errorExit("test file not found: %s", testFile)
		}

		testOutput := strings.Replace(output, ".go", "_test.go", 1)
		to, err := os.Create(testOutput)
		handleError(err, "could not create: %s", testOutput)
		defer to.Close()
		w := bufio.NewWriter(to)
		vt := vfsTests{
			w:        w,
			pkgName:  pkgName,
			testFile: v.processed[testFile],
		}
		vt.Generate()
		err = w.Flush()
		handleError(err, "could not write: %s", testOutput)
	}
}

func resolveSources(args []string) []*mapping {
	mappings := []*mapping{}
	for _, a := range args {
		i := strings.LastIndex(a, "::")
		if i == -1 {
			errorExit("invalid mapping: %s", a)
		}
		src := strings.TrimSpace(a[0:i])
		targetDir := strings.TrimSpace(a[i+2:])
		if src == "" || targetDir == "" || strings.Index(targetDir, `\`) != -1 || targetDir[0] != '/' {
			errorExit("invalid mapping: %s", a)
		}
		targetDir = path.Clean(targetDir)
		matches, err := filepath.Glob(src)
		handleError(err, "invalid mapping: %s", a)
		mappings = append(mappings, &mapping{src: matches, targetDir: targetDir, pattern: a})
	}
	return mappings
}

type mapping struct {
	src       []string
	targetDir string
	pattern   string
}

type file struct {
	path    string
	size    int64
	version string
}

type vfs struct {
	mappings  []*mapping
	w         io.Writer
	pkgName   string
	processed map[string]*file
	buf       []byte
}

func (v *vfs) Generate() {
	v.processed = map[string]*file{}
	v.buf = make([]byte, 4096)

	err := v.writeHeader()
	handleError(err, "could not write")

	for _, m := range v.mappings {
		if len(m.src) == 0 {
			fmt.Println("skip mapping, no matches:", m.pattern)
		}
		for _, s := range m.src {
			stat, err := os.Stat(s)
			handleError(err, "stat error: %s", s)

			if stat.Mode().IsRegular() || stat.Mode().IsDir() {
				v.walk(m.targetDir, s)
			} else {
				fmt.Println("skip source, not a regular file or directory:", s)
			}
		}
	}

	err = v.writeFooter()
	handleError(err, "could not write")
}

func (v *vfs) walk(targetDir string, src string) {
	base := filepath.Base(src)

	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		handleError(err, "could not visit: %s", p)

		if info.Mode().IsRegular() {
			rel, err := filepath.Rel(src, p)
			handleError(err, "could not compute relative path from %s to %s", src, p)
			rel = filepath.ToSlash(rel)

			target := path.Join(targetDir, base, rel)

			if _, ok := v.processed[target]; ok {
				errorExit("target already exists: %s", target)
			} else {
				fmt.Println(p, " -> ", target)
				version, err := v.writeFile(target, p, info.Size())
				handleError(err, "could not write: %s", target)
				v.processed[target] = &file{
					path:    target,
					size:    info.Size(),
					version: version,
				}
			}
		}
		return nil
	})
	handleError(err, "could not visit: %s", src)
}

func (v *vfs) writeHeader() error {
	if v.w == nil {
		return nil
	}

	tmpl, err := template.New("header").Parse(tmplHeader)
	if err != nil {
		return err
	}
	data := map[string]interface{}{
		"ts":      time.Now().Format(time.RFC3339),
		"pkgName": v.pkgName,
	}

	return tmpl.Execute(v.w, data)
}

var tmplHeader = `// Code generated with govfs. DO NOT EDIT.

// Package {{.pkgName}} provides an embedded virtual file system.
package {{.pkgName}}

import (
	"fmt"
	"io"
	"math"
)

const maxSize = int64(math.MaxInt32) // ~1.99 GiB

// File represents an open file descriptor.
type File struct {
	f *file
	path string
	offset int64
}

// Open opens a file. If the version is specified it must match 
// the version of the file. Open returns an error if the 
// specified file does not exist.
func Open(path, version string) (*File, error) {
	f, ok := store[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	if version != "" && f.version != version {
		return nil, fmt.Errorf("version mismatch (requested: %s, actual: %s): %s", 
			version, f.version, path)
	}
	return &File{f: f, path: path}, nil
}

// Path returns the path of the file.
func (f *File) Path() string {
	return f.path
}

// Version returns the version of the file.
// The version is computed based on the contents of the file
// using the adler32 hash function.
func (f *File) Version() string {
	return f.f.version
}

// Size returns the number of bytes of the file contents.
func (f *File) Size() int64 {
	return int64(len(f.f.contents))
}

// Read reads up to len(b) bytes into b. It returns the 
// number of bytes read (0 <= n <= len(b)) and any 
// error encountered.
func (f *File) Read(b []byte) (int, error) {
	bufLen := len(b)
	available := len(f.f.contents) - int(f.offset)
	if available == 0 {
		return 0, io.EOF
	}

	canRead := available
	if bufLen < available {
		canRead = bufLen
	}

	copy(b, f.f.contents[int(f.offset): int(f.offset)+canRead])
	f.offset += int64(canRead)
	return canRead, nil
}

// Seek sets the offset for the next Read to offset, 
// interpreted according to whence: io.SeekStart means 
// relative to the start of the file, io.SeekCurrent means 
// relative to the current offset, and io.SeekEnd means 
// relative to the end. Seek returns the new offset relative 
// to the start of the file and an error, if any.
func (f *File) Seek(offset int64, whence int) (int64, error) {
	if maxSize < offset {
		return 0, fmt.Errorf("invalid target offset: %d", offset)
	}
	newOffset := f.offset
	if whence == io.SeekStart {
		newOffset = offset
	} else if whence == io.SeekEnd {
		newOffset = int64(len(f.f.contents)) - offset
	} else if whence == io.SeekCurrent {
		newOffset = f.offset + offset
	} else {
		return 0, fmt.Errorf("invalid seek whence: %d", whence)
	}

	if maxSize < newOffset || newOffset < 0 || int64(len(f.f.contents)) < newOffset {
		return 0, fmt.Errorf("invalid target offset: %d", newOffset)
	}

	f.offset = newOffset
	return f.offset, nil
}

type file struct {
	contents []byte
	version string
}

var store map[string]*file = map[string]*file{
`

func (v *vfs) writeFooter() error {
	if v.w == nil {
		return nil
	}
	_, err := fmt.Fprintln(v.w, "}")
	return err
}

func (v *vfs) writeFile(target, src string, size int64) (string, error) {
	version := ""

	if v.w == nil {
		return version, nil
	}

	if maxSize < size {
		return version, fmt.Errorf("maximum allowed size exceeded: %v", maxSize)
	}

	f, err := os.Open(src)
	if err != nil {
		return version, err
	}
	defer f.Close()

	_, err = fmt.Fprintf(v.w, "\t\"%s\": &file{\n\t\tcontents: []byte{", target)
	if err != nil {
		return version, err
	}

	hash := adler32.New()

	for {
		n, err := f.Read(v.buf)
		if err != nil && err != io.EOF {
			return version, err
		}
		if err == io.EOF {
			break
		}
		_, err = hash.Write(v.buf[0:n])
		if err != nil {
			return version, err
		}
		for _, b := range v.buf[0:n] {
			_, err := fmt.Fprintf(v.w, "%d, ", b)
			if err != nil {
				return version, err
			}
		}
	}
	version = fmt.Sprintf("%x", hash.Sum(nil))
	_, err = fmt.Fprintf(v.w, "},\n\t\tversion: \"%s\",\n\t},\n", version)
	return version, err
}

type vfsTests struct {
	w        io.Writer
	pkgName  string
	testFile *file
}

func (t *vfsTests) Generate() {
	tmpl, err := template.New("test").Parse(tmplTest)
	handleError(err, "could not write tests")

	data := map[string]interface{}{
		"pkgName":         t.pkgName,
		"testFilePath":    t.testFile.path,
		"testFileSize":    t.testFile.size,
		"testFileVersion": t.testFile.version,
	}

	err = tmpl.Execute(t.w, data)
	handleError(err, "could not write tests")
}

var tmplTest = `// Code generated with govfs. DO NOT EDIT.

package {{.pkgName}}

import (
	"testing"
	"io"
)

func TestPath(t *testing.T) {
	t.Parallel()
	f, err := Open("{{.testFilePath}}", "{{.testFileVersion}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := "{{.testFilePath}}"
	if f.Path() != expectedPath {
		t.Fatalf("expected path %v, but got %v", expectedPath, f.Path())
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	f, err := Open("{{.testFilePath}}", "{{.testFileVersion}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedVersion := "{{.testFileVersion}}"
	if f.Version() != expectedVersion {
		t.Fatalf("expected version %v, but got %v", expectedVersion, f.Version())
	}
}

func TestSize(t *testing.T) {
	t.Parallel()
	f, err := Open("{{.testFilePath}}", "{{.testFileVersion}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedSize := int64({{.testFileSize}})
	if f.Size() != expectedSize {
		t.Fatalf("expected size %v, but got %v", expectedSize, f.Size())
	}
}

func TestReader(t *testing.T) {
	t.Parallel()
	f, err := Open("{{.testFilePath}}", "{{.testFileVersion}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	buf := make([]byte, {{.testFileSize}})
	n, err := f.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedBytes := {{.testFileSize}}
	if n != expectedBytes {
		t.Fatalf("expected bytes %v, but got %v", expectedBytes, n)
	}
}

func TestSeeker(t *testing.T) {
	t.Parallel()
	f, err := Open("{{.testFilePath}}", "{{.testFileVersion}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedSize := int64({{.testFileSize}})
	if size != expectedSize {
		t.Fatalf("expected size %v, but got %v", expectedSize, size)
	}
}
`

func handleError(err error, format string, a ...interface{}) {
	if err != nil {
		fmt.Printf("Error: ")
		fmt.Printf(format, a...)
		fmt.Println()
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}
}

func errorExit(format string, a ...interface{}) {
	fmt.Printf("Error: ")
	fmt.Printf(format, a...)
	fmt.Println()
	os.Exit(1)
}

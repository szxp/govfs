# govfs
Generate a virtual file system by embedding files and directories.

Run `govfs -h` for help.

```
NAME

        govfs - generate a virtual file system by embedding files

SYNOPSIS

        govfs [OPTIONS]... [PATTERN::TARGETDIR]...

DESCRIPTION

        Embed files into a Go binary by generating a mini
        virtual file system.

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

  -o string
        path to output file, for example vfs/vfs.go
  -p string
        custom package name (default "vfs")
  -t string
        path to a file in the virtual file system that will be used to generate tests, interpreted only if option -o is specified
```


## How to install
1. Make sure GOBIN path is set and is included in your PATH.
2. Run `go install`


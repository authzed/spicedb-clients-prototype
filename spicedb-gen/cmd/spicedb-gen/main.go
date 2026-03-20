package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/authzed/spicedb-clients/spicedb-gen/generator"
	"github.com/authzed/spicedb-clients/spicedb-gen/schema"

	// Register language generators.
	_ "github.com/authzed/spicedb-clients/spicedb-gen/golang"
	_ "github.com/authzed/spicedb-clients/spicedb-gen/java"
	_ "github.com/authzed/spicedb-clients/spicedb-gen/typescript"
)

func main() {
	schemaPath := flag.String("schema", "", "path to .zed schema file (required)")
	lang := flag.String("lang", "", "target language, e.g. \"typescript\" (required)")
	outPath := flag.String("out", "", "output file path (required)")
	flag.Parse()

	if *schemaPath == "" || *lang == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "error: --schema, --lang, and --out are all required")
		flag.Usage()
		os.Exit(1)
	}

	s, err := schema.ParseFile(*schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "schema parse error: %v\n", err)
		os.Exit(1)
	}

	gen, ok := generator.Registry[*lang]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown language %q; registered languages:", *lang)
		for name := range generator.Registry {
			fmt.Fprintf(os.Stderr, " %s", name)
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(2)
	}

	opts := parseLangOptions(*lang, os.Args[1:])
	files, err := gen.Generate(s, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generation error: %v\n", err)
		os.Exit(2)
	}

	for _, f := range files {
		dest := *outPath
		if len(files) > 1 {
			dest = filepath.Join(*outPath, f.Path)
		} else if info, err := os.Stat(*outPath); err == nil && info.IsDir() {
			dest = filepath.Join(*outPath, f.Path)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating output directory: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(dest, f.Content, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", dest, err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s\n", dest)
	}
}

// parseLangOptions scans args for --<lang>.<key>=<value> or --<lang>.<key> <value>
// flags and returns a map of key -> value pairs (with the lang prefix stripped).
func parseLangOptions(lang string, args []string) map[string]string {
	prefix := "--" + lang + "."
	opts := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, prefix) {
			continue
		}
		rest := arg[len(prefix):]
		if idx := strings.Index(rest, "="); idx >= 0 {
			opts[rest[:idx]] = rest[idx+1:]
		} else if i+1 < len(args) {
			opts[rest] = args[i+1]
			i++
		}
	}
	return opts
}

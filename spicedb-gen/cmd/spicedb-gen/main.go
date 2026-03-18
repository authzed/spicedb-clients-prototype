package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/authzed/spicedb-clients/spicedb-gen/generator"
	"github.com/authzed/spicedb-clients/spicedb-gen/schema"

	// Register language generators.
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

	files, err := gen.Generate(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generation error: %v\n", err)
		os.Exit(2)
	}

	for _, f := range files {
		dest := *outPath
		if len(files) > 1 {
			dest = filepath.Join(filepath.Dir(*outPath), f.Path)
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

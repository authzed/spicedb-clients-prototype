package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/authzed/spicedb-clients/spicedb-gen/generator"
	"github.com/authzed/spicedb-clients/spicedb-gen/schema"

	// Register language generators.
	_ "github.com/authzed/spicedb-clients/spicedb-gen/golang"
	_ "github.com/authzed/spicedb-clients/spicedb-gen/java"
	_ "github.com/authzed/spicedb-clients/spicedb-gen/python"
	_ "github.com/authzed/spicedb-clients/spicedb-gen/typescript"
)

func main() {
	// Extract lang-specific flags (--<lang>.<key>=<value>) before flag.Parse(),
	// since the standard flag package rejects unknown flags.
	filteredArgs, langOpts := extractLangOptions(os.Args[1:])
	os.Args = append([]string{os.Args[0]}, filteredArgs...)

	flag.Usage = usage
	schemaPath := flag.String("schema", "", "path to .zed schema file, or - to read the schema from stdin (required)")
	lang := flag.String("lang", "", "target language, e.g. \"typescript\" (required)")
	outPath := flag.String("out", "", "output file path (required)")
	flag.Parse()

	if *schemaPath == "" || *lang == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "error: --schema, --lang, and --out are all required")
		flag.Usage()
		os.Exit(1)
	}

	var s *schema.Schema
	var err error
	if *schemaPath == "-" {
		var text []byte
		text, err = io.ReadAll(os.Stdin)
		if err == nil {
			s, err = schema.ParseString(string(text))
		}
	} else {
		s, err = schema.ParseFile(*schemaPath)
	}
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

	opts := filterLangOptions(*lang, langOpts)
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

// usage prints the standard flags followed by each language's options.
func usage() {
	out := flag.CommandLine.Output()
	_, _ = fmt.Fprintf(out, "Usage: %s --schema <file|-> --lang <language> --out <path> [--<language>.<option>=<value> ...]\n\n", filepath.Base(os.Args[0]))
	flag.PrintDefaults()
	langs := make([]string, 0, len(generator.Registry))
	for name := range generator.Registry {
		langs = append(langs, name)
	}
	sort.Strings(langs)
	_, _ = fmt.Fprintln(out, "\nLanguage options:")
	for _, name := range langs {
		_, _ = fmt.Fprintf(out, "\n  %s\n", name)
		d, ok := generator.Registry[name].(generator.OptionDescriber)
		if !ok || len(d.Options()) == 0 {
			_, _ = fmt.Fprintln(out, "    (none)")
			continue
		}
		for _, o := range d.Options() {
			_, _ = fmt.Fprintf(out, "    --%s.%s=%s\n        %s\n", name, o.Name, o.Value, o.Description)
		}
	}
}

// extractLangOptions separates lang-specific flags (--<anything>.<key>=<value>)
// from standard flags. Returns the remaining args and the extracted lang args.
func extractLangOptions(args []string) (remaining []string, langArgs []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Match --word.word pattern (lang-specific flags contain a dot after --)
		if strings.HasPrefix(arg, "--") && strings.Contains(arg[2:], ".") {
			switch {
			case strings.Contains(arg, "="):
				langArgs = append(langArgs, arg)
			case i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
				// No = in the arg: the next arg is the value.
				langArgs = append(langArgs, arg, args[i+1])
				i++
			default:
				// A bare boolean flag such as --go.names-only.
				langArgs = append(langArgs, arg+"=true")
			}
		} else {
			remaining = append(remaining, arg)
		}
	}
	return
}

// filterLangOptions extracts options for the given language from lang args.
// E.g., for lang="java", --java.package=com.test becomes {"package": "com.test"}.
func filterLangOptions(lang string, langArgs []string) map[string]string {
	prefix := "--" + lang + "."
	opts := map[string]string{}
	for i := 0; i < len(langArgs); i++ {
		arg := langArgs[i]
		if !strings.HasPrefix(arg, prefix) {
			continue
		}
		rest := arg[len(prefix):]
		if idx := strings.Index(rest, "="); idx >= 0 {
			opts[rest[:idx]] = rest[idx+1:]
		} else if i+1 < len(langArgs) {
			opts[rest] = langArgs[i+1]
			i++
		}
	}
	return opts
}

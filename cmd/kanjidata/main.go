// Command kanjidata builds the kanji dictionary that cli-kanji reads.
//
// It is meant to be run on a development machine, not on the box you review on:
// it chews through 78 MB of XML and writes a SQLite file you then copy next to
// the kanji binary. Running it is a one-off, repeated only when EDRDG publishes
// a new release of either dictionary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/OmitNomis/cli-kanji/internal/importer"
)

func main() {
	raw := flag.String("raw", filepath.Join("data", "raw"), "directory holding the downloaded dictionaries")
	out := flag.String("out", filepath.Join("data", "dict.db"), "dictionary database to write")
	tier := flag.Int("tier", 1, "lowest JMdict commonness tier that becomes reviewable: 2 common, 1 also second tier, 0 every compound")
	refresh := flag.Bool("refresh", false, "re-download the dictionaries even if they are already present")
	fetchOnly := flag.Bool("fetch-only", false, "download the dictionaries and stop")
	quiet := flag.Bool("quiet", false, "only report the final summary")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `kanjidata builds the cli-kanji dictionary from KANJIDIC2 and JMdict.

Usage:
  kanjidata [flags]

The dictionaries are downloaded on first run and reused afterwards. Every
compound in JMdict is stored either way; -tier only decides how much of it is
scheduled for review.

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *tier < 0 || *tier > 2 {
		fmt.Fprintln(os.Stderr, "kanjidata: -tier must be 0, 1 or 2")
		os.Exit(2)
	}

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
	if *quiet {
		logf = func(string, ...any) {}
	}

	// Ctrl-C has to reach the build: it holds an open transaction over a
	// multi-hundred-megabyte write, and the temporary file wants cleaning up.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, *raw, *out, *tier, *refresh, *fetchOnly, logf); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "kanjidata: interrupted, nothing was changed")
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "kanjidata: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, raw, out string, tier int, refresh, fetchOnly bool, logf func(string, ...any)) error {
	if err := importer.Fetch(ctx, raw, refresh, logf); err != nil {
		return err
	}
	if fetchOnly {
		return nil
	}

	stats, err := importer.Build(ctx, importer.Options{
		KanjidicPath:  filepath.Join(raw, importer.KanjidicFile),
		JMdictPath:    filepath.Join(raw, importer.JMdictFile),
		DictPath:      out,
		MinWordCommon: tier,
		Log:           logf,
	})
	if err != nil {
		return err
	}

	size := "unknown size"
	if st, serr := os.Stat(out); serr == nil {
		size = fmt.Sprintf("%.1f MB", float64(st.Size())/(1<<20))
	}

	fmt.Printf("built %s (%s) in %s\n", out, size, stats.Elapsed.Round(1e8))
	fmt.Printf("  %d kanji (%d joyo), %d compounds, %d kanji links\n",
		stats.Kanji, stats.JoyoKanji, stats.Words, stats.WordKanji)
	fmt.Printf("  %d review items: %d kanji, %d words at tier %d and above\n",
		stats.KanjiItems+stats.WordItems, stats.KanjiItems, stats.WordItems, tier)
	return nil
}

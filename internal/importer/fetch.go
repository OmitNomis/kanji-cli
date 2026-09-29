package importer

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Source files, both from the Electronic Dictionary Research and Development
// Group and both used under CC BY-SA 4.0. The gzipped forms are 12 MB together
// against 78 MB uncompressed, and nothing ever needs them uncompressed on disk,
// so they are kept and read as-is.
const (
	KanjidicURL  = "http://www.edrdg.org/kanjidic/kanjidic2.xml.gz"
	JMdictURL    = "http://ftp.edrdg.org/pub/Nihongo/JMdict_e.gz"
	KanjidicFile = "kanjidic2.xml.gz"
	JMdictFile   = "JMdict_e.gz"
)

// Fetch downloads both dictionaries into dir, skipping any file that is already
// there unless refresh is set.
func Fetch(ctx context.Context, dir string, refresh bool, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	for _, src := range []struct{ url, name string }{
		{KanjidicURL, KanjidicFile},
		{JMdictURL, JMdictFile},
	} {
		path := filepath.Join(dir, src.name)
		if !refresh {
			if st, err := os.Stat(path); err == nil && st.Size() > 0 {
				logf("%s: already present (%s)", src.name, humanBytes(st.Size()))
				continue
			}
		}
		if err := download(ctx, src.url, path, logf); err != nil {
			return err
		}
	}
	return nil
}

func download(ctx context.Context, url, path string, logf func(string, ...any)) error {
	logf("%s: downloading ...", filepath.Base(path))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cli-kanji dictionary importer")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}

	// Write to a temporary file first so an interrupted download cannot be
	// mistaken for a complete one on the next run.
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("move %s into place: %w", path, err)
	}

	logf("%s: %s", filepath.Base(path), humanBytes(n))
	return nil
}

// open returns a reader for a dictionary file, transparently decompressing it
// when the name says it is gzipped.
func open(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".gz") {
		return f, nil
	}

	zr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s is not valid gzip: %w", path, err)
	}
	return gzipReadCloser{zr, f}, nil
}

// gzipReadCloser closes the decompressor and the file underneath it.
type gzipReadCloser struct {
	*gzip.Reader
	file *os.File
}

func (g gzipReadCloser) Close() error {
	err := g.Reader.Close()
	if ferr := g.file.Close(); err == nil {
		err = ferr
	}
	return err
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

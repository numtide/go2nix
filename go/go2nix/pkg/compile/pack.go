package compile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// packAppend appends ofiles to the archive afile, mirroring cmd/go's
// packInternal (cmd/go/internal/work/gc.go). cmd/go never runs cmd/pack,
// and cmd/pack is not among the tools shipped in GOTOOLDIR (as of Go
// 1.26): `go tool pack` first builds it from source into GOCACHE, which
// is empty in every build sandbox.
//
// Entries get mtime, uid and gid 0 and mode 0644 whatever the source file
// has (a .syso read from the Nix store is 0444), as in the archives
// `go build` writes.
func packAppend(afile string, ofiles []string) error {
	dst, err := os.OpenFile(afile, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer dst.Close() //nolint:errcheck // only for error returns
	w := bufio.NewWriter(dst)

	for _, ofile := range ofiles {
		src, err := os.Open(ofile)
		if err != nil {
			return err
		}
		fi, err := src.Stat()
		if err != nil {
			src.Close() //nolint:errcheck
			return err
		}
		// The ar name field is 16 bytes, not runes.
		name := fi.Name()
		if len(name) > 16 {
			name = name[:16]
		} else {
			name += strings.Repeat(" ", 16-len(name))
		}
		size := fi.Size()
		fmt.Fprintf(w, "%s%-12d%-6d%-6d%-8o%-10d`\n", name, 0, 0, 0, 0o644, size) //nolint:errcheck // bufio errors are sticky; Flush reports them
		n, err := io.Copy(w, src)
		src.Close() //nolint:errcheck
		if err == nil && n < size {
			err = io.ErrUnexpectedEOF
		} else if err == nil && n > size {
			err = errors.New("file larger than size reported by stat")
		}
		if err != nil {
			return fmt.Errorf("copying %s to %s: %w", ofile, afile, err)
		}
		// ar entries start on even offsets.
		if size&1 != 0 {
			w.WriteByte(0) //nolint:errcheck // sticky, see above
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}
	return dst.Close()
}
